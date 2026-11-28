// Package store guarda cards, runs e entregas no Postgres. Todo SQL do lab mora aqui.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

//go:embed migrations/*.sql
var migrations embed.FS

// CardStatus é a coluna em que o card está no board.
type CardStatus string

const (
	CardBacklog  CardStatus = "backlog"
	CardReady    CardStatus = "ready"
	CardRunning  CardStatus = "running"
	CardInReview CardStatus = "in_review" // PR aberto: CI, revisão e merge
	CardDone     CardStatus = "done"      // PR mergeado por uma pessoa
	CardFailed   CardStatus = "failed"
)

// ReviewPhase é a fase do PR enquanto o card está em revisão.
type ReviewPhase string

const (
	PhaseCIPending        ReviewPhase = "ci_pending"
	PhaseCIFailed         ReviewPhase = "ci_failed"
	PhaseAwaitingReview   ReviewPhase = "awaiting_review"
	PhaseChangesRequested ReviewPhase = "changes_requested"
	PhaseApproved         ReviewPhase = "approved"
)

// ErrActiveRun indica que o card já tem um run na fila ou rodando.
var ErrActiveRun = errors.New("o card já tem um run ativo")

// ErrNotFound indica que o card não existe.
var ErrNotFound = errors.New("card não encontrado")

// ErrNoAgent indica que o card precisa de um agente antes de ir para Pronto.
var ErrNoAgent = errors.New("o card não tem agente atribuído")

// ErrUnknownAgent indica um agente que não existe ou saiu do catálogo.
var ErrUnknownAgent = errors.New("agente não existe ou está inativo no catálogo")

// ErrInvalidTransition indica um movimento que o estado atual do card não permite.
var ErrInvalidTransition = errors.New("o card não pode fazer esse movimento no estado atual")

// Store é o acesso ao Postgres.
type Store struct{ db *pgxpool.Pool }

// Open conecta ao Postgres.
func Open(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("conectando ao Postgres: %w", err)
	}
	return &Store{db: db}, nil
}

// Close encerra o pool.
func (s *Store) Close() { s.db.Close() }

// Migrate aplica, em ordem e uma vez só, as migrations embutidas.
func (s *Store) Migrate(ctx context.Context) ([]string, error) {
	if _, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, name := range names {
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			// Trava para duas instâncias não aplicarem a mesma migration ao mesmo tempo.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242)`); err != nil {
				return err
			}
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migration WHERE name = $1)`, name).Scan(&done); err != nil || done {
				return err
			}
			sql, err := migrations.ReadFile(name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migration (name) VALUES ($1)`, name); err != nil {
				return err
			}
			applied = append(applied, name)
			return nil
		})
		if err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// CreateCard grava um card novo no backlog. O agente é opcional, mas, se vier, precisa estar ativo.
func (s *Store) CreateCard(ctx context.Context, c card.Card) error {
	if err := c.Validate(); err != nil {
		return err
	}
	ac, _ := json.Marshal(c.AcceptanceCriteria)
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if c.Agent != "" {
			if err := activeAgent(ctx, tx, c.Agent); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO card (id, title, objective, acceptance_criteria, repo, agent) VALUES ($1, $2, $3, $4, $5, $6)`,
			c.ID, c.Title, c.Objective, ac, c.Repo, nullable(c.Agent))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("já existe um card com id %s", c.ID)
		}
		return err
	})
}

func activeAgent(ctx context.Context, tx pgx.Tx, name string) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent WHERE name = $1 AND active)`, name).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, name)
	}
	return nil
}

// movable é o card travado para um movimento feito por uma pessoa.
type movable struct {
	agent  *string
	status CardStatus
}

// lockMovable trava o card e confere que o estado atual permite o movimento.
func lockMovable(ctx context.Context, tx pgx.Tx, cardID string, from ...CardStatus) (movable, error) {
	var m movable
	err := tx.QueryRow(ctx, `SELECT status, agent FROM card WHERE id = $1 FOR UPDATE`, cardID).Scan(&m.status, &m.agent)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	for _, f := range from {
		if m.status == f {
			return m, nil
		}
	}
	if m.status == CardRunning {
		return m, ErrActiveRun
	}
	return m, ErrInvalidTransition
}

// AssignAgent atribui um agente do catálogo ao card, antes de ele entrar em Executando.
func (s *Store) AssignAgent(ctx context.Context, cardID, agentName string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := lockMovable(ctx, tx, cardID, CardBacklog, CardReady, CardFailed); err != nil {
			return err
		}
		if err := activeAgent(ctx, tx, agentName); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE card SET agent = $2, updated_at = now() WHERE id = $1`, cardID, agentName)
		return err
	})
}

// Move faz o movimento que uma pessoa pediu no board (arrastar ou botão) ou na linha de comando.
// As regras são as de AllowedMoves; entrar em Executando enfileira o run e devolve o id dele.
func (s *Store) Move(ctx context.Context, cardID string, to CardStatus) (int64, error) {
	switch to {
	case CardBacklog:
		return 0, s.setColumn(ctx, cardID, CardBacklog, false, CardReady)
	case CardReady:
		return 0, s.setColumn(ctx, cardID, CardReady, true, CardBacklog, CardFailed)
	case CardRunning:
		return s.Start(ctx, cardID)
	}
	return 0, ErrInvalidTransition
}

// MarkReady move o card para Pronto: refinado, com agente, esperando alguém puxar. Não gasta nada.
func (s *Store) MarkReady(ctx context.Context, cardID string) error {
	_, err := s.Move(ctx, cardID, CardReady)
	return err
}

func (s *Store) setColumn(ctx context.Context, cardID string, to CardStatus, needsAgent bool, from ...CardStatus) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		m, err := lockMovable(ctx, tx, cardID, from...)
		if err != nil {
			return err
		}
		if needsAgent {
			if m.agent == nil {
				return ErrNoAgent
			}
			if err := activeAgent(ctx, tx, *m.agent); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE card SET status = $2, failure_code = NULL, failure_detail = NULL, updated_at = now() WHERE id = $1`,
			cardID, string(to))
		return err
	})
}

// Start põe o card em Executando e enfileira o run que implementa o card, com o agente congelado.
// Sai do backlog, de Pronto ou de Falhou, e só com agente ativo. Com o PR fechado, o card recomeça do zero.
func (s *Store) Start(ctx context.Context, cardID string) (int64, error) {
	var runID int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		m, err := lockMovable(ctx, tx, cardID, CardBacklog, CardReady, CardFailed)
		if err != nil {
			return err
		}
		if m.agent == nil {
			return ErrNoAgent
		}
		spec, err := loadSpec(ctx, tx, *m.agent)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE card SET status = 'running', review_phase = NULL, failure_code = NULL, failure_detail = NULL,
			ci_attempts = 0, review_rounds = 0, last_review_id = NULL, pending_review_id = NULL, rework_kind = NULL, rework_feedback = NULL,
			updated_at = now() WHERE id = $1`, cardID); err != nil {
			return err
		}
		runID, err = insertRun(ctx, tx, cardID, spec, run.Task{Kind: run.KindImplement})
		return err
	})
	return runID, err
}

// insertRun grava o run na fila. O índice único garante no máximo um run ativo por card.
func insertRun(ctx context.Context, tx pgx.Tx, cardID string, spec catalog.Spec, t run.Task) (int64, error) {
	raw, _ := json.Marshal(spec)
	var att *int
	if t.Kind.Rework() {
		att = &t.Attempt
	}
	var runID int64
	err := tx.QueryRow(ctx, `INSERT INTO run (card_id, agent, agent_version, skill_version, spec, kind, feedback, attempt, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		cardID, spec.Agent, spec.AgentVersion, spec.SkillVersion, raw, string(t.Kind), nullable(t.Feedback), att, nullable(t.Note)).Scan(&runID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrActiveRun
	}
	return runID, err
}

func loadSpec(ctx context.Context, tx pgx.Tx, agentName string) (catalog.Spec, error) {
	var sp catalog.Spec
	var tools []byte
	err := tx.QueryRow(ctx, `
		SELECT a.name, a.version, a.model, a.budget_usd::float8, a.tools, s.name, s.version, s.content
		FROM agent a JOIN skill s ON s.name = a.skill
		WHERE a.name = $1 AND a.active`, agentName).Scan(&sp.Agent, &sp.AgentVersion, &sp.Model, &sp.BudgetUSD, &tools,
		&sp.Skill, &sp.SkillVersion, &sp.SkillContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return sp, fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	if err != nil {
		return sp, err
	}
	return sp, json.Unmarshal(tools, &sp.Tools)
}

// SyncCatalog copia o catálogo do repositório para o banco. Agente que sumiu do repositório fica inativo.
func (s *Store) SyncCatalog(ctx context.Context, cat catalog.Catalog) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4243)`); err != nil {
			return err
		}
		for _, sk := range cat.Skills {
			if _, err := tx.Exec(ctx, `
				INSERT INTO skill (name, description, content, version) VALUES ($1, $2, $3, $4)
				ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, content = EXCLUDED.content,
					version = EXCLUDED.version,
					updated_at = CASE WHEN skill.version = EXCLUDED.version THEN skill.updated_at ELSE now() END`,
				sk.Name, sk.Description, sk.Content, sk.Version); err != nil {
				return fmt.Errorf("skill %s: %w", sk.Name, err)
			}
		}
		names := make([]string, 0, len(cat.Agents))
		for _, a := range cat.Agents {
			tools, _ := json.Marshal(a.Tools)
			if _, err := tx.Exec(ctx, `
				INSERT INTO agent (name, description, skill, model, budget_usd, tools, version) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, skill = EXCLUDED.skill,
					model = EXCLUDED.model, budget_usd = EXCLUDED.budget_usd, tools = EXCLUDED.tools,
					version = EXCLUDED.version, active = true,
					updated_at = CASE WHEN agent.version = EXCLUDED.version AND agent.active THEN agent.updated_at ELSE now() END`,
				a.Name, a.Description, a.Skill, a.Model, a.BudgetUSD, tools, a.Version); err != nil {
				return fmt.Errorf("agente %s: %w", a.Name, err)
			}
			names = append(names, a.Name)
		}
		_, err := tx.Exec(ctx, `UPDATE agent SET active = false, updated_at = now() WHERE active AND NOT (name = ANY($1))`, names)
		return err
	})
}

// Claimed é um run tirado da fila, com o card dele.
type Claimed struct {
	RunID int64
	Card  card.Card
	Spec  catalog.Spec
	Task  run.Task
}

// ClaimRun tira o run mais antigo da fila. Com SKIP LOCKED, dois orquestradores nunca pegam o mesmo.
func (s *Store) ClaimRun(ctx context.Context) (*Claimed, error) {
	var cl Claimed
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var ac, spec []byte
		var kind string
		var feedback, note, prURL *string
		var attempt, prNumber *int
		err := tx.QueryRow(ctx, `
			SELECT r.id, c.id, c.title, c.objective, c.acceptance_criteria, c.repo, r.agent, r.spec,
				r.kind, r.feedback, r.attempt, r.note, d.pr_number, d.pr_url
			FROM run r JOIN card c ON c.id = r.card_id
			LEFT JOIN LATERAL (SELECT pr_number, pr_url FROM delivery WHERE card_id = c.id ORDER BY id DESC LIMIT 1) d ON true
			WHERE r.status = 'queued'
			ORDER BY r.id
			LIMIT 1
			FOR UPDATE OF r SKIP LOCKED`).Scan(&cl.RunID, &cl.Card.ID, &cl.Card.Title, &cl.Card.Objective, &ac, &cl.Card.Repo, &cl.Card.Agent, &spec,
			&kind, &feedback, &attempt, &note, &prNumber, &prURL)
		if err != nil {
			return err
		}
		cl.Task = run.Task{Kind: run.Kind(kind)}
		if cl.Task.Kind.Rework() {
			if prNumber == nil || feedback == nil {
				return fmt.Errorf("run %d de correção sem PR ou sem entrada", cl.RunID)
			}
			cl.Task.Feedback, cl.Task.PRNumber, cl.Task.PRURL = *feedback, *prNumber, *prURL
			if attempt != nil {
				cl.Task.Attempt = *attempt
			}
			if note != nil {
				cl.Task.Note = *note
			}
		}
		if err := json.Unmarshal(ac, &cl.Card.AcceptanceCriteria); err != nil {
			return err
		}
		if err := json.Unmarshal(spec, &cl.Spec); err != nil {
			return err
		}
		// O card já está onde deve: em Executando (implementação) ou em Em revisão (correção).
		_, err = tx.Exec(ctx, `UPDATE run SET status = 'running', started_at = now() WHERE id = $1`, cl.RunID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cl, nil
}

// SetContainer registra o container do worker do run.
func (s *Store) SetContainer(ctx context.Context, runID int64, containerID string) error {
	_, err := s.db.Exec(ctx, `UPDATE run SET container_id = $2 WHERE id = $1`, runID, containerID)
	return err
}

// UpdateRunLog grava o fim do log do worker enquanto o run roda, para o board mostrar o andamento.
func (s *Store) UpdateRunLog(ctx context.Context, runID int64, log string) error {
	_, err := s.db.Exec(ctx, `UPDATE run SET log = $2 WHERE id = $1 AND status = 'running'`, runID, log)
	return err
}

// FinishRun grava o resultado do run, move o card e registra a entrega, numa transação.
func (s *Store) FinishRun(ctx context.Context, runID int64, res run.Result, log string) error {
	if err := res.Validate(); err != nil {
		return err
	}
	handoff, _ := json.Marshal(res.Handoff)
	files, _ := json.Marshal(res.ChangedFiles)
	denials, _ := json.Marshal(res.Denials)
	var code *string
	if res.FailureCode != "" {
		c := string(res.FailureCode)
		code = &c
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var cardID, repo, kind string
		err := tx.QueryRow(ctx, `
			UPDATE run SET status = $2, failure_code = $3, detail = $4, model = $5, cost_usd = $6, num_turns = $7,
				duration_sec = $8, handoff = $9, changed_files = $10, permission_denials = $11, log = $12, finished_at = now()
			FROM card
			WHERE run.id = $1 AND run.status = 'running' AND card.id = run.card_id
			RETURNING card.id, card.repo, run.kind`,
			runID, string(res.Status), code, nullable(res.Detail), nullable(res.Model), res.CostUSD, res.NumTurns,
			res.DurationSec, nullJSON(handoff), nullJSON(files), nullJSON(denials), nullable(log)).Scan(&cardID, &repo, &kind)
		if err != nil {
			return fmt.Errorf("finalizando run %d: %w", runID, err)
		}
		status, phase := CardFailed, (*string)(nil)
		if res.Status == run.StatusSucceeded {
			status = CardInReview
			p := string(PhaseCIPending)
			phase = &p
			if res.PRNumber == 0 || res.PRURL == "" {
				return fmt.Errorf("run %d terminou com sucesso sem PR", runID)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO delivery (run_id, card_id, repo, branch, pr_number, pr_url) VALUES ($1, $2, $3, $4, $5, $6)`,
				runID, cardID, repo, res.Branch, res.PRNumber, res.PRURL); err != nil {
				return err
			}
		}
		if run.Kind(kind).Rework() {
			// A correção não move o card: ele segue em revisão. Com push novo, o CI recomeça.
			if res.Status == run.StatusSucceeded {
				_, err = tx.Exec(ctx, `UPDATE card SET review_phase = 'ci_pending', rework_kind = NULL, rework_feedback = NULL,
					pending_review_id = NULL, updated_at = now() WHERE id = $1`, cardID)
			}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE card SET status = $2, review_phase = $3, updated_at = now() WHERE id = $1`, cardID, string(status), phase)
		return err
	})
}

// RecoverInterrupted encerra runs que estavam rodando quando o orquestrador caiu.
func (s *Store) RecoverInterrupted(ctx context.Context) (int64, error) {
	var n int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE run SET status = 'failed', failure_code = 'interrupted', finished_at = now(),
				detail = 'o orquestrador reiniciou com este run em andamento'
			WHERE status = 'running' RETURNING CASE WHEN kind = 'implement' THEN card_id END`)
		if err != nil {
			return err
		}
		// Correção interrompida deixa o card em revisão; só a implementação interrompida leva o card para Falhou.
		var cards []string
		for rows.Next() {
			var id *string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			n++
			if id != nil {
				cards = append(cards, *id)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE card SET status = 'failed', updated_at = now() WHERE id = ANY($1)`, cards)
		return err
	})
	return n, err
}

// CardView é o card com o último run, para listar.
type CardView struct {
	card.Card
	Status CardStatus `json:"status"`
	// Phase é a fase do PR quando o card está em revisão.
	Phase        *ReviewPhase `json:"review_phase,omitempty"`
	CIAttempts   int          `json:"ci_attempts"`
	ReviewRounds int          `json:"review_rounds"`
	RunID        *int64       `json:"run_id,omitempty"`
	RunStatus    *string      `json:"run_status,omitempty"`
	RunKind      *run.Kind    `json:"run_kind,omitempty"`
	RunAttempt   *int         `json:"run_attempt,omitempty"`
	// FailureCode é o do card (ciclo de revisão) ou, se ele não tiver, o do último run.
	FailureCode   *string  `json:"failure_code,omitempty"`
	FailureDetail *string  `json:"failure_detail,omitempty"`
	CostUSD       *float64 `json:"cost_usd,omitempty"`
	// HasPending diz se há correção pronta para o botão; AgentBudgetUSD é o teto do agente atribuído.
	HasPending     bool      `json:"has_pending"`
	AgentBudgetUSD *float64  `json:"agent_budget_usd,omitempty"`
	PRNumber       *int      `json:"pr_number,omitempty"`
	PRURL          *string   `json:"pr_url,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const cardViewSelect = `
	SELECT c.id, c.title, c.objective, c.acceptance_criteria, c.repo, COALESCE(c.agent, ''), c.status, c.updated_at,
		c.review_phase, c.ci_attempts, c.review_rounds,
		r.id, r.status, r.kind, r.attempt, COALESCE(c.failure_code, r.failure_code), COALESCE(c.failure_detail, r.detail),
		r.cost_usd::float8, c.rework_kind IS NOT NULL, a.budget_usd::float8, d.pr_number, d.pr_url
	FROM card c
	LEFT JOIN agent a ON a.name = c.agent
	LEFT JOIN LATERAL (SELECT * FROM run WHERE card_id = c.id ORDER BY id DESC LIMIT 1) r ON true
	LEFT JOIN LATERAL (SELECT pr_number, pr_url FROM delivery WHERE card_id = c.id ORDER BY id DESC LIMIT 1) d ON true`

func scanCardView(row pgx.Row) (CardView, error) {
	var v CardView
	var ac []byte
	if err := row.Scan(&v.ID, &v.Title, &v.Objective, &ac, &v.Repo, &v.Agent, &v.Status, &v.UpdatedAt,
		&v.Phase, &v.CIAttempts, &v.ReviewRounds,
		&v.RunID, &v.RunStatus, &v.RunKind, &v.RunAttempt, &v.FailureCode, &v.FailureDetail,
		&v.CostUSD, &v.HasPending, &v.AgentBudgetUSD, &v.PRNumber, &v.PRURL); err != nil {
		return v, err
	}
	return v, json.Unmarshal(ac, &v.AcceptanceCriteria)
}

// ListCards devolve os cards com o último run de cada um.
func (s *Store) ListCards(ctx context.Context) ([]CardView, error) {
	rows, err := s.db.Query(ctx, cardViewSelect+` ORDER BY c.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CardView
	for rows.Next() {
		v, err := scanCardView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RunSummary é uma linha do histórico de runs de um card.
type RunSummary struct {
	ID           int64      `json:"id"`
	CardID       string     `json:"card_id"`
	Status       string     `json:"status"`
	Kind         run.Kind   `json:"kind"`
	Attempt      *int       `json:"attempt,omitempty"`
	FailureCode  *string    `json:"failure_code,omitempty"`
	Agent        *string    `json:"agent,omitempty"`
	AgentVersion *string    `json:"agent_version,omitempty"`
	SkillVersion *string    `json:"skill_version,omitempty"`
	Model        *string    `json:"model,omitempty"`
	CostUSD      *float64   `json:"cost_usd,omitempty"`
	NumTurns     *int       `json:"num_turns,omitempty"`
	DurationSec  *float64   `json:"duration_sec,omitempty"`
	PRNumber     *int       `json:"pr_number,omitempty"`
	PRURL        *string    `json:"pr_url,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

const runSummarySelect = `
	SELECT r.id, r.card_id, r.status, r.kind, r.attempt, r.failure_code, r.agent, r.agent_version, r.skill_version, r.model,
		r.cost_usd::float8, r.num_turns, r.duration_sec::float8, d.pr_number, d.pr_url, r.created_at, r.started_at, r.finished_at`

func (r *RunSummary) dest() []any {
	return []any{&r.ID, &r.CardID, &r.Status, &r.Kind, &r.Attempt, &r.FailureCode, &r.Agent, &r.AgentVersion, &r.SkillVersion, &r.Model,
		&r.CostUSD, &r.NumTurns, &r.DurationSec, &r.PRNumber, &r.PRURL, &r.CreatedAt, &r.StartedAt, &r.FinishedAt}
}

// CardDetail é o card com todos os runs, do mais novo para o mais antigo.
type CardDetail struct {
	CardView
	Runs []RunSummary `json:"runs"`
}

// GetCard devolve o card e o histórico de runs.
func (s *Store) GetCard(ctx context.Context, id string) (*CardDetail, error) {
	v, err := scanCardView(s.db.QueryRow(ctx, cardViewSelect+` WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, runSummarySelect+` FROM run r LEFT JOIN delivery d ON d.run_id = r.id WHERE r.card_id = $1 ORDER BY r.id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &CardDetail{CardView: v}
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, err
		}
		out.Runs = append(out.Runs, r)
	}
	return out, rows.Err()
}

// RunDetail é tudo o que o run registrou.
type RunDetail struct {
	RunSummary
	CardTitle    string           `json:"card_title"`
	Detail       *string          `json:"detail,omitempty"`
	Handoff      *handoff.Handoff `json:"handoff,omitempty"`
	ChangedFiles []string         `json:"changed_files,omitempty"`
	Denials      []string         `json:"permission_denials,omitempty"`
	Log          *string          `json:"log,omitempty"`
	ContainerID  *string          `json:"container_id,omitempty"`
	// Feedback é a entrada de um run de correção: o log do CI ou os comentários da revisão.
	Feedback *string `json:"feedback,omitempty"`
}

// GetRun devolve um run com handoff, arquivos, tools negadas e log.
func (s *Store) GetRun(ctx context.Context, id int64) (*RunDetail, error) {
	var r RunDetail
	var h, files, denials []byte
	dest := append(r.dest(), &r.CardTitle, &r.Detail, &h, &files, &denials, &r.Log, &r.ContainerID, &r.Feedback)
	err := s.db.QueryRow(ctx, runSummarySelect+`, c.title, r.detail, r.handoff, r.changed_files, r.permission_denials, r.log, r.container_id, r.feedback
		FROM run r JOIN card c ON c.id = r.card_id LEFT JOIN delivery d ON d.run_id = r.id WHERE r.id = $1`, id).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	for _, j := range []struct {
		raw []byte
		v   any
	}{{h, &r.Handoff}, {files, &r.ChangedFiles}, {denials, &r.Denials}} {
		if len(j.raw) > 0 {
			if err := json.Unmarshal(j.raw, j.v); err != nil {
				return nil, err
			}
		}
	}
	return &r, nil
}

// Reviewing é um card em revisão, com o PR que o observador acompanha.
type Reviewing struct {
	CardID          string
	Repo            string
	PRNumber        int
	PRURL           string
	Phase           *ReviewPhase
	CIAttempts      int
	ReviewRounds    int
	LastReviewID    *int64 // última revisão já mandada ao agente
	PendingReviewID *int64 // revisão cujo pedido de mudanças está pronto para o botão
	HasPending      bool   // há correção pronta para o botão
	RerunRequested  bool   // alguém pediu para rodar o CI de novo
}

// ReviewQueue devolve os cards em revisão sem run ativo, com o PR mais recente de cada um.
// Enquanto um run de correção roda, o observador deixa o card em paz.
func (s *Store) ReviewQueue(ctx context.Context) ([]Reviewing, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.repo, d.pr_number, d.pr_url, c.review_phase, c.ci_attempts, c.review_rounds, c.last_review_id,
			c.pending_review_id, c.rework_kind IS NOT NULL, c.ci_rerun_requested
		FROM card c
		JOIN LATERAL (SELECT pr_number, pr_url FROM delivery WHERE card_id = c.id ORDER BY id DESC LIMIT 1) d ON true
		WHERE c.status = 'in_review'
			AND NOT EXISTS (SELECT 1 FROM run WHERE card_id = c.id AND status IN ('queued', 'running'))
		ORDER BY c.updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reviewing
	for rows.Next() {
		var r Reviewing
		if err := rows.Scan(&r.CardID, &r.Repo, &r.PRNumber, &r.PRURL, &r.Phase, &r.CIAttempts, &r.ReviewRounds, &r.LastReviewID,
			&r.PendingReviewID, &r.HasPending, &r.RerunRequested); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// inReview roda uma alteração só se o card ainda está em revisão e sem run ativo. Se ele mudou no meio,
// devolve ErrInvalidTransition e nada muda.
func (s *Store) inReview(ctx context.Context, sql string, args ...any) error {
	tag, err := s.db.Exec(ctx, sql+` AND status = 'in_review'
		AND NOT EXISTS (SELECT 1 FROM run WHERE card_id = card.id AND status IN ('queued', 'running'))`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidTransition
	}
	return nil
}

// Pending é a correção que o observador deixa pronta para o botão "Corrigir com o agente".
type Pending struct {
	Kind     run.Kind
	Feedback string
	ReviewID *int64 // só no address_review
}

// SetReviewState grava a fase do PR e a correção pronta para o botão (nil limpa).
func (s *Store) SetReviewState(ctx context.Context, cardID string, phase ReviewPhase, p *Pending) error {
	var kind, feedback *string
	var reviewID *int64
	if p != nil {
		k := string(p.Kind)
		kind, feedback, reviewID = &k, &p.Feedback, p.ReviewID
	}
	return s.inReview(ctx, `UPDATE card SET review_phase = $2, rework_kind = $3, rework_feedback = $4, pending_review_id = $5,
		updated_at = CASE WHEN review_phase IS DISTINCT FROM $2 THEN now() ELSE updated_at END
		WHERE id = $1`, cardID, string(phase), kind, feedback, reviewID)
}

// CompleteCard conclui o card: uma pessoa fez o merge do PR.
func (s *Store) CompleteCard(ctx context.Context, cardID string) error {
	return s.inReview(ctx, `UPDATE card SET status = 'done', review_phase = NULL, rework_kind = NULL, rework_feedback = NULL,
		pending_review_id = NULL, done_at = now(), updated_at = now() WHERE id = $1`, cardID)
}

// FailCard encerra o ciclo de revisão com código de card. Hoje só o PR fechado sem merge faz isso.
func (s *Store) FailCard(ctx context.Context, cardID string, code run.FailureCode, detail string) error {
	if code != run.FailurePRClosed {
		return fmt.Errorf("código %q não é de card", code)
	}
	return s.inReview(ctx, `UPDATE card SET status = 'failed', review_phase = NULL, rework_kind = NULL, rework_feedback = NULL,
		pending_review_id = NULL, failure_code = $2, failure_detail = $3, updated_at = now() WHERE id = $1`, cardID, string(code), nullable(detail))
}

// RequestCIRerun registra o pedido de rodar de novo os jobs que falharam no CI do PR.
// O board não fala com o GitHub: o observador executa o pedido na próxima volta.
func (s *Store) RequestCIRerun(ctx context.Context, cardID string) error {
	err := s.inReview(ctx, `UPDATE card SET ci_rerun_requested = true, updated_at = now() WHERE id = $1 AND review_phase = 'ci_failed'`, cardID)
	if errors.Is(err, ErrInvalidTransition) {
		return ErrNothingToFix
	}
	return err
}

// CIRerunDone marca o pedido como executado e o CI como rodando.
func (s *Store) CIRerunDone(ctx context.Context, cardID string) error {
	return s.inReview(ctx, `UPDATE card SET ci_rerun_requested = false, review_phase = 'ci_pending', rework_kind = NULL,
		rework_feedback = NULL, updated_at = now() WHERE id = $1`, cardID)
}

// ErrNothingToFix indica que o PR não tem CI vermelho nem pedido de mudanças para mandar ao agente.
var ErrNothingToFix = errors.New("não há correção pendente para este card")

// ErrLimitReached indica que o card já usou todas as correções daquele tipo.
var ErrLimitReached = errors.New("o card atingiu o limite de correções")

// RequestRework manda ao agente a correção pendente do card (log do CI ou comentários da revisão),
// com a orientação opcional de quem pediu. O card continua em Em revisão; o run usa o agente do run anterior.
func (s *Store) RequestRework(ctx context.Context, cardID, note string) (int64, error) {
	var runID int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var status CardStatus
		var ci, rounds int
		var kind, feedback *string
		var reviewID *int64
		err := tx.QueryRow(ctx, `SELECT status, ci_attempts, review_rounds, rework_kind, rework_feedback, pending_review_id
			FROM card WHERE id = $1 FOR UPDATE`, cardID).Scan(&status, &ci, &rounds, &kind, &feedback, &reviewID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != CardInReview {
			return ErrInvalidTransition
		}
		if kind == nil || feedback == nil {
			return ErrNothingToFix
		}
		t := run.Task{Kind: run.Kind(*kind), Feedback: *feedback, Note: strings.TrimSpace(note)}
		q := `UPDATE card SET ci_attempts = $2, updated_at = now() WHERE id = $1`
		args := []any{cardID}
		switch t.Kind {
		case run.KindFixCI:
			if ci >= run.MaxCIAttempts {
				return ErrLimitReached
			}
			t.Attempt = ci + 1
			args = append(args, t.Attempt)
		case run.KindAddressReview:
			if rounds >= run.MaxReviewRounds {
				return ErrLimitReached
			}
			t.Attempt = rounds + 1
			q = `UPDATE card SET review_rounds = $2, last_review_id = COALESCE($3, last_review_id), updated_at = now() WHERE id = $1`
			args = append(args, t.Attempt, reviewID)
		default:
			return ErrNothingToFix
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT spec FROM run WHERE card_id = $1 AND spec IS NOT NULL ORDER BY id DESC LIMIT 1`, cardID).Scan(&raw); err != nil {
			return fmt.Errorf("agente do último run: %w", err)
		}
		var spec catalog.Spec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return err
		}
		if runID, err = insertRun(ctx, tx, cardID, spec, t); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, q, args...)
		return err
	})
	return runID, err
}

// AgentView é um agente do catálogo, como está no banco.
type AgentView struct {
	catalog.Agent
	SkillVersion string    `json:"skill_version"`
	Active       bool      `json:"active"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ListAgents devolve os agentes, ativos primeiro.
func (s *Store) ListAgents(ctx context.Context) ([]AgentView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.name, a.description, a.skill, a.model, a.budget_usd::float8, a.tools, a.version, s.version, a.active, a.updated_at
		FROM agent a JOIN skill s ON s.name = a.skill
		ORDER BY a.active DESC, a.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentView
	for rows.Next() {
		var a AgentView
		var tools []byte
		if err := rows.Scan(&a.Name, &a.Description, &a.Skill, &a.Model, &a.BudgetUSD, &tools, &a.Version, &a.SkillVersion,
			&a.Active, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tools, &a.Tools); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SkillView é uma skill do catálogo, como está no banco.
type SkillView struct {
	catalog.Skill
	UpdatedAt time.Time `json:"updated_at"`
}

// ListSkills devolve as skills com o conteúdo.
func (s *Store) ListSkills(ctx context.Context) ([]SkillView, error) {
	rows, err := s.db.Query(ctx, `SELECT name, description, content, version, updated_at FROM skill ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkillView
	for rows.Next() {
		var sk SkillView
		if err := rows.Scan(&sk.Name, &sk.Description, &sk.Content, &sk.Version, &sk.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullJSON(b []byte) []byte {
	if string(b) == "null" {
		return nil
	}
	return b
}
