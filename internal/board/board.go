// Package board é a interface web do lab: HTML renderizado no servidor, com htmx.
//
// O board só fala com o Postgres. Ele não tem o socket do Docker nem segredo nenhum:
// mover um card para Pronto grava um run na fila, e quem roda o run é o orquestrador.
package board

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Store é o que o board precisa do banco.
type Store interface {
	ListCards(ctx context.Context) ([]store.CardView, error)
	GetCard(ctx context.Context, id string) (*store.CardDetail, error)
	GetRun(ctx context.Context, id int64) (*store.RunDetail, error)
	CreateCard(ctx context.Context, c card.Card) error
	AssignAgent(ctx context.Context, cardID, agent string) error
	MarkReady(ctx context.Context, cardID string) (int64, error)
	ListAgents(ctx context.Context) ([]store.AgentView, error)
	ListSkills(ctx context.Context) ([]store.SkillView, error)
}

// Column é uma coluna do board.
type Column struct {
	Status store.CardStatus
	Title  string
	Cards  []store.CardView
}

// Columns é a ordem das colunas: o caminho feliz da esquerda para a direita, e Falhou no fim.
var Columns = []struct {
	Status store.CardStatus
	Title  string
}{
	{store.CardBacklog, "Backlog"},
	{store.CardReady, "Pronto"},
	{store.CardRunning, "Executando"},
	{store.CardPROpen, "PR aberto"},
	{store.CardFailed, "Falhou"},
}

// Server é o board.
type Server struct {
	store       Store
	defaultRepo string
	log         *slog.Logger
	pages       map[string]*template.Template
}

// New monta o board e compila os templates.
func New(st Store, defaultRepo string, log *slog.Logger) (*Server, error) {
	s := &Server{store: st, defaultRepo: defaultRepo, log: log, pages: map[string]*template.Template{}}
	base, err := template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/partials.html")
	if err != nil {
		return nil, err
	}
	for _, page := range []string{"board", "card", "new", "run", "catalog"} {
		t, err := template.Must(base.Clone()).ParseFS(templateFS, "templates/"+page+".html")
		if err != nil {
			return nil, err
		}
		s.pages[page] = t
	}
	return s, nil
}

// Handler devolve as rotas do board, com os headers de segurança e a proteção contra CSRF.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", s.boardPage)
	mux.HandleFunc("GET /partials/columns", s.columns)
	mux.HandleFunc("GET /cards/new", s.newCardPage)
	mux.HandleFunc("POST /cards", s.createCard)
	mux.HandleFunc("GET /cards/{id}", s.cardPage)
	mux.HandleFunc("GET /cards/{id}/panel", s.cardPanel)
	mux.HandleFunc("POST /cards/{id}/agent", s.assignAgent)
	mux.HandleFunc("POST /cards/{id}/ready", s.markReady)
	mux.HandleFunc("GET /runs/{id}", s.runPage)
	mux.HandleFunc("GET /runs/{id}/panel", s.runPanel)
	mux.HandleFunc("GET /catalog", s.catalogPage)
	return secure(mux)
}

// secure aplica os headers de segurança e só aceita escrita vinda do próprio board.
// Mover um card para Pronto gasta dinheiro: um site de fora não pode fazer isso por um formulário.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; "+
			"connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if reason := forbiddenWrite(r); reason != "" {
				http.Error(w, "escrita recusada: "+reason, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func forbiddenWrite(r *http.Request) string {
	// Header customizado: formulário de outro site não consegue mandar sem passar pelo CORS, que o board não libera.
	if r.Header.Get("HX-Request") != "true" {
		return "falta o header HX-Request"
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return "requisição de outro site"
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return "origem diferente do board"
		}
	}
	return ""
}

func (s *Server) render(w http.ResponseWriter, page, name string, status int, data any) {
	var b strings.Builder
	if err := s.pages[page].ExecuteTemplate(&b, name, data); err != nil {
		s.log.Error("renderizando template", "page", page, "template", name, "err", err)
		http.Error(w, "erro ao renderizar a página", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "não encontrado", http.StatusNotFound)
		return
	}
	s.log.Error("erro no board", "err", err)
	http.Error(w, "erro interno", http.StatusInternalServerError)
}

// boardData é o que a página e o parcial das colunas precisam.
type boardData struct {
	Columns []Column
	Error   string
}

func (s *Server) loadColumns(ctx context.Context) ([]Column, error) {
	cards, err := s.store.ListCards(ctx)
	if err != nil {
		return nil, err
	}
	cols := make([]Column, len(Columns))
	for i, c := range Columns {
		cols[i] = Column{Status: c.Status, Title: c.Title}
		for _, v := range cards {
			if v.Status == c.Status {
				cols[i].Cards = append(cols[i].Cards, v)
			}
		}
	}
	return cols, nil
}

func (s *Server) boardPage(w http.ResponseWriter, r *http.Request) {
	cols, err := s.loadColumns(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "board", "layout", http.StatusOK, boardData{Columns: cols})
}

func (s *Server) columns(w http.ResponseWriter, r *http.Request) {
	s.renderColumns(w, r, http.StatusOK, "")
}

func (s *Server) renderColumns(w http.ResponseWriter, r *http.Request, status int, msg string) {
	cols, err := s.loadColumns(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "board", "columns", status, boardData{Columns: cols, Error: msg})
}

// newCardData é o formulário de card novo.
type newCardData struct {
	Form   cardForm
	Agents []store.AgentView
	Error  string
}

type cardForm struct {
	ID, Title, Objective, Criteria, Repo, Agent string
}

func (f cardForm) card() card.Card {
	var ac []string
	for _, line := range strings.Split(f.Criteria, "\n") {
		if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); line != "" {
			ac = append(ac, line)
		}
	}
	return card.Card{ID: strings.TrimSpace(f.ID), Title: strings.TrimSpace(f.Title), Objective: strings.TrimSpace(f.Objective),
		AcceptanceCriteria: ac, Repo: strings.TrimSpace(f.Repo), Agent: f.Agent}
}

func (s *Server) activeAgents(ctx context.Context) ([]store.AgentView, error) {
	all, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.AgentView
	for _, a := range all {
		if a.Active {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Server) newCardPage(w http.ResponseWriter, r *http.Request) {
	agents, err := s.activeAgents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "new", "layout", http.StatusOK, newCardData{Form: cardForm{Repo: s.defaultRepo}, Agents: agents})
}

func (s *Server) createCard(w http.ResponseWriter, r *http.Request) {
	f := cardForm{ID: r.FormValue("id"), Title: r.FormValue("title"), Objective: r.FormValue("objective"),
		Criteria: r.FormValue("criteria"), Repo: r.FormValue("repo"), Agent: r.FormValue("agent")}
	c := f.card()
	err := c.Validate()
	if err == nil {
		err = s.store.CreateCard(r.Context(), c)
	}
	if err != nil {
		agents, aerr := s.activeAgents(r.Context())
		if aerr != nil {
			s.fail(w, aerr)
			return
		}
		s.render(w, "new", "card-form", http.StatusUnprocessableEntity, newCardData{Form: f, Agents: agents, Error: err.Error()})
		return
	}
	w.Header().Set("HX-Redirect", "/cards/"+url.PathEscape(c.ID))
	w.WriteHeader(http.StatusOK)
}

// cardData é a página e o painel de um card.
type cardData struct {
	Card   *store.CardDetail
	Agents []store.AgentView
	Error  string
}

// Movable diz se o card pode trocar de agente ou ir para Pronto.
func (d cardData) Movable() bool {
	return d.Card.Status == store.CardBacklog || d.Card.Status == store.CardFailed
}

// Active diz se o card tem run na fila ou rodando (o painel se atualiza sozinho).
func (d cardData) Active() bool {
	return d.Card.Status == store.CardReady || d.Card.Status == store.CardRunning
}

func (s *Server) loadCard(ctx context.Context, id string) (cardData, error) {
	c, err := s.store.GetCard(ctx, id)
	if err != nil {
		return cardData{}, err
	}
	agents, err := s.activeAgents(ctx)
	return cardData{Card: c, Agents: agents}, err
}

func (s *Server) cardPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadCard(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "card", "layout", http.StatusOK, d)
}

func (s *Server) cardPanel(w http.ResponseWriter, r *http.Request) {
	s.renderCardPanel(w, r, http.StatusOK, "")
}

func (s *Server) renderCardPanel(w http.ResponseWriter, r *http.Request, status int, msg string) {
	d, err := s.loadCard(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Error = msg
	s.render(w, "card", "card-panel", status, d)
}

func (s *Server) assignAgent(w http.ResponseWriter, r *http.Request) {
	err := s.store.AssignAgent(r.Context(), r.PathValue("id"), r.FormValue("agent"))
	status, msg, ok := actionResult(err)
	if !ok {
		s.fail(w, err)
		return
	}
	s.renderCardPanel(w, r, status, msg)
}

// markReady move o card para Pronto: grava o run na fila. Responde com o painel do card
// ou com as colunas do board, conforme de onde veio o clique.
func (s *Server) markReady(w http.ResponseWriter, r *http.Request) {
	_, err := s.store.MarkReady(r.Context(), r.PathValue("id"))
	status, msg, ok := actionResult(err)
	if !ok {
		s.fail(w, err)
		return
	}
	if r.FormValue("view") == "board" {
		s.renderColumns(w, r, status, msg)
		return
	}
	s.renderCardPanel(w, r, status, msg)
}

// actionResult traduz os erros de domínio em status e mensagem. ok=false é erro inesperado.
func actionResult(err error) (status int, msg string, ok bool) {
	switch {
	case err == nil:
		return http.StatusOK, "", true
	case errors.Is(err, store.ErrNoAgent):
		return http.StatusUnprocessableEntity, "Atribua um agente antes de mover o card para Pronto.", true
	case errors.Is(err, store.ErrUnknownAgent):
		return http.StatusUnprocessableEntity, "Esse agente não existe ou saiu do catálogo.", true
	case errors.Is(err, store.ErrActiveRun):
		return http.StatusConflict, "O card já tem um run na fila ou rodando.", true
	case errors.Is(err, store.ErrInvalidTransition):
		return http.StatusConflict, "O card já tem PR aberto; o retorno ao agente chega no M4.", true
	default:
		return 0, "", false
	}
}

// runData é a página e o painel de um run.
type runData struct{ Run *store.RunDetail }

// Active diz se o run ainda não terminou (o painel se atualiza sozinho).
func (d runData) Active() bool { return d.Run.Status == "queued" || d.Run.Status == "running" }

func (s *Server) loadRun(r *http.Request) (runData, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return runData{}, store.ErrNotFound
	}
	run, err := s.store.GetRun(r.Context(), id)
	return runData{Run: run}, err
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadRun(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "run", "layout", http.StatusOK, d)
}

func (s *Server) runPanel(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadRun(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "run", "run-panel", http.StatusOK, d)
}

type catalogData struct {
	Agents []store.AgentView
	Skills []store.SkillView
}

func (s *Server) catalogPage(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	skills, err := s.store.ListSkills(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "catalog", "layout", http.StatusOK, catalogData{Agents: agents, Skills: skills})
}

var statusLabels = map[string]string{
	"backlog": "Backlog", "ready": "Pronto", "running": "Executando", "pr_open": "PR aberto", "failed": "Falhou",
	"queued": "Na fila", "succeeded": "Sucesso",
}

var funcs = template.FuncMap{
	"label": func(s any) string {
		k := fmt.Sprint(s)
		if l, ok := statusLabels[k]; ok {
			return l
		}
		return k
	},
	"str": func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	},
	"money": func(p *float64) string {
		if p == nil {
			return "–"
		}
		return fmt.Sprintf("US$ %.2f", *p)
	},
	"usd":  func(v float64) string { return fmt.Sprintf("US$ %.2f", v) },
	"int":  func(p *int) string { return deref(p) },
	"secs": func(p *float64) string { return secs(p) },
	"when": func(t any) string {
		switch v := t.(type) {
		case time.Time:
			return v.Local().Format("02/01 15:04:05")
		case *time.Time:
			if v == nil {
				return "–"
			}
			return v.Local().Format("02/01 15:04:05")
		}
		return ""
	},
	"prnum": func(u *string) string {
		if u == nil {
			return ""
		}
		return "#" + path.Base(*u)
	},
}

func deref(p *int) string {
	if p == nil {
		return "–"
	}
	return strconv.Itoa(*p)
}

func secs(p *float64) string {
	if p == nil {
		return "–"
	}
	return fmt.Sprintf("%.0f s", *p)
}
