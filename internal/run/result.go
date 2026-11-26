package run

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/handoff"
)

// Result é o que o worker grava em result.json e o publisher lê.
type Result struct {
	CardID       string           `json:"card_id"`
	Status       Status           `json:"status"`
	FailureCode  FailureCode      `json:"failure_code,omitempty"`
	Detail       string           `json:"detail,omitempty"`
	Branch       string           `json:"branch,omitempty"`
	Model        string           `json:"model,omitempty"`
	CostUSD      float64          `json:"cost_usd"`
	NumTurns     int              `json:"num_turns"`
	DurationSec  float64          `json:"duration_sec"`
	ChangedFiles []string         `json:"changed_files,omitempty"`
	Denials      []string         `json:"permission_denials,omitempty"`
	Handoff      *handoff.Handoff `json:"handoff,omitempty"`
	PRNumber     int              `json:"pr_number,omitempty"`
	PRURL        string           `json:"pr_url,omitempty"`
}

// Failed marca o resultado como falho com código.
func (r *Result) Failed(e *Error) {
	r.Status, r.FailureCode, r.Detail = StatusFailed, e.Code, e.Detail
}

// Validate garante o invariante: falha sempre com código válido, sucesso sem código.
func (r Result) Validate() error {
	switch r.Status {
	case StatusSucceeded:
		if r.FailureCode != "" {
			return fmt.Errorf("resultado: sucesso com failure_code %q", r.FailureCode)
		}
	case StatusFailed:
		if !r.FailureCode.Valid() {
			return fmt.Errorf("resultado: falha sem código válido (%q)", r.FailureCode)
		}
	default:
		return fmt.Errorf("resultado: status %q inválido", r.Status)
	}
	return nil
}

// Write grava o resultado validado em JSON.
func (r Result) Write(path string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadResult lê e valida um result.json.
func ReadResult(path string) (Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, err
	}
	return r, r.Validate()
}
