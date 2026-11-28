package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

// GitHub é o que o observador lê do PR, mais o pedido de rodar o CI de novo.
type GitHub interface {
	GetPullRequest(ctx context.Context, token, repo string, number int) (githubapp.PRState, error)
	CheckRuns(ctx context.Context, token, repo, sha string) ([]githubapp.CheckRun, error)
	JobLog(ctx context.Context, token, repo string, jobID int64) (string, error)
	Reviews(ctx context.Context, token, repo string, number int) ([]githubapp.Review, error)
	ReviewComments(ctx context.Context, token, repo string, number int, reviewID int64) ([]githubapp.ReviewComment, error)
	RerunFailedJobs(ctx context.Context, token, repo string, jobIDs []int64) error
}

// Store é o que o observador muda no banco. Toda alteração só vale se o card ainda está em revisão.
type Store interface {
	ReviewQueue(ctx context.Context) ([]store.Reviewing, error)
	SetReviewState(ctx context.Context, cardID string, phase store.ReviewPhase, p *store.Pending) error
	CompleteCard(ctx context.Context, cardID string) error
	FailCard(ctx context.Context, cardID string, code run.FailureCode, detail string) error
	CIRerunDone(ctx context.Context, cardID string) error
}

// Watcher acompanha os PRs dos cards em revisão.
type Watcher struct {
	GitHub GitHub
	Store  Store
	Token  func(ctx context.Context) (string, error) // token de instalação do App, com cache
	Log    *slog.Logger
}

// Tick confere uma vez cada card em revisão. Erro num card não impede os outros.
func (w *Watcher) Tick(ctx context.Context) error {
	cards, err := w.Store.ReviewQueue(ctx)
	if err != nil || len(cards) == 0 {
		return err
	}
	token, err := w.Token(ctx)
	if err != nil {
		return fmt.Errorf("token do GitHub App: %w", err)
	}
	for _, c := range cards {
		if err := w.check(ctx, token, c); err != nil {
			w.Log.Warn("não conferiu o PR do card", "card", c.CardID, "pr", c.PRNumber, "err", err)
		}
	}
	return nil
}

func (w *Watcher) check(ctx context.Context, token string, c store.Reviewing) error {
	snap := Snapshot{Card: c}
	var err error
	if snap.PR, err = w.GitHub.GetPullRequest(ctx, token, c.Repo, c.PRNumber); err != nil {
		return err
	}
	if snap.PR.State == "open" {
		if snap.Checks, err = w.GitHub.CheckRuns(ctx, token, c.Repo, snap.PR.Head.SHA); err != nil {
			return err
		}
		if snap.Reviews, err = w.GitHub.Reviews(ctx, token, c.Repo, c.PRNumber); err != nil {
			return err
		}
	}
	log := w.Log.With("card", c.CardID, "pr", c.PRNumber)
	if c.RerunRequested && snap.PR.State == "open" {
		return w.rerun(ctx, token, c, snap, log)
	}
	d := Decide(snap)
	switch d.Action {
	case ActComplete:
		log.Info("PR mergeado: card concluído")
		return ignoreMoved(w.Store.CompleteCard(ctx, c.CardID))
	case ActFail:
		log.Warn("PR fechado sem merge", "failure_code", d.Code)
		return ignoreMoved(w.Store.FailCard(ctx, c.CardID, d.Code, d.Detail))
	case ActPhase:
		pending, changed, err := w.pending(ctx, token, c, snap, d)
		if err != nil || !changed {
			return err
		}
		log.Info("fase do PR", "phase", d.Phase, "correction_ready", pending != nil)
		return ignoreMoved(w.Store.SetReviewState(ctx, c.CardID, d.Phase, pending))
	}
	return fmt.Errorf("decisão desconhecida %q", d.Action)
}

// pending monta a correção pronta para o botão. Só busca log ou comentários quando algo mudou,
// para não baixar o mesmo log a cada volta.
func (w *Watcher) pending(ctx context.Context, token string, c store.Reviewing, snap Snapshot, d Decision) (*store.Pending, bool, error) {
	same := c.Phase != nil && *c.Phase == d.Phase
	switch d.Phase {
	case store.PhaseCIFailed:
		if same && c.HasPending {
			return nil, false, nil
		}
		var logs []CheckLog
		for _, chk := range d.Failed {
			l, err := w.GitHub.JobLog(ctx, token, c.Repo, chk.ID)
			logs = append(logs, CheckLog{Check: chk, Log: l, Err: err})
		}
		return &store.Pending{Kind: run.KindFixCI, Feedback: CIFeedback(c.PRNumber, snap.PR.Head.SHA, logs)}, true, nil
	case store.PhaseChangesRequested:
		if same && c.PendingReviewID != nil && *c.PendingReviewID == d.Review.ID {
			return nil, false, nil
		}
		comments, err := w.GitHub.ReviewComments(ctx, token, c.Repo, c.PRNumber, d.Review.ID)
		if err != nil {
			return nil, false, err
		}
		id := d.Review.ID
		return &store.Pending{Kind: run.KindAddressReview, Feedback: ReviewFeedback(c.PRNumber, *d.Review, comments), ReviewID: &id}, true, nil
	}
	return nil, !same || c.HasPending, nil
}

// rerun executa o "Rodar o CI de novo" pedido no board: refaz os jobs vermelhos do último commit.
func (w *Watcher) rerun(ctx context.Context, token string, c store.Reviewing, snap Snapshot, log *slog.Logger) error {
	_, failed := CIState(snap.Checks)
	var ids []int64
	for _, chk := range failed {
		ids = append(ids, chk.ID)
	}
	if len(ids) > 0 {
		if err := w.GitHub.RerunFailedJobs(ctx, token, c.Repo, ids); err != nil {
			return fmt.Errorf("rodando o CI de novo: %w", err)
		}
	}
	log.Info("CI rodando de novo a pedido do board", "checks", names(failed))
	return ignoreMoved(w.Store.CIRerunDone(ctx, c.CardID))
}

// ignoreMoved: o card saiu de revisão entre a leitura e a escrita (outro processo o moveu). Nada a fazer.
func ignoreMoved(err error) error {
	if errors.Is(err, store.ErrInvalidTransition) {
		return nil
	}
	return err
}

// Loop confere os PRs a cada intervalo até o contexto ser cancelado.
func (w *Watcher) Loop(ctx context.Context, every time.Duration) {
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("erro no observador de PRs", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
