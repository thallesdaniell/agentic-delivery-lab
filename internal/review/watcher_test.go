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
	pr       githubapp.PRState
	checks   []githubapp.CheckRun
	reviews  []githubapp.Review
	logErr   error
	comments []string
}

func (f *fakeGitHub) GetPullRequest(context.Context, string, string, int) (githubapp.PRState, error) {
	return f.pr, nil
}
func (f *fakeGitHub) CheckRuns(context.Context, string, string, string) ([]githubapp.CheckRun, error) {
	return f.checks, nil
}
func (f *fakeGitHub) JobLog(context.Context, string, string, int64) (string, error) {
	return "1 failed, 4 passed", f.logErr
}
func (f *fakeGitHub) Reviews(context.Context, string, string, int) ([]githubapp.Review, error) {
	return f.reviews, nil
}
func (f *fakeGitHub) ReviewComments(context.Context, string, string, int, int64) ([]githubapp.ReviewComment, error) {
	return []githubapp.ReviewComment{{Path: "a.py", Body: "renomeie"}}, nil
}
func (f *fakeGitHub) CreateIssueComment(_ context.Context, _, _ string, _ int, body string) error {
	f.comments = append(f.comments, body)
	return nil
}

type fakeStore struct {
	cards    []store.Reviewing
	phase    store.ReviewPhase
	done     bool
	failed   run.FailureCode
	rework   run.Kind
	feedback string
	reviewID *int64
	moved    bool // simula o card que saiu de revisão no meio
}

func (f *fakeStore) ReviewQueue(context.Context) ([]store.Reviewing, error) { return f.cards, nil }
func (f *fakeStore) SetReviewPhase(_ context.Context, _ string, p store.ReviewPhase) error {
	f.phase = p
	return nil
}
func (f *fakeStore) CompleteCard(context.Context, string) error { f.done = true; return nil }
func (f *fakeStore) FailCard(_ context.Context, _ string, c run.FailureCode, _ string) error {
	f.failed = c
	return nil
}
func (f *fakeStore) EnqueueRework(_ context.Context, _ string, k run.Kind, fb string, id *int64) (int64, error) {
	if f.moved {
		return 0, store.ErrInvalidTransition
	}
	f.rework, f.feedback, f.reviewID = k, fb, id
	return 10, nil
}

func watcher(gh *fakeGitHub, st *fakeStore) *Watcher {
	return &Watcher{GitHub: gh, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Token: func(context.Context) (string, error) { return "tok", nil }}
}

func inReview() []store.Reviewing {
	return []store.Reviewing{{CardID: "ADL-4", Repo: "o/r", PRNumber: 5}}
}

func TestWatcherSendsTheCILogBackToTheAgent(t *testing.T) {
	gh := &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: []githubapp.CheckRun{{ID: 1, Name: "test", Status: "completed", Conclusion: "failure"}}}
	st := &fakeStore{cards: inReview()}
	if err := watcher(gh, st).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.rework != run.KindFixCI || !strings.Contains(st.feedback, "1 failed, 4 passed") {
		t.Fatalf("correção do CI: %s %q", st.rework, st.feedback)
	}
}

func TestWatcherSendsReviewCommentsAndRecordsTheReview(t *testing.T) {
	r := githubapp.Review{ID: 77, State: "CHANGES_REQUESTED", Body: "falta teste"}
	r.User.Login = "ana"
	gh := &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: green(), reviews: []githubapp.Review{r}}
	st := &fakeStore{cards: inReview()}
	_ = watcher(gh, st).Tick(context.Background())
	if st.rework != run.KindAddressReview || st.reviewID == nil || *st.reviewID != 77 || !strings.Contains(st.feedback, "a.py: renomeie") {
		t.Fatalf("correção da revisão: %s %v %q", st.rework, st.reviewID, st.feedback)
	}
}

func TestWatcherCompletesOnMergeAndCommentsWhenGivingUp(t *testing.T) {
	st := &fakeStore{cards: inReview()}
	_ = watcher(&fakeGitHub{pr: githubapp.PRState{State: "closed", Merged: true}}, st).Tick(context.Background())
	if !st.done {
		t.Fatal("merge deveria concluir o card")
	}

	gh := &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: []githubapp.CheckRun{{Name: "test", Status: "completed", Conclusion: "failure"}}}
	cards := inReview()
	cards[0].CIAttempts = run.MaxCIAttempts
	st = &fakeStore{cards: cards}
	_ = watcher(gh, st).Tick(context.Background())
	if st.failed != run.FailureCIFailedAfterRetries || len(gh.comments) != 1 {
		t.Fatalf("desistência: %s, comentários %v", st.failed, gh.comments)
	}
}

func TestWatcherSkipsUnchangedPhaseAndIgnoresAMovedCard(t *testing.T) {
	phase := store.PhaseAwaitingReview
	cards := inReview()
	cards[0].Phase = &phase
	st := &fakeStore{cards: cards}
	_ = watcher(&fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: green()}, st).Tick(context.Background())
	if st.phase != "" {
		t.Fatalf("fase igual não deveria ser regravada: %s", st.phase)
	}

	st = &fakeStore{cards: inReview(), moved: true}
	gh := &fakeGitHub{pr: githubapp.PRState{Number: 5, State: "open"}, checks: []githubapp.CheckRun{{Name: "t", Status: "completed", Conclusion: "failure"}},
		logErr: errors.New("HTTP 403")}
	if err := watcher(gh, st).Tick(context.Background()); err != nil {
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
