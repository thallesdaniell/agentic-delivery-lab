package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/dockerapi"
)

// DockerExecutor roda o worker num container novo por run, sem bind mount:
// o card e o agente vão por variável de ambiente e a saída é copiada de /out quando o container para.
type DockerExecutor struct {
	Docker      *dockerapi.Client
	Image       string
	Env         []string      // ANTHROPIC_API_KEY, RUN_BUDGET_USD, RUN_TIMEOUT
	LogInterval time.Duration // de quanto em quanto tempo o log vai para o banco; padrão 3 s
}

// Execute implementa Executor.
func (d DockerExecutor) Execute(ctx context.Context, runID int64, c card.Card, spec catalog.Spec, outDir string, hooks Hooks) (string, error) {
	rawCard, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	id, err := d.Docker.Create(ctx, dockerapi.Spec{
		Name:  "adl-run-" + strconv.FormatInt(runID, 10),
		Image: d.Image,
		Cmd:   []string{"worker"},
		Env:   append([]string{"ADL_CARD_JSON=" + string(rawCard), "ADL_AGENT_SPEC=" + string(rawSpec)}, d.Env...),
		Labels: map[string]string{"adl.run_id": strconv.FormatInt(runID, 10), "adl.card_id": c.ID,
			"adl.agent": spec.Agent, "adl.agent_version": spec.AgentVersion},
		Memory: 2 << 30,
		Pids:   512,
	})
	if err != nil {
		return "", err
	}
	// Limpeza com contexto próprio: roda mesmo depois de timeout ou cancelamento.
	cleanup := context.WithoutCancel(ctx)
	defer func() { _ = d.Docker.Remove(cleanup, id) }()
	if hooks.OnStart != nil {
		hooks.OnStart(id)
	}
	if err := d.Docker.Start(ctx, id); err != nil {
		return "", err
	}
	stop := d.streamLogs(ctx, id, hooks.OnLog)
	_, waitErr := d.Docker.Wait(ctx, id)
	stop()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = d.Docker.Kill(cleanup, id)
		log, _ := d.Docker.Logs(cleanup, id, 200)
		return log, ErrTimeout
	}
	if waitErr != nil {
		return "", fmt.Errorf("esperando o worker: %w", waitErr)
	}
	log, _ := d.Docker.Logs(cleanup, id, 200)
	if err := d.Docker.CopyOut(cleanup, id, "/out", outDir); err != nil {
		return log, fmt.Errorf("copiando /out do worker: %w", err)
	}
	return log, nil
}

// streamLogs manda o fim do log para onLog enquanto o container roda. Devolve a função que para.
func (d DockerExecutor) streamLogs(ctx context.Context, id string, onLog func(string)) func() {
	if onLog == nil {
		return func() {}
	}
	every := d.LogInterval
	if every <= 0 {
		every = 3 * time.Second
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(every)
		defer t.Stop()
		last := ""
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if tail, err := d.Docker.Logs(ctx, id, 200); err == nil && tail != last {
					onLog(tail)
					last = tail
				}
			}
		}
	}()
	return func() { close(done); <-finished }
}
