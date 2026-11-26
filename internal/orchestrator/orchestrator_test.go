package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

type fakeStore struct {
	queue    []*store.Claimed
	finished map[int64]run.Result
	conts    map[int64]string
}

func (f *fakeStore) ClaimRun(context.Context) (*store.Claimed, error) {
	if len(f.queue) == 0 {
		return nil, nil
	}
	c := f.queue[0]
	f.queue = f.queue[1:]
	return c, nil
}

func (f *fakeStore) SetContainer(_ context.Context, id int64, c string) error {
	f.conts[id] = c
	return nil
}

func (f *fakeStore) FinishRun(_ context.Context, id int64, r run.Result, _ string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	f.finished[id] = r
	return nil
}

// fakeExecutor grava o result.json que o worker gravaria, ou devolve um erro.
type fakeExecutor struct {
	result *run.Result
	err    error
}

func (f fakeExecutor) Execute(_ context.Context, _ int64, _ card.Card, dir string, onStart func(string)) (string, error) {
	onStart("container-1")
	if f.err != nil {
		return "log", f.err
	}
	if f.result != nil {
		if err := f.result.Write(filepath.Join(dir, "result.json")); err != nil {
			return "", err
		}
	}
	return "log", nil
}

func newOrch(t *testing.T, exec Executor, pub Publisher) (*Orchestrator, *fakeStore) {
	st := &fakeStore{
		queue:    []*store.Claimed{{RunID: 1, Card: card.Card{ID: "ADL-1", Title: "Adicionar GET /health"}}},
		finished: map[int64]run.Result{}, conts: map[int64]string{},
	}
	return &Orchestrator{Store: st, Executor: exec, Publish: pub, WorkDir: t.TempDir(), Timeout: time.Minute,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, st
}

func okResult() *run.Result {
	return &run.Result{CardID: "ADL-1", Status: run.StatusSucceeded, Branch: "agent/adl-1-x",
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "s", TestsRun: []string{"t"}, Limitations: []string{}}}
}

func TestSuccessfulRunIsPublished(t *testing.T) {
	pub := func(_ context.Context, cardPath, outDir string) (run.Result, error) {
		r, err := run.ReadResult(filepath.Join(outDir, "result.json"))
		r.PRNumber, r.PRURL = 2, "https://github.com/o/r/pull/2"
		return r, err
	}
	o, st := newOrch(t, fakeExecutor{result: okResult()}, pub)
	worked, err := o.Tick(context.Background())
	if err != nil || !worked {
		t.Fatalf("tick: %v %v", worked, err)
	}
	if r := st.finished[1]; r.Status != run.StatusSucceeded || r.PRNumber != 2 || st.conts[1] != "container-1" {
		t.Fatalf("resultado: %+v containers %v", r, st.conts)
	}
}

func TestFailuresAlwaysCarryACode(t *testing.T) {
	noPub := func(context.Context, string, string) (run.Result, error) {
		t.Fatal("run com falha não pode ser publicado")
		return run.Result{}, nil
	}
	workerFailed := &run.Result{CardID: "ADL-1"}
	workerFailed.Failed(run.Fail(run.FailureBudgetExceeded, "teto"))
	cases := map[string]struct {
		exec Executor
		pub  Publisher
		want run.FailureCode
	}{
		"falha do worker é preservada": {fakeExecutor{result: workerFailed}, noPub, run.FailureBudgetExceeded},
		"timeout":                      {fakeExecutor{err: ErrTimeout}, noPub, run.FailureTimeout},
		"erro de infraestrutura":       {fakeExecutor{err: errors.New("docker fora")}, noPub, run.FailureWorkspaceError},
		"worker sem result.json":       {fakeExecutor{}, noPub, run.FailureAgentError},
		"falha ao publicar": {fakeExecutor{result: okResult()}, func(context.Context, string, string) (run.Result, error) {
			return run.Result{}, errors.New("GitHub fora")
		}, run.FailurePublishError},
	}
	for name, tc := range cases {
		o, st := newOrch(t, tc.exec, tc.pub)
		if _, err := o.Tick(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r := st.finished[1]; r.Status != run.StatusFailed || r.FailureCode != tc.want {
			t.Errorf("%s: %+v, esperado %s", name, r, tc.want)
		}
	}
}

func TestEmptyQueueDoesNothing(t *testing.T) {
	o, st := newOrch(t, fakeExecutor{}, nil)
	st.queue = nil
	worked, err := o.Tick(context.Background())
	if worked || err != nil {
		t.Fatalf("fila vazia: %v %v", worked, err)
	}
}
