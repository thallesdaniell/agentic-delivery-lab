package handoff

import (
	"encoding/json"
	"testing"
)

func TestSchemaIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(Schema), &v); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptsAValidHandoff(t *testing.T) {
	h, err := Parse(json.RawMessage(`{"outcome":"done","summary":"Adicionei GET /health.","tests_run":["uv run pytest: 6 passed"],"limitations":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if h.Outcome != OutcomeDone {
		t.Fatalf("outcome = %q", h.Outcome)
	}
}

func TestRejectsInvalidHandoffs(t *testing.T) {
	cases := map[string]string{
		"vazio":             ``,
		"null":              `null`,
		"outcome inventado": `{"outcome":"maybe","summary":"x","tests_run":["a"],"limitations":[]}`,
		"sem summary":       `{"outcome":"done","summary":" ","tests_run":["a"],"limitations":[]}`,
		"campo extra":       `{"outcome":"done","summary":"x","tests_run":["a"],"limitations":[],"pr":1}`,
		"done sem teste":    `{"outcome":"done","summary":"x","tests_run":[],"limitations":[]}`,
		"sem limitations":   `{"outcome":"done","summary":"x","tests_run":["a"]}`,
	}
	for name, raw := range cases {
		if _, err := Parse(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: deveria recusar", name)
		}
	}
}

func TestBlockedMayHaveNoTests(t *testing.T) {
	if _, err := Parse(json.RawMessage(`{"outcome":"blocked","summary":"Faltou acesso.","tests_run":[],"limitations":["x"]}`)); err != nil {
		t.Fatal(err)
	}
}
