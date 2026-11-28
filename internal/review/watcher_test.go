package review

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

type fakeGitHub struct {
	pr      githubapp.PRState
	checks  []githubapp.CheckRun
	reviews []githubapp.Review
	logErr  error
	logs    int
	reruns  [][]int64
}

func (f *fakeGitHub) GetPullRequest(context.Context, string, string, int) (githubapp.PRState, error) {
	return f.pr, nil
}
func (f *fakeGitHub) CheckRuns(context.Context, string, string, string) ([]githubapp.CheckRun, error) {
	return f.checks, nil
}
func (f *fakeGitHub) JobLog(context.Context, string, string, int64) (string, error) {
	f.logs++
	return "1 failed, 4 passed", f.logErr
}
func (f *fakeGitHub) Reviews(context.Context, string, string, int) ([]githubapp.Review, error) {
	return f.reviews, nil
}
func (f *fakeGitHub) ReviewComments(context.Context, string, string, int, int64) ([]githubapp.ReviewComment, error) {
	return []githubapp.ReviewComment{{Path: "a.py", Body: "renomeie"}}, nil
}
func (f *fakeGitHub) RerunFailedJobs(_ context.Context, _, _ string, ids []int64) error {
	f.reruns = append(f.reruns, ids)
	return nil
}

type fakeStore struct {
	cards     []store.Reviewing
	phase     store.ReviewPhase
	pending   *store.Pending
	writes    int
	done      bool
	failed    run.FailureCode
	rerunDone bool
	moved     bool // simula o card que saiu de revisão no meio
}

func (f *fakeStore) ReviewQueue(context.Context) ([]store.Reviewing, error) { return f.cards, nil }
func (f *fakeStore) SetReviewState(_ context.Context, _ string, p store.ReviewPhase, pend *store.Pending) error {
	if f.moved {
		return store.ErrInvalidTransition
	}
	f.phase, f.pending = p, pend
	f.writes++
	return nil
}
func (f *fakeStore) CompleteCard(context.Context, string) error { f.done = true; return nil }
func (f *fakeStore) FailCard(_ context.Context, _ string, c run.FailureCode, _ string) error {
	f.failed = c
	return nil
}
func (f *fakeStore) CIRerunDone(context.Context, string) error { f.rerunDone = true; return nil }

func watcher(gh *fakeGitHub, st *fakeStore) *Watcher {
	return &Watcher{GitHub: gh, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Token: func(context.Context) (string, error) { return "tok", nil }}
}

func inReview() []store.Reviewing {
	return []store.Reviewing{{CardID: "ADL-4", Repo: "o/r", PRNumber: 5}}
}

func redCI() *fakeGitHub {
	return &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"},
		checks: []githubapp.CheckRun{{ID: 11, Name: "test", Status: "completed", Conclusion: "failure"}}}
}

// CI vermelho não devolve o card: a etiqueta muda e a correção fica pronta para o botão.
func TestWatcherPreparesTheCIFixWithoutQueueingIt(t *testing.T) {
	st := &fakeStore{cards: inReview()}
	if err := watcher(redCI(), st).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.phase != store.PhaseCIFailed || st.pending == nil || st.pending.Kind != run.KindFixCI || !strings.Contains(st.pending.Feedback, "1 failed, 4 passed") {
		t.Fatalf("correção pronta: %s %+v", st.phase, st.pending)
	}
}

func TestWatcherDoesNotDownloadTheSameLogTwice(t *testing.T) {
	phase := store.PhaseCIFailed
	cards := inReview()
	cards[0].Phase, cards[0].HasPending = &phase, true
	gh, st := redCI(), &fakeStore{cards: cards}
	_ = watcher(gh, st).Tick(context.Background())
	if gh.logs != 0 || st.writes != 0 {
		t.Fatalf("nada mudou: %d logs, %d escritas", gh.logs, st.writes)
	}
}

func TestWatcherPreparesTheReviewFix(t *testing.T) {
	r := githubapp.Review{ID: 77, State: "CHANGES_REQUESTED", Body: "falta teste"}
	r.User.Login = "ana"
	gh := &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: green(), reviews: []githubapp.Review{r}}
	st := &fakeStore{cards: inReview()}
	_ = watcher(gh, st).Tick(context.Background())
	if st.pending == nil || st.pending.Kind != run.KindAddressReview || *st.pending.ReviewID != 77 || !strings.Contains(st.pending.Feedback, "a.py: renomeie") {
		t.Fatalf("correção da revisão: %+v", st.pending)
	}
}

func TestWatcherRerunsTheCIWhenAsked(t *testing.T) {
	cards := inReview()
	cards[0].RerunRequested = true
	gh, st := redCI(), &fakeStore{cards: cards}
	_ = watcher(gh, st).Tick(context.Background())
	if len(gh.reruns) != 1 || gh.reruns[0][0] != 11 || !st.rerunDone {
		t.Fatalf("rerun: %v, feito=%v", gh.reruns, st.rerunDone)
	}
}

func TestWatcherCompletesOnMergeAndFailsOnClose(t *testing.T) {
	st := &fakeStore{cards: inReview()}
	_ = watcher(&fakeGitHub{pr: githubapp.PRState{State: "closed", Merged: true}}, st).Tick(context.Background())
	if !st.done {
		t.Fatal("merge deveria concluir o card")
	}
	st = &fakeStore{cards: inReview()}
	_ = watcher(&fakeGitHub{pr: githubapp.PRState{Number: 5, State: "closed"}}, st).Tick(context.Background())
	if st.failed != run.FailurePRClosed {
		t.Fatalf("PR fechado: %s", st.failed)
	}
}

func TestWatcherClearsAStalePendingAndIgnoresAMovedCard(t *testing.T) {
	phase := store.PhaseCIFailed
	cards := inReview()
	cards[0].Phase, cards[0].HasPending = &phase, true
	st := &fakeStore{cards: cards}
	_ = watcher(&fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: green()}, st).Tick(context.Background())
	if st.phase != store.PhaseAwaitingReview || st.pending != nil {
		t.Fatalf("CI verde limpa a correção pronta: %s %+v", st.phase, st.pending)
	}

	gh := redCI()
	gh.logErr = errors.New("HTTP 403")
	if err := watcher(gh, &fakeStore{cards: inReview(), moved: true}).Tick(context.Background()); err != nil {
		t.Fatalf("card que saiu de revisão no meio não é erro: %v", err)
	}
}

func TestWatcherWithoutCardsDoesNotAskForAToken(t *testing.T) {
	w := watcher(&fakeGitHub{}, &fakeStore{})
	w.Token = func(context.Context) (string, error) {
		t.Fatal("sem cards em revisão, não pede token")
		return "", nil
	}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}
