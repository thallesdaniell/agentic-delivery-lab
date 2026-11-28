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

func TestReworkCommentNamesTheAttempt(t *testing.T) {
	res := run.Result{Model: "m", NumTurns: 5, CostUSD: 0.03,
		Handoff: &handoff.Handoff{Outcome: handoff.OutcomeDone, Summary: "corrigi o teste", TestsRun: []string{"pytest: ok"}, Limitations: []string{}}}
	body := ReworkComment(res, run.Task{Kind: run.KindFixCI, Attempt: 1})
	for _, want := range []string{"Corrige o CI (tentativa 1 de 2)", "corrigi o teste", "pytest: ok", "US$ 0.03"} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q no comentário:\n%s", want, body)
		}
	}
	if got := ReworkCommitMessage(card.Card{ID: "ADL-4"}, res, run.Task{Kind: run.KindAddressReview, Attempt: 2}); !strings.HasPrefix(got, "[ADL-4] Atende à revisão (rodada 2 de 3)") {
		t.Fatalf("mensagem do commit: %q", got)
	}
}
