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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

//go:embed migrations/*.sql
var migrations embed.FS

// CardStatus é a coluna em que o card está no board.
type CardStatus string

const (
	CardBacklog CardStatus = "backlog"
	CardReady   CardStatus = "ready"
	CardRunning CardStatus = "running"
	CardPROpen  CardStatus = "pr_open"
	CardFailed  CardStatus = "failed"
)

// ErrActiveRun indica que o card já tem um run na fila ou rodando.
var ErrActiveRun = errors.New("o card já tem um run ativo")

// ErrNotFound indica que o card não existe.
var ErrNotFound = errors.New("card não encontrado")

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

// CreateCard grava um card novo no backlog.
func (s *Store) CreateCard(ctx context.Context, c card.Card) error {
	if err := c.Validate(); err != nil {
		return err
	}
	ac, _ := json.Marshal(c.AcceptanceCriteria)
	_, err := s.db.Exec(ctx, `INSERT INTO card (id, title, objective, acceptance_criteria, repo, skill) VALUES ($1, $2, $3, $4, $5, $6)`,
		c.ID, c.Title, c.Objective, ac, c.Repo, c.Skill)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("já existe um card com id %s", c.ID)
	}
	return err
}

// MarkReady move o card para Pronto e enfileira um run. Recusa se já houver run ativo.
func (s *Store) MarkReady(ctx context.Context, cardID string) (int64, error) {
	var runID int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE card SET status = 'ready', updated_at = now() WHERE id = $1`, cardID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		err = tx.QueryRow(ctx, `INSERT INTO run (card_id) VALUES ($1) RETURNING id`, cardID).Scan(&runID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrActiveRun
		}
		return err
	})
	return runID, err
}

// Claimed é um run tirado da fila, com o card dele.
type Claimed struct {
	RunID int64
	Card  card.Card
}

// ClaimRun tira o run mais antigo da fila. Com SKIP LOCKED, dois orquestradores nunca pegam o mesmo.
func (s *Store) ClaimRun(ctx context.Context) (*Claimed, error) {
	var cl Claimed
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var ac []byte
		err := tx.QueryRow(ctx, `
			SELECT r.id, c.id, c.title, c.objective, c.acceptance_criteria, c.repo, c.skill
			FROM run r JOIN card c ON c.id = r.card_id
			WHERE r.status = 'queued'
			ORDER BY r.id
			LIMIT 1
			FOR UPDATE OF r SKIP LOCKED`).Scan(&cl.RunID, &cl.Card.ID, &cl.Card.Title, &cl.Card.Objective, &ac, &cl.Card.Repo, &cl.Card.Skill)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(ac, &cl.Card.AcceptanceCriteria); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE run SET status = 'running', started_at = now() WHERE id = $1`, cl.RunID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE card SET status = 'running', updated_at = now() WHERE id = $1`, cl.Card.ID)
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
		var cardID, repo string
		err := tx.QueryRow(ctx, `
			UPDATE run SET status = $2, failure_code = $3, detail = $4, model = $5, cost_usd = $6, num_turns = $7,
				duration_sec = $8, handoff = $9, changed_files = $10, permission_denials = $11, log = $12, finished_at = now()
			FROM card
			WHERE run.id = $1 AND run.status = 'running' AND card.id = run.card_id
			RETURNING card.id, card.repo`,
			runID, string(res.Status), code, nullable(res.Detail), nullable(res.Model), res.CostUSD, res.NumTurns,
			res.DurationSec, nullJSON(handoff), nullJSON(files), nullJSON(denials), nullable(log)).Scan(&cardID, &repo)
		if err != nil {
			return fmt.Errorf("finalizando run %d: %w", runID, err)
		}
		status := CardFailed
		if res.Status == run.StatusSucceeded {
			status = CardPROpen
			if res.PRNumber == 0 || res.PRURL == "" {
				return fmt.Errorf("run %d terminou com sucesso sem PR", runID)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO delivery (run_id, card_id, repo, branch, pr_number, pr_url) VALUES ($1, $2, $3, $4, $5, $6)`,
				runID, cardID, repo, res.Branch, res.PRNumber, res.PRURL); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE card SET status = $2, updated_at = now() WHERE id = $1`, cardID, string(status))
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
			WHERE status = 'running' RETURNING card_id`)
		if err != nil {
			return err
		}
		var cards []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			cards = append(cards, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		n = int64(len(cards))
		_, err = tx.Exec(ctx, `UPDATE card SET status = 'failed', updated_at = now() WHERE id = ANY($1)`, cards)
		return err
	})
	return n, err
}

// CardView é o card com o último run, para listar.
type CardView struct {
	card.Card
	Status      CardStatus `json:"status"`
	RunID       *int64     `json:"run_id,omitempty"`
	RunStatus   *string    `json:"run_status,omitempty"`
	FailureCode *string    `json:"failure_code,omitempty"`
	CostUSD     *float64   `json:"cost_usd,omitempty"`
	PRURL       *string    `json:"pr_url,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ListCards devolve os cards com o último run de cada um.
func (s *Store) ListCards(ctx context.Context) ([]CardView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.title, c.objective, c.acceptance_criteria, c.repo, c.skill, c.status, c.updated_at,
			r.id, r.status, r.failure_code, r.cost_usd::float8, d.pr_url
		FROM card c
		LEFT JOIN LATERAL (SELECT * FROM run WHERE card_id = c.id ORDER BY id DESC LIMIT 1) r ON true
		LEFT JOIN delivery d ON d.run_id = r.id
		ORDER BY c.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CardView
	for rows.Next() {
		var v CardView
		var ac []byte
		if err := rows.Scan(&v.ID, &v.Title, &v.Objective, &ac, &v.Repo, &v.Skill, &v.Status, &v.UpdatedAt,
			&v.RunID, &v.RunStatus, &v.FailureCode, &v.CostUSD, &v.PRURL); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ac, &v.AcceptanceCriteria); err != nil {
			return nil, err
		}
		out = append(out, v)
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
