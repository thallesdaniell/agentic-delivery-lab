// Package handoff define o contrato que o agente devolve ao terminar.
// O Claude Code recebe o Schema por --json-schema, e o worker valida de novo:
// um handoff que não passa aqui encerra o run com handoff_invalid.
package handoff

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Outcome é o que o agente declara sobre o próprio trabalho.
type Outcome string

const (
	OutcomeDone    Outcome = "done"
	OutcomeBlocked Outcome = "blocked"
)

// Handoff é a saída estruturada do agente.
type Handoff struct {
	Outcome     Outcome  `json:"outcome"`
	Summary     string   `json:"summary"`
	TestsRun    []string `json:"tests_run"`
	Limitations []string `json:"limitations"`
}

// Schema é o JSON Schema passado ao Claude Code.
const Schema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["outcome", "summary", "tests_run", "limitations"],
  "properties": {
    "outcome": {"type": "string", "enum": ["done", "blocked"], "description": "done se os critérios de aceite foram atendidos e os testes passam; blocked se não foi possível"},
    "summary": {"type": "string", "minLength": 1, "maxLength": 2000, "description": "O que foi feito, em português, para a descrição do PR"},
    "tests_run": {"type": "array", "maxItems": 20, "items": {"type": "string", "maxLength": 300}, "description": "Comandos de verificação executados e o resultado de cada um"},
    "limitations": {"type": "array", "maxItems": 20, "items": {"type": "string", "maxLength": 500}, "description": "O que não foi feito ou não foi verificado"}
  }
}`

// Parse decodifica e valida um handoff, recusando campos desconhecidos.
func Parse(raw json.RawMessage) (Handoff, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Handoff{}, fmt.Errorf("o agente terminou sem handoff")
	}
	var h Handoff
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Handoff{}, fmt.Errorf("handoff fora do schema: %w", err)
	}
	return h, h.Validate()
}

// Validate impõe as mesmas regras do Schema.
func (h Handoff) Validate() error {
	switch h.Outcome {
	case OutcomeDone, OutcomeBlocked:
	default:
		return fmt.Errorf("handoff: outcome %q inválido", h.Outcome)
	}
	if strings.TrimSpace(h.Summary) == "" || len(h.Summary) > 2000 {
		return fmt.Errorf("handoff: summary obrigatório, até 2000 caracteres")
	}
	if h.TestsRun == nil || h.Limitations == nil {
		return fmt.Errorf("handoff: tests_run e limitations são obrigatórios (podem ser vazios)")
	}
	if len(h.TestsRun) > 20 || len(h.Limitations) > 20 {
		return fmt.Errorf("handoff: no máximo 20 itens por lista")
	}
	if h.Outcome == OutcomeDone && len(h.TestsRun) == 0 {
		return fmt.Errorf("handoff: outcome done exige ao menos um teste executado")
	}
	return nil
}
