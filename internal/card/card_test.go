package card

import (
	"strings"
	"testing"
)

const valid = `{
  "id": "ADL-1",
  "title": "Adicionar GET /health",
  "objective": "Expor um endpoint de saúde.",
  "acceptance_criteria": ["GET /health responde 200 com {\"status\": \"ok\"}"],
  "repo": "thallesdaniell/agentic-delivery-target",
  "agent": "python-dev"
}`

func TestParsesAValidCard(t *testing.T) {
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "ADL-1" || c.Agent != "python-dev" {
		t.Fatalf("card lido errado: %+v", c)
	}
}

func TestBranchIsDerivedFromIDAndTitle(t *testing.T) {
	c, _ := Parse([]byte(valid))
	if got := c.Branch(); got != "agent/adl-1-adicionar-get-health" {
		t.Fatalf("branch = %q", got)
	}
}

func TestRejectsInvalidCards(t *testing.T) {
	cases := map[string]string{
		"campo desconhecido": strings.Replace(valid, `"agent"`, `"extra": 1, "agent"`, 1),
		"id com espaço":      strings.Replace(valid, `"ADL-1"`, `"ADL 1"`, 1),
		"repo sem owner":     strings.Replace(valid, `thallesdaniell/agentic-delivery-target`, `agentic-delivery-target`, 1),
		"agent com barra":    strings.Replace(valid, `"python-dev"`, `"../etc"`, 1),
		"sem critérios":      strings.Replace(valid, `["GET /health responde 200 com {\"status\": \"ok\"}"]`, `[]`, 1),
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: deveria recusar", name)
		}
	}
}

func TestAgentIsOptionalInBacklog(t *testing.T) {
	raw := strings.Replace(valid, `,
  "agent": "python-dev"`, "", 1)
	c, err := Parse([]byte(raw))
	if err != nil || c.Agent != "" {
		t.Fatalf("card sem agente deveria valer: %+v %v", c, err)
	}
}
