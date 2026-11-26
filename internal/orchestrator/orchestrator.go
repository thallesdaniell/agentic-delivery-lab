// Package orchestrator tira runs da fila, roda o worker num container e publica o resultado.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

// Store é o que o orquestrador precisa do banco.
type Store interface {
	ClaimRun(ctx context.Context) (*store.Claimed, error)
	SetContainer(ctx context.Context, runID int64, containerID string) error
	UpdateRunLog(ctx context.Context, runID int64, log string) error
	FinishRun(ctx context.Context, runID int64, res run.Result, log string) error
}

// Hooks avisa o orquestrador do andamento do worker.
type Hooks struct {
	OnStart func(containerID string)
	OnLog   func(tail string) // chamado periodicamente com o fim do log, enquanto o worker roda
}

// Executor roda o worker de um run com o agente congelado e deixa result.json e changes.patch em outDir.
// Devolve o log do worker. Um erro de Executor é falha de infraestrutura, não do agente.
type Executor interface {
	Execute(ctx context.Context, runID int64, c card.Card, spec catalog.Spec, outDir string, hooks Hooks) (log string, err error)
}

// Publisher abre o PR de um run que terminou com sucesso (publish.Run em produção).
type Publisher func(ctx context.Context, cardPath, outDir string) (run.Result, error)

// Orchestrator junta fila, execução e publicação.
type Orchestrator struct {
	Store    Store
	Executor Executor
	Publish  Publisher
	WorkDir  string
	Timeout  time.Duration // tempo máximo do worker, além do qual o run é timeout
	Log      *slog.Logger
}

// ErrTimeout é devolvido pelo Executor quando o worker passa do tempo máximo.
var ErrTimeout = errors.New("worker passou do tempo máximo")

// Tick processa no máximo um run. Devolve false se a fila estava vazia.
func (o *Orchestrator) Tick(ctx context.Context) (bool, error) {
	cl, err := o.Store.ClaimRun(ctx)
	if err != nil || cl == nil {
		return false, err
	}
	log := o.Log.With("run", cl.RunID, "card", cl.Card.ID, "agent", cl.Spec.Agent)
	log.Info("run iniciado")
	res, workerLog := o.process(ctx, cl, log)
	// O resultado é gravado mesmo se o contexto do loop foi cancelado no meio.
	if err := o.Store.FinishRun(context.WithoutCancel(ctx), cl.RunID, res, workerLog); err != nil {
		return true, fmt.Errorf("gravando o run %d: %w", cl.RunID, err)
	}
	log.Info("run finalizado", "status", res.Status, "failure_code", res.FailureCode, "cost_usd", res.CostUSD, "pr", res.PRURL)
	return true, nil
}

func (o *Orchestrator) process(ctx context.Context, cl *store.Claimed, log *slog.Logger) (run.Result, string) {
	failed := func(code run.FailureCode, detail string) run.Result {
		r := run.Result{CardID: cl.Card.ID, Branch: cl.Card.Branch()}
		r.Failed(run.Fail(code, detail))
		return r
	}
	dir, err := os.MkdirTemp(o.WorkDir, fmt.Sprintf("run-%d-", cl.RunID))
	if err != nil {
		return failed(run.FailureWorkspaceError, err.Error()), ""
	}
	defer os.RemoveAll(dir)
	cardPath := filepath.Join(dir, "card.json")
	raw, _ := json.Marshal(cl.Card)
	if err := os.WriteFile(cardPath, raw, 0o600); err != nil {
		return failed(run.FailureWorkspaceError, err.Error()), ""
	}

	ectx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	workerLog, err := o.Executor.Execute(ectx, cl.RunID, cl.Card, cl.Spec, dir, Hooks{
		OnStart: func(id string) {
			if err := o.Store.SetContainer(ctx, cl.RunID, id); err != nil {
				log.Warn("não gravou o container do run", "err", err)
			}
		},
		OnLog: func(tail string) {
			if err := o.Store.UpdateRunLog(ctx, cl.RunID, tail); err != nil {
				log.Warn("não gravou o log do run", "err", err)
			}
		},
	})
	switch {
	case errors.Is(err, ErrTimeout):
		return failed(run.FailureTimeout, "o worker passou do tempo máximo e foi encerrado"), workerLog
	case err != nil:
		return failed(run.FailureWorkspaceError, "executando o worker: "+err.Error()), workerLog
	}

	res, err := run.ReadResult(filepath.Join(dir, "result.json"))
	if err != nil {
		return failed(run.FailureAgentError, "o worker terminou sem result.json válido: "+err.Error()), workerLog
	}
	if res.Status != run.StatusSucceeded {
		return res, workerLog
	}
	published, err := o.Publish(ctx, cardPath, dir)
	if err != nil {
		res.Failed(run.Fail(run.FailurePublishError, err.Error()))
		return res, workerLog
	}
	return published, workerLog
}

// Loop processa a fila até o contexto ser cancelado, esperando poll entre filas vazias.
func (o *Orchestrator) Loop(ctx context.Context, poll time.Duration) error {
	for {
		worked, err := o.Tick(ctx)
		if err != nil {
			o.Log.Error("erro no orquestrador", "err", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}
