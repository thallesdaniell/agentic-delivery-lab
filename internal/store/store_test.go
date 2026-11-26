package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

// A constraint do banco e a lista em Go precisam ter os mesmos códigos.
func TestMigrationFailureCodesMatchGo(t *testing.T) {
	sql, err := migrations.ReadFile("migrations/001_init.sql")
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
	if _, err := s.db.Exec(ctx, `DROP TABLE IF EXISTS delivery, run, card, schema_migration CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleCard(id string) card.Card {
	return card.Card{ID: id, Title: "Adicionar GET /health", Objective: "o", AcceptanceCriteria: []string{"a"},
		Repo: "thallesdaniell/agentic-delivery-target", Skill: "python-feature"}
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
	if v.Status != CardPROpen || v.PRURL == nil || *v.PRURL != res.PRURL || *v.CostUSD != 0.04 {
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
