package board

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

type fakeStore struct {
	cards   map[string]*store.CardDetail
	created []card.Card
	moveFn  func(id string, to store.CardStatus) error
	fixed   map[string]string
	reruns  []string
}

func newFake() *fakeStore {
	f := &fakeStore{cards: map[string]*store.CardDetail{}}
	f.cards["ADL-1"] = &store.CardDetail{CardView: store.CardView{Card: card.Card{ID: "ADL-1", Title: "Adicionar GET /health",
		Objective: "o", AcceptanceCriteria: []string{"a"}, Repo: "o/r", Agent: "python-dev"}, Status: store.CardBacklog}}
	f.cards["ADL-2"] = &store.CardDetail{CardView: store.CardView{Card: card.Card{ID: "ADL-2", Title: "<script>alert(1)</script>",
		Objective: "o", AcceptanceCriteria: []string{"a"}, Repo: "o/r"}, Status: store.CardBacklog}}
	return f
}

func (f *fakeStore) ListCards(context.Context) ([]store.CardView, error) {
	var out []store.CardView
	for _, id := range []string{"ADL-1", "ADL-2"} {
		out = append(out, f.cards[id].CardView)
	}
	return out, nil
}

func (f *fakeStore) GetCard(_ context.Context, id string) (*store.CardDetail, error) {
	if c, ok := f.cards[id]; ok {
		return c, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GetRun(_ context.Context, id int64) (*store.RunDetail, error) {
	if id != 7 {
		return nil, store.ErrNotFound
	}
	code, detail, log := "agent_blocked", "não deu", "clonando\nagente trabalhando"
	return &store.RunDetail{RunSummary: store.RunSummary{ID: 7, CardID: "ADL-1", Status: "failed", FailureCode: &code, CreatedAt: time.Now()},
		CardTitle: "t", Detail: &detail, Log: &log, Denials: []string{"Bash {\"command\":\"cat x\"}"},
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeBlocked, Summary: "resumo do agente"}}, nil
}

func (f *fakeStore) CreateCard(_ context.Context, c card.Card) error {
	f.created = append(f.created, c)
	return nil
}

func (f *fakeStore) AssignAgent(_ context.Context, id, agent string) error {
	f.cards[id].Agent = agent
	return nil
}

// Move aplica a mesma regra do store real (store.CanMove), para o teste pegar a interface fora da regra.
func (f *fakeStore) Move(_ context.Context, id string, to store.CardStatus) (int64, error) {
	if f.moveFn != nil {
		if err := f.moveFn(id, to); err != nil {
			return 0, err
		}
	}
	c := f.cards[id]
	if !store.CanMove(c.Status, c.Agent != "", to) {
		return 0, store.ErrInvalidTransition
	}
	c.Status = to
	return 1, nil
}

func (f *fakeStore) RequestRework(_ context.Context, id, note string) (int64, error) {
	if f.fixed == nil {
		f.fixed = map[string]string{}
	}
	f.fixed[id] = note
	return 2, nil
}

func (f *fakeStore) RequestCIRerun(_ context.Context, id string) error {
	f.reruns = append(f.reruns, id)
	return nil
}

func (f *fakeStore) ListAgents(context.Context) ([]store.AgentView, error) {
	return []store.AgentView{
		{Agent: catalog.Agent{Name: "python-dev", Model: "claude-sonnet-5-5", BudgetUSD: 1, Skill: "python-feature", Version: "abc"}, Active: true},
		{Agent: catalog.Agent{Name: "velho", Model: "m", BudgetUSD: 1, Skill: "python-feature"}, Active: false},
	}, nil
}

func (f *fakeStore) ListSkills(context.Context) ([]store.SkillView, error) {
	return []store.SkillView{{Skill: catalog.Skill{Name: "python-feature", Description: "d", Content: "# skill", Version: "v1"}}}, nil
}

func server(t *testing.T, st Store) http.Handler {
	t.Helper()
	s, err := New(st, "thallesdaniell/agentic-delivery-target", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

func do(t *testing.T, h http.Handler, method, target string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBoardShowsColumnsAndEscapesCardText(t *testing.T) {
	rec := do(t, server(t, newFake()), "GET", "/", nil, false)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Backlog", "Pronto", "Executando", "Em revisão", "Concluído", "Falhou", "ADL-1", "Executar", "sem agente"} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q no board", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("o texto do card precisa sair escapado")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("falta a CSP")
	}
}

// A interface marca o card com os movimentos da regra: card sem agente não arrasta nem tem botão.
func TestBoardMarksTheMovesOfTheRule(t *testing.T) {
	body := do(t, server(t, newFake()), "GET", "/partials/columns", nil, false).Body.String()
	if !strings.Contains(body, `data-card="ADL-1" data-moves="ready running"`) || !strings.Contains(body, `data-card="ADL-2" data-moves=""`) {
		t.Fatalf("movimentos no card:\n%s", body)
	}
	if strings.Count(body, `draggable="true"`) != 1 || strings.Count(body, ">Executar<") != 1 {
		t.Fatalf("só o ADL-1 tem agente:\n%s", body)
	}
	for _, col := range []string{`data-status="backlog"`, `data-status="running"`, `data-status="in_review"`} {
		if !strings.Contains(body, col) {
			t.Errorf("coluna sem %s", col)
		}
	}
	if !strings.Contains(body, `hx-confirm="Executar ADL-1 com python-dev`) {
		t.Fatal("executar gasta dinheiro: precisa de confirmação")
	}
}

func TestWritesRequireHTMXHeaderAndSameOrigin(t *testing.T) {
	h := server(t, newFake())
	if rec := do(t, h, "POST", "/cards/ADL-1/move", url.Values{"to": {"running"}}, false); rec.Code != http.StatusForbidden {
		t.Fatalf("POST sem HX-Request: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/cards/ADL-1/move", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST de outra origem: %d", rec.Code)
	}
}

func TestMoveFromBoardReturnsColumns(t *testing.T) {
	st := newFake()
	rec := do(t, server(t, st), "POST", "/cards/ADL-1/move", url.Values{"to": {"running"}, "view": {"board"}}, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="columns"`) {
		t.Fatalf("status %d:\n%s", rec.Code, rec.Body.String())
	}
	if st.cards["ADL-1"].Status != store.CardRunning {
		t.Fatal("soltar em Executando deveria pôr o card em Executando")
	}
}

// Arrastar para uma coluna fora da regra é recusado pelo servidor, mesmo que a interface deixe passar.
func TestMoveOutsideTheRuleIsRefused(t *testing.T) {
	st := newFake()
	rec := do(t, server(t, st), "POST", "/cards/ADL-1/move", url.Values{"to": {"done"}, "view": {"board"}}, true)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "não está na regra") || st.cards["ADL-1"].Status != store.CardBacklog {
		t.Fatalf("mover para Concluído: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMoveMapsDomainErrors(t *testing.T) {
	cases := map[error]int{store.ErrNoAgent: 422, store.ErrActiveRun: 409, store.ErrInvalidTransition: 409,
		store.ErrNothingToFix: 409, store.ErrLimitReached: 409}
	for e, code := range cases {
		st := newFake()
		st.moveFn = func(string, store.CardStatus) error { return e }
		rec := do(t, server(t, st), "POST", "/cards/ADL-1/move", url.Values{"to": {"ready"}}, true)
		if rec.Code != code || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%v: status %d", e, rec.Code)
		}
	}
}

func TestCreateCardValidatesAndRedirects(t *testing.T) {
	st := newFake()
	h := server(t, st)
	bad := url.Values{"id": {"ADL 3"}, "title": {"t"}, "objective": {"o"}, "criteria": {"a"}, "repo": {"o/r"}}
	if rec := do(t, h, "POST", "/cards", bad, true); rec.Code != 422 || !strings.Contains(rec.Body.String(), "fora do formato") {
		t.Fatalf("id inválido: %d %s", rec.Code, rec.Body.String())
	}
	good := url.Values{"id": {"ADL-3"}, "title": {"Filtrar tarefas"}, "objective": {"o"},
		"criteria": {"- primeiro\n\nsegundo\n"}, "repo": {"o/r"}, "agent": {"python-dev"}}
	rec := do(t, h, "POST", "/cards", good, true)
	if rec.Code != 200 || rec.Header().Get("HX-Redirect") != "/cards/ADL-3" {
		t.Fatalf("criar: %d %v", rec.Code, rec.Header())
	}
	c := st.created[len(st.created)-1]
	if len(c.AcceptanceCriteria) != 2 || c.AcceptanceCriteria[0] != "primeiro" || c.Agent != "python-dev" {
		t.Fatalf("card criado: %+v", c)
	}
}

func TestNewCardFormListsOnlyActiveAgents(t *testing.T) {
	body := do(t, server(t, newFake()), "GET", "/cards/new", nil, false).Body.String()
	if !strings.Contains(body, `value="python-dev"`) || strings.Contains(body, `value="velho"`) {
		t.Fatalf("formulário:\n%s", body)
	}
	if !strings.Contains(body, `value="thallesdaniell/agentic-delivery-target"`) {
		t.Fatal("repositório padrão ausente")
	}
}

func TestRunPageShowsFailureHandoffDenialsAndLog(t *testing.T) {
	h := server(t, newFake())
	body := do(t, h, "GET", "/runs/7", nil, false).Body.String()
	for _, want := range []string{"agent_blocked", "não deu", "resumo do agente", "Tools negadas", "agente trabalhando"} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q na página do run", want)
		}
	}
	if do(t, h, "GET", "/runs/8", nil, false).Code != 404 || do(t, h, "GET", "/runs/x", nil, false).Code != 404 {
		t.Fatal("run inexistente deveria dar 404")
	}
}

func TestCardPageAndCatalog(t *testing.T) {
	h := server(t, newFake())
	if rec := do(t, h, "GET", "/cards/ADL-1", nil, false); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Nenhum run ainda") {
		t.Fatalf("card: %d", rec.Code)
	}
	if do(t, h, "GET", "/cards/NAO", nil, false).Code != 404 {
		t.Fatal("card inexistente deveria dar 404")
	}
	body := do(t, h, "GET", "/catalog", nil, false).Body.String()
	if !strings.Contains(body, "python-dev") || !strings.Contains(body, "inativo") || !strings.Contains(body, "# skill") {
		t.Fatalf("catálogo:\n%s", body)
	}
	if rec := do(t, h, "GET", "/static/htmx.min.js", nil, false); rec.Code != 200 {
		t.Fatalf("htmx embutido: %d", rec.Code)
	}
}

func TestBoardShowsThePRPhase(t *testing.T) {
	f := newFake()
	phase := store.PhaseAwaitingReview
	pr, url := 3, "https://github.com/o/r/pull/3"
	f.cards["ADL-1"].Status, f.cards["ADL-1"].Phase = store.CardInReview, &phase
	f.cards["ADL-1"].PRNumber, f.cards["ADL-1"].PRURL = &pr, &url
	body := do(t, server(t, f), "GET", "/", nil, false).Body.String()
	for _, want := range []string{"aguardando revisão", "phase-awaiting_review", "Arraste o card"} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q no board", want)
		}
	}
}

func TestFailedCardCanRunAgain(t *testing.T) {
	f := newFake()
	code, detail := "pr_closed", "o PR #3 foi fechado sem merge"
	c := f.cards["ADL-1"]
	c.Status, c.FailureCode, c.FailureDetail = store.CardFailed, &code, &detail
	body := do(t, server(t, f), "GET", "/cards/ADL-1", nil, false).Body.String()
	for _, want := range []string{"Executar de novo", "pr_closed"} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q na página do card", want)
		}
	}
}

func inReviewWith(f *fakeStore, phase store.ReviewPhase, pending bool) *store.CardDetail {
	c := f.cards["ADL-1"]
	c.Status, c.Phase, c.HasPending = store.CardInReview, &phase, pending
	pr := "https://github.com/o/r/pull/5"
	c.PRURL = &pr
	return c
}

func TestRedCIOffersFixAndRerunWithoutMovingTheCard(t *testing.T) {
	f := newFake()
	inReviewWith(f, store.PhaseCIFailed, true)
	h := server(t, f)
	body := do(t, h, "GET", "/partials/columns", nil, false).Body.String()
	for _, want := range []string{"CI vermelho", "Corrigir com o agente", "Rodar o CI de novo", `data-card="ADL-1" data-moves=""`} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q no board", want)
		}
	}
	page := do(t, h, "GET", "/cards/ADL-1", nil, false).Body.String()
	if !strings.Contains(page, `name="note"`) || !strings.Contains(page, "o log do CI") {
		t.Fatalf("a página do card deveria ter a orientação para o agente:\n%s", page)
	}
	if rec := do(t, h, "POST", "/cards/ADL-1/fix", url.Values{"note": {"corrija o código"}}, true); rec.Code != 200 || f.fixed["ADL-1"] != "corrija o código" {
		t.Fatalf("corrigir: %d %v", rec.Code, f.fixed)
	}
	if rec := do(t, h, "POST", "/cards/ADL-1/rerun-ci", url.Values{"view": {"board"}}, true); rec.Code != 200 || len(f.reruns) != 1 {
		t.Fatalf("rodar o CI de novo: %d %v", rec.Code, f.reruns)
	}
}

func TestReworkInProgressAndLimitAreLabelsNotButtons(t *testing.T) {
	f := newFake()
	c := inReviewWith(f, store.PhaseCIFailed, true)
	c.CIAttempts = 2
	body := do(t, server(t, f), "GET", "/partials/columns", nil, false).Body.String()
	if !strings.Contains(body, "limite de correções") || strings.Contains(body, "Corrigir com o agente") {
		t.Fatalf("no limite, o botão some e a etiqueta avisa:\n%s", body)
	}

	f = newFake()
	c = inReviewWith(f, store.PhaseCIFailed, true)
	kind, attempt, status := run.KindFixCI, 1, "running"
	c.RunKind, c.RunAttempt, c.RunStatus = &kind, &attempt, &status
	body = do(t, server(t, f), "GET", "/partials/columns", nil, false).Body.String()
	if !strings.Contains(body, "corrigindo · correção do CI 1/2") || strings.Contains(body, "Rodar o CI de novo") {
		t.Fatalf("com a correção rodando, o card mostra a etiqueta:\n%s", body)
	}
}

func TestBoardServesTheDragScript(t *testing.T) {
	h := server(t, newFake())
	if rec := do(t, h, "GET", "/static/board.js", nil, false); rec.Code != 200 || !strings.Contains(rec.Body.String(), "data-moves") {
		t.Fatalf("board.js: %d", rec.Code)
	}
	page := do(t, h, "GET", "/", nil, false).Body.String()
	if !strings.Contains(page, `<script src="/static/board.js" defer>`) || !strings.Contains(page, `<dialog id="confirm"`) {
		t.Fatal("o layout precisa carregar o board.js e o diálogo de confirmação")
	}
	if js := do(t, h, "GET", "/static/board.js", nil, false).Body.String(); !strings.Contains(js, "htmx:confirm") || !strings.Contains(js, "showModal") {
		t.Fatal("a confirmação não pode depender do confirm() nativo")
	}
}
