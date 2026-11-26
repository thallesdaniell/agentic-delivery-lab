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
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

type fakeStore struct {
	cards   map[string]*store.CardDetail
	created []card.Card
	readyFn func(id string) error
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

func (f *fakeStore) MarkReady(_ context.Context, id string) (int64, error) {
	if f.readyFn != nil {
		if err := f.readyFn(id); err != nil {
			return 0, err
		}
	}
	f.cards[id].Status = store.CardReady
	return 1, nil
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
	for _, want := range []string{"Backlog", "Pronto", "Executando", "PR aberto", "Falhou", "ADL-1", "Mover para Pronto", "sem agente"} {
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

// Card sem agente não mostra o botão de Pronto.
func TestReadyButtonOnlyWithAgent(t *testing.T) {
	body := do(t, server(t, newFake()), "GET", "/partials/columns", nil, false).Body.String()
	if strings.Count(body, "Mover para Pronto") != 1 {
		t.Fatalf("só o ADL-1 tem agente:\n%s", body)
	}
}

func TestWritesRequireHTMXHeaderAndSameOrigin(t *testing.T) {
	h := server(t, newFake())
	if rec := do(t, h, "POST", "/cards/ADL-1/ready", url.Values{}, false); rec.Code != http.StatusForbidden {
		t.Fatalf("POST sem HX-Request: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/cards/ADL-1/ready", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST de outra origem: %d", rec.Code)
	}
}

func TestMarkReadyFromBoardReturnsColumns(t *testing.T) {
	st := newFake()
	rec := do(t, server(t, st), "POST", "/cards/ADL-1/ready", url.Values{"view": {"board"}}, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="columns"`) {
		t.Fatalf("status %d:\n%s", rec.Code, rec.Body.String())
	}
	if st.cards["ADL-1"].Status != store.CardReady {
		t.Fatal("o card deveria ir para Pronto")
	}
}

func TestMarkReadyMapsDomainErrors(t *testing.T) {
	cases := map[error]int{store.ErrNoAgent: 422, store.ErrActiveRun: 409, store.ErrInvalidTransition: 409}
	for e, code := range cases {
		st := newFake()
		st.readyFn = func(string) error { return e }
		rec := do(t, server(t, st), "POST", "/cards/ADL-1/ready", url.Values{}, true)
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
