package publish

import (
	"strings"
	"testing"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

func TestPRBodyCarriesCardHandoffAndCost(t *testing.T) {
	c := card.Card{ID: "ADL-1", Title: "Adicionar GET /health", Objective: "Endpoint de saúde.", AcceptanceCriteria: []string{"responde 200"}}
	res := run.Result{Model: "claude-sonnet-5-5", NumTurns: 9, CostUSD: 0.31, DurationSec: 80,
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "Criei a rota.", TestsRun: []string{"uv run pytest: 6 passed"}, Limitations: []string{"sem teste de carga"}}}
	body := PRBody(c, res)
	for _, want := range []string{"ADL-1", "responde 200", "Criei a rota.", "6 passed", "sem teste de carga", "US$ 0.31", "O merge é de uma pessoa"} {
		if !strings.Contains(body, want) {
			t.Errorf("corpo do PR sem %q", want)
		}
	}
}
