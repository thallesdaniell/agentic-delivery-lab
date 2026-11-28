package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

// A constraint do banco e a lista em Go precisam ter os mesmos códigos.
func TestMigrationFailureCodesMatchGo(t *testing.T) {
	// A constraint vale como definida na última migration que a recria.
	sql, err := migrations.ReadFile("migrations/003_review_cycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range run.AllFailureCodes {
		if !strings.Contains(string(sql), "'"+string(c)+"'") {
			t.Errorf("código %q falta na constraint de run.failure_code", c)
		}
	}
}

// Os testes abaixo rodam contra um Postgres real. Sem TEST_DATABASE_URL, são pulados.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definido")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.db.Exec(ctx, `DROP TABLE IF EXISTS delivery, run, card, agent, skill, schema_migration CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCatalog(ctx, testCatalog("skill v1")); err != nil {
		t.Fatal(err)
	}
	return s
}

func testCatalog(skillBody string) catalog.Catalog {
	sk := catalog.Skill{Name: "python-feature", Description: "d", Content: skillBody, Version: catalog.Version([]byte(skillBody))}
	return catalog.Catalog{
		Skills: []catalog.Skill{sk},
		Agents: []catalog.Agent{{Name: "python-dev", Description: "d", Skill: sk.Name, Model: "claude-sonnet-5-5",
			BudgetUSD: 1, Tools: []string{"Read", "Edit"}, Version: "a-" + sk.Version}},
	}
}

func sampleCard(id string) card.Card {
	return card.Card{ID: id, Title: "Adicionar GET /health", Objective: "o", AcceptanceCriteria: []string{"a"},
		Repo: "thallesdaniell/agentic-delivery-target", Agent: "python-dev"}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := testStore(t)
	applied, err := s.Migrate(context.Background())
	if err != nil || len(applied) != 0 {
		t.Fatalf("segunda migração aplicou %v, %v", applied, err)
	}
}

func TestQueueClaimsOnceAndBlocksDuplicateRuns(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateCard(ctx, sampleCard("ADL-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("segundo run ativo deveria ser recusado, veio %v", err)
	}
	if _, err := s.MarkReady(ctx, "NAO-EXISTE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("card inexistente: %v", err)
	}
	first, err := s.ClaimRun(ctx)
	if err != nil || first == nil || first.Card.ID != "ADL-1" || len(first.Card.AcceptanceCriteria) != 1 {
		t.Fatalf("claim: %+v %v", first, err)
	}
	second, err := s.ClaimRun(ctx)
	if err != nil || second != nil {
		t.Fatalf("a fila deveria estar vazia: %+v %v", second, err)
	}
}

func TestFinishRunSuccessRecordsDelivery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	_, _ = s.MarkReady(ctx, "ADL-1")
	cl, _ := s.ClaimRun(ctx)
	res := run.Result{Status: run.StatusSucceeded, Branch: "agent/adl-1-x", Model: "m", CostUSD: 0.04, NumTurns: 12,
		Handoff:  &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "s", TestsRun: []string{"t"}, Limitations: []string{}},
		PRNumber: 2, PRURL: "https://github.com/o/r/pull/2"}
	if err := s.FinishRun(ctx, cl.RunID, res, "log"); err != nil {
		t.Fatal(err)
	}
	cards, err := s.ListCards(ctx)
	if err != nil || len(cards) != 1 {
		t.Fatalf("list: %v %v", cards, err)
	}
	v := cards[0]
	if v.Status != CardInReview || v.Phase == nil || *v.Phase != PhaseCIPending || v.PRURL == nil || *v.PRURL != res.PRURL || *v.CostUSD != 0.04 {
		t.Fatalf("card depois do sucesso: %+v", v)
	}
	if err := s.FinishRun(ctx, cl.RunID, res, ""); err == nil {
		t.Fatal("um run já finalizado não pode ser finalizado de novo")
	}
}

func TestFinishRunFailureMovesCardAndAllowsRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	_, _ = s.MarkReady(ctx, "ADL-1")
	cl, _ := s.ClaimRun(ctx)
	res := run.Result{CardID: "ADL-1"}
	res.Failed(run.Fail(run.FailureBudgetExceeded, "teto"))
	if err := s.FinishRun(ctx, cl.RunID, res, ""); err != nil {
		t.Fatal(err)
	}
	cards, _ := s.ListCards(ctx)
	if cards[0].Status != CardFailed || *cards[0].FailureCode != "budget_exceeded" {
		t.Fatalf("card: %+v", cards[0])
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); err != nil {
		t.Fatalf("depois de falhar, o card pode voltar à fila: %v", err)
	}
}

func TestDatabaseRejectsFailureWithoutCode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	runID, _ := s.MarkReady(ctx, "ADL-1")
	if _, err := s.db.Exec(ctx, `UPDATE run SET status = 'failed' WHERE id = $1`, runID); err == nil {
		t.Fatal("o banco deveria recusar falha sem failure_code")
	}
}

func TestRecoverInterrupted(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	_, _ = s.MarkReady(ctx, "ADL-1")
	_, _ = s.ClaimRun(ctx)
	n, err := s.RecoverInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	cards, _ := s.ListCards(ctx)
	if cards[0].Status != CardFailed || *cards[0].FailureCode != "interrupted" {
		t.Fatalf("card: %+v", cards[0])
	}
}

func TestReadyRequiresAnActiveAgent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	c := sampleCard("ADL-1")
	c.Agent = ""
	if err := s.CreateCard(ctx, c); err != nil {
		t.Fatalf("card sem agente vai para o backlog: %v", err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("sem agente não vai para Pronto: %v", err)
	}
	if err := s.AssignAgent(ctx, "ADL-1", "nao-existe"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("agente inexistente: %v", err)
	}
	bad := sampleCard("ADL-2")
	bad.Agent = "nao-existe"
	if err := s.CreateCard(ctx, bad); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("criar com agente inexistente: %v", err)
	}
	if err := s.AssignAgent(ctx, "ADL-1", "python-dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignAgent(ctx, "ADL-1", "python-dev"); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("não troca o agente com run ativo: %v", err)
	}
}

// O run roda o agente do momento em que entrou na fila, mesmo que o catálogo mude depois.
func TestRunFreezesTheAgentSpec(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	runID, err := s.MarkReady(ctx, "ADL-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCatalog(ctx, testCatalog("skill v2")); err != nil {
		t.Fatal(err)
	}
	cl, err := s.ClaimRun(ctx)
	if err != nil || cl == nil {
		t.Fatalf("claim: %v", err)
	}
	if cl.Spec.SkillContent != "skill v1" || cl.Spec.Model != "claude-sonnet-5-5" || len(cl.Spec.Tools) != 2 || cl.Card.Agent != "python-dev" {
		t.Fatalf("spec congelado errado: %+v", cl.Spec)
	}
	r, err := s.GetRun(ctx, runID)
	if err != nil || r.SkillVersion == nil || *r.SkillVersion != catalog.Version([]byte("skill v1")) {
		t.Fatalf("run sem a versão da skill: %+v %v", r, err)
	}
}

func TestCardInReviewCannotGoBackToReady(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	_, _ = s.MarkReady(ctx, "ADL-1")
	cl, _ := s.ClaimRun(ctx)
	res := run.Result{Status: run.StatusSucceeded, Branch: "b", PRNumber: 3, PRURL: "https://github.com/o/r/pull/3",
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "s", TestsRun: []string{"t"}, Limitations: []string{}}}
	if err := s.FinishRun(ctx, cl.RunID, res, "log"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("card com PR aberto não volta para Pronto: %v", err)
	}
	d, err := s.GetCard(ctx, "ADL-1")
	if err != nil || len(d.Runs) != 1 || d.Runs[0].PRNumber == nil || *d.Runs[0].PRNumber != 3 {
		t.Fatalf("detalhe do card: %+v %v", d, err)
	}
}

func TestCatalogSyncDeactivatesRemovedAgents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	empty := testCatalog("skill v1")
	empty.Agents = nil
	if err := s.SyncCatalog(ctx, empty); err != nil {
		t.Fatal(err)
	}
	agents, err := s.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Active {
		t.Fatalf("agente removido do repositório deveria ficar inativo: %+v %v", agents, err)
	}
	c := sampleCard("ADL-1")
	if err := s.CreateCard(ctx, c); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("agente inativo não pode ser atribuído: %v", err)
	}
	if err := s.SyncCatalog(ctx, testCatalog("skill v1")); err != nil {
		t.Fatal(err)
	}
	if agents, _ := s.ListAgents(ctx); !agents[0].Active {
		t.Fatal("agente de volta ao repositório volta a ficar ativo")
	}
}

func TestUpdateRunLogOnlyWhileRunning(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_ = s.CreateCard(ctx, sampleCard("ADL-1"))
	runID, _ := s.MarkReady(ctx, "ADL-1")
	_ = s.UpdateRunLog(ctx, runID, "antes")
	if r, _ := s.GetRun(ctx, runID); r.Log != nil {
		t.Fatal("run na fila não tem log")
	}
	_, _ = s.ClaimRun(ctx)
	_ = s.UpdateRunLog(ctx, runID, "clonando")
	if r, _ := s.GetRun(ctx, runID); r.Log == nil || *r.Log != "clonando" {
		t.Fatalf("log ao vivo: %+v", r.Log)
	}
}

// openPR leva um card até Em revisão, com o PR 3 aberto pelo primeiro run.
func openPR(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateCard(ctx, sampleCard(id)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, id); err != nil {
		t.Fatal(err)
	}
	cl, _ := s.ClaimRun(ctx)
	if err := s.FinishRun(ctx, cl.RunID, okRun(), "log"); err != nil {
		t.Fatal(err)
	}
}

func okRun() run.Result {
	return run.Result{Status: run.StatusSucceeded, Branch: "agent/adl-1-x", PRNumber: 3, PRURL: "https://github.com/o/r/pull/3",
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "s", TestsRun: []string{"t"}, Limitations: []string{}}}
}

func TestReworkGoesBackToTheAgentOnTheSamePR(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	q, err := s.ReviewQueue(ctx)
	if err != nil || len(q) != 1 || q[0].PRNumber != 3 {
		t.Fatalf("fila de revisão: %+v %v", q, err)
	}

	if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindFixCI, "log do CI", nil); err != nil {
		t.Fatal(err)
	}
	cl, _ := s.ClaimRun(ctx)
	want := run.Task{Kind: run.KindFixCI, Feedback: "log do CI", PRNumber: 3, PRURL: "https://github.com/o/r/pull/3", Attempt: 1}
	if cl.Task != want || cl.Spec.Agent != "python-dev" {
		t.Fatalf("run de correção: %+v %+v", cl.Task, cl.Spec)
	}
	if err := s.FinishRun(ctx, cl.RunID, okRun(), "log"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.GetCard(ctx, "ADL-1")
	if d.Status != CardInReview || d.CIAttempts != 1 || len(d.Runs) != 2 || d.Runs[0].Kind != run.KindFixCI || *d.PRNumber != 3 {
		t.Fatalf("card depois da correção: %+v", d)
	}
	r, _ := s.GetRun(ctx, cl.RunID)
	if r.Feedback == nil || *r.Feedback != "log do CI" {
		t.Fatalf("o run guarda a entrada: %+v", r.Feedback)
	}
}

func TestReworkRespectsTheLimitAndOnlyLeavesReview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	for i := 1; i <= run.MaxCIAttempts; i++ {
		if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindFixCI, "log", nil); err != nil {
			t.Fatalf("tentativa %d: %v", i, err)
		}
		if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindFixCI, "log", nil); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("com o run na fila, o card não está em revisão: %v", err)
		}
		cl, _ := s.ClaimRun(ctx)
		if err := s.FinishRun(ctx, cl.RunID, okRun(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindFixCI, "log", nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("passou do limite de tentativas: %v", err)
	}
}

func TestCardCompletesOnMergeAndFailsWithCardCode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	openPR(t, s, "ADL-2")
	if err := s.SetReviewPhase(ctx, "ADL-1", PhaseApproved); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteCard(ctx, "ADL-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteCard(ctx, "ADL-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("card concluído não conclui de novo: %v", err)
	}
	if err := s.FailCard(ctx, "ADL-2", run.FailureCIFailedAfterRetries, "CI vermelho"); err != nil {
		t.Fatal(err)
	}
	if err := s.FailCard(ctx, "ADL-2", run.FailureTimeout, "x"); err == nil {
		t.Fatal("código de run não serve para card")
	}
	cards, _ := s.ListCards(ctx)
	if cards[0].Status != CardDone || cards[0].Phase != nil {
		t.Fatalf("card concluído: %+v", cards[0])
	}
	if cards[1].Status != CardFailed || *cards[1].FailureCode != "ci_failed_after_retries" || *cards[1].FailureDetail != "CI vermelho" {
		t.Fatalf("card que falhou no ciclo: %+v", cards[1])
	}
}

func TestRetryAfterCIGaveUpRepeatsTheFixOnTheSamePR(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindFixCI, "log 1", nil); err != nil {
		t.Fatal(err)
	}
	cl, _ := s.ClaimRun(ctx)
	_ = s.FinishRun(ctx, cl.RunID, okRun(), "")
	if err := s.FailCard(ctx, "ADL-1", run.FailureCIFailedAfterRetries, "desisti"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); err != nil {
		t.Fatal(err)
	}
	cl, _ = s.ClaimRun(ctx)
	if cl.Task.Kind != run.KindFixCI || cl.Task.Attempt != 1 || cl.Task.Feedback != "log 1" || cl.Task.PRNumber != 3 {
		t.Fatalf("tentar de novo deveria repetir a correção no PR: %+v", cl.Task)
	}
}

func TestRetryAfterPRClosedImplementsAgain(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	if err := s.FailCard(ctx, "ADL-1", run.FailurePRClosed, "fechado"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkReady(ctx, "ADL-1"); err != nil {
		t.Fatal(err)
	}
	cl, _ := s.ClaimRun(ctx)
	if cl.Task.Kind != run.KindImplement {
		t.Fatalf("com o PR fechado, o card é implementado de novo: %+v", cl.Task)
	}
}

func TestReviewReworkRecordsTheHandledReview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	openPR(t, s, "ADL-1")
	id := int64(991)
	if _, err := s.EnqueueRework(ctx, "ADL-1", run.KindAddressReview, "comentários", &id); err != nil {
		t.Fatal(err)
	}
	cl, _ := s.ClaimRun(ctx)
	_ = s.FinishRun(ctx, cl.RunID, okRun(), "")
	q, _ := s.ReviewQueue(ctx)
	if len(q) != 1 || q[0].LastReviewID == nil || *q[0].LastReviewID != 991 || q[0].ReviewRounds != 1 {
		t.Fatalf("revisão atendida: %+v", q)
	}
}
