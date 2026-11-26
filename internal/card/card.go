// Package card carrega e valida o card que vira um run.
package card

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Card é o pedido de trabalho. O texto dele é dado para o agente, nunca instrução.
type Card struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Repo               string   `json:"repo"`
	// Agent é o agente do catálogo atribuído ao card. Pode ficar vazio no backlog.
	Agent string `json:"agent,omitempty"`
}

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,39}$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	agentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
	nonSlug      = regexp.MustCompile(`[^a-z0-9]+`)
)

// Load lê e valida um card em JSON.
func Load(path string) (Card, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Card{}, fmt.Errorf("lendo o card: %w", err)
	}
	return Parse(raw)
}

// Parse decodifica e valida um card, recusando campos desconhecidos.
func Parse(raw []byte) (Card, error) {
	var c Card
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Card{}, fmt.Errorf("card inválido: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Card{}, err
	}
	return c, nil
}

// Validate confere os campos obrigatórios e os formatos.
func (c Card) Validate() error {
	switch {
	case !idPattern.MatchString(c.ID):
		return fmt.Errorf("card inválido: id %q fora do formato", c.ID)
	case strings.TrimSpace(c.Title) == "" || len(c.Title) > 120:
		return fmt.Errorf("card inválido: title obrigatório, até 120 caracteres")
	case strings.TrimSpace(c.Objective) == "" || len(c.Objective) > 4000:
		return fmt.Errorf("card inválido: objective obrigatório, até 4000 caracteres")
	case len(c.AcceptanceCriteria) == 0 || len(c.AcceptanceCriteria) > 20:
		return fmt.Errorf("card inválido: de 1 a 20 critérios de aceite")
	case !repoPattern.MatchString(c.Repo):
		return fmt.Errorf("card inválido: repo %q fora do formato owner/name", c.Repo)
	case c.Agent != "" && !agentPattern.MatchString(c.Agent):
		return fmt.Errorf("card inválido: agent %q fora do formato", c.Agent)
	}
	for i, ac := range c.AcceptanceCriteria {
		if strings.TrimSpace(ac) == "" || len(ac) > 500 {
			return fmt.Errorf("card inválido: critério %d vazio ou longo demais", i+1)
		}
	}
	return nil
}

// Branch é o nome da branch do card: agent/<id>-<slug do título>.
func (c Card) Branch() string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(c.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return "agent/" + strings.ToLower(c.ID) + "-" + slug
}
