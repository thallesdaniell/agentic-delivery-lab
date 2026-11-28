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

// GitHub é o que o observador lê do PR (e o comentário que ele deixa ao desistir).
type GitHub interface {
	GetPullRequest(ctx context.Context, token, repo string, number int) (githubapp.PRState, error)
	CheckRuns(ctx context.Context, token, repo, sha string) ([]githubapp.CheckRun, error)
	JobLog(ctx context.Context, token, repo string, jobID int64) (string, error)
	Reviews(ctx context.Context, token, repo string, number int) ([]githubapp.Review, error)
	ReviewComments(ctx context.Context, token, repo string, number int, reviewID int64) ([]githubapp.ReviewComment, error)
	CreateIssueComment(ctx context.Context, token, repo string, number int, body string) error
}

// Store é o que o observador muda no banco. Toda alteração só vale se o card ainda está em revisão.
type Store interface {
	ReviewQueue(ctx context.Context) ([]store.Reviewing, error)
	SetReviewPhase(ctx context.Context, cardID string, phase store.ReviewPhase) error
	CompleteCard(ctx context.Context, cardID string) error
	FailCard(ctx context.Context, cardID string, code run.FailureCode, detail string) error
	EnqueueRework(ctx context.Context, cardID string, kind run.Kind, feedback string, reviewID *int64) (int64, error)
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
	d := Decide(snap)
	log := w.Log.With("card", c.CardID, "pr", c.PRNumber)
	switch d.Action {
	case ActPhase:
		if c.Phase != nil && *c.Phase == d.Phase {
			return nil
		}
		log.Info("fase do PR", "phase", d.Phase)
		return ignoreMoved(w.Store.SetReviewPhase(ctx, c.CardID, d.Phase))
	case ActComplete:
		log.Info("PR mergeado: card concluído")
		return ignoreMoved(w.Store.CompleteCard(ctx, c.CardID))
	case ActFail:
		log.Warn("ciclo de revisão encerrado", "failure_code", d.Code, "detail", d.Detail)
		if err := ignoreMoved(w.Store.FailCard(ctx, c.CardID, d.Code, d.Detail)); err != nil {
			return err
		}
		if d.Code != run.FailurePRClosed {
			w.comment(ctx, token, c, "O agente parou aqui: "+d.Detail+". O card foi para **Falhou** no board; daqui em diante é com uma pessoa.")
		}
		return nil
	case ActFixCI:
		var logs []CheckLog
		for _, chk := range d.Failed {
			l, err := w.GitHub.JobLog(ctx, token, c.Repo, chk.ID)
			logs = append(logs, CheckLog{Check: chk, Log: l, Err: err})
		}
		feedback := CIFeedback(c.PRNumber, snap.PR.Head.SHA, logs)
		runID, err := w.Store.EnqueueRework(ctx, c.CardID, run.KindFixCI, feedback, nil)
		if err != nil {
			return ignoreMoved(err)
		}
		log.Info("CI vermelho: card devolvido ao agente", "run", runID, "attempt", c.CIAttempts+1, "checks", names(d.Failed))
		return nil
	case ActAddressReview:
		comments, err := w.GitHub.ReviewComments(ctx, token, c.Repo, c.PRNumber, d.Review.ID)
		if err != nil {
			return err
		}
		feedback := ReviewFeedback(c.PRNumber, *d.Review, comments)
		id := d.Review.ID
		runID, err := w.Store.EnqueueRework(ctx, c.CardID, run.KindAddressReview, feedback, &id)
		if err != nil {
			return ignoreMoved(err)
		}
		log.Info("revisão pediu mudanças: card devolvido ao agente", "run", runID, "round", c.ReviewRounds+1, "review", id)
		return nil
	}
	return fmt.Errorf("decisão desconhecida %q", d.Action)
}

// comment avisa no PR. Falhar aqui não muda o card: o board já mostra o que aconteceu.
func (w *Watcher) comment(ctx context.Context, token string, c store.Reviewing, body string) {
	if err := w.GitHub.CreateIssueComment(ctx, token, c.Repo, c.PRNumber, body); err != nil {
		w.Log.Warn("não comentou no PR", "card", c.CardID, "pr", c.PRNumber, "err", err)
	}
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
