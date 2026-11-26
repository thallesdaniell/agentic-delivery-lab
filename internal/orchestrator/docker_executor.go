package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/dockerapi"
)

// DockerExecutor roda o worker num container novo por run, sem bind mount:
// o card vai por variável de ambiente e a saída é copiada de /out quando o container para.
type DockerExecutor struct {
	Docker *dockerapi.Client
	Image  string
	Env    []string // ANTHROPIC_API_KEY, AGENT_MODEL, RUN_BUDGET_USD, RUN_TIMEOUT
}

// Execute implementa Executor.
func (d DockerExecutor) Execute(ctx context.Context, runID int64, c card.Card, outDir string, onStart func(string)) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	id, err := d.Docker.Create(ctx, dockerapi.Spec{
		Name:   "adl-run-" + strconv.FormatInt(runID, 10),
		Image:  d.Image,
		Cmd:    []string{"worker"},
		Env:    append([]string{"ADL_CARD_JSON=" + string(raw)}, d.Env...),
		Labels: map[string]string{"adl.run_id": strconv.FormatInt(runID, 10), "adl.card_id": c.ID},
		Memory: 2 << 30,
		Pids:   512,
	})
	if err != nil {
		return "", err
	}
	// Limpeza com contexto próprio: roda mesmo depois de timeout ou cancelamento.
	cleanup := context.WithoutCancel(ctx)
	defer func() { _ = d.Docker.Remove(cleanup, id) }()
	onStart(id)
	if err := d.Docker.Start(ctx, id); err != nil {
		return "", err
	}
	_, waitErr := d.Docker.Wait(ctx, id)
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
