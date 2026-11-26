// Package catalog carrega as skills e os agentes versionados no repositório.
//
// Uma skill é skills/<nome>/SKILL.md. Um agente é agents/<nome>.json e combina
// uma skill, um modelo, um teto de custo e um subconjunto das tools que o worker permite.
// A versão de cada um é o hash do conteúdo: mudou o arquivo, mudou a versão.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/agent"
)

// Skill é uma SKILL.md com o frontmatter lido.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Version     string `json:"version"`
}

// Agent é a combinação de skill, modelo, teto de custo e tools.
type Agent struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skill       string   `json:"skill"`
	Model       string   `json:"model"`
	BudgetUSD   float64  `json:"budget_usd"`
	Tools       []string `json:"tools"`
	Version     string   `json:"version,omitempty"`
}

// Spec é o agente resolvido, com a skill dentro. O run guarda o Spec do momento em que
// entrou na fila e o worker roda exatamente ele, mesmo que o catálogo mude depois.
type Spec struct {
	Agent        string   `json:"agent"`
	AgentVersion string   `json:"agent_version"`
	Skill        string   `json:"skill"`
	SkillVersion string   `json:"skill_version"`
	SkillContent string   `json:"skill_content"`
	Model        string   `json:"model"`
	BudgetUSD    float64  `json:"budget_usd"`
	Tools        []string `json:"tools"`
}

// Catalog é o conjunto carregado do repositório.
type Catalog struct {
	Skills []Skill
	Agents []Agent
}

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
	modelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,79}$`)
)

// MaxBudgetUSD é o maior teto que um agente pode declarar.
const MaxBudgetUSD = 10.0

// Load lê root/skills e root/agents e valida tudo. Um erro em qualquer arquivo recusa o catálogo inteiro.
func Load(root string) (Catalog, error) {
	var cat Catalog
	skillFiles, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil {
		return cat, err
	}
	for _, f := range skillFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			return cat, err
		}
		s, err := ParseSkill(filepath.Base(filepath.Dir(f)), raw)
		if err != nil {
			return cat, err
		}
		cat.Skills = append(cat.Skills, s)
	}
	agentFiles, err := filepath.Glob(filepath.Join(root, "agents", "*.json"))
	if err != nil {
		return cat, err
	}
	for _, f := range agentFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			return cat, err
		}
		a, err := ParseAgent(strings.TrimSuffix(filepath.Base(f), ".json"), raw)
		if err != nil {
			return cat, err
		}
		cat.Agents = append(cat.Agents, a)
	}
	sort.Slice(cat.Skills, func(i, j int) bool { return cat.Skills[i].Name < cat.Skills[j].Name })
	sort.Slice(cat.Agents, func(i, j int) bool { return cat.Agents[i].Name < cat.Agents[j].Name })
	for i, a := range cat.Agents {
		s, ok := cat.skill(a.Skill)
		if !ok {
			return cat, fmt.Errorf("agente %s: skill %q não existe", a.Name, a.Skill)
		}
		cat.Agents[i].Version = agentVersion(a, s.Version)
	}
	return cat, nil
}

func (c Catalog) skill(name string) (Skill, bool) {
	for _, s := range c.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Spec resolve um agente do catálogo.
func (c Catalog) Spec(name string) (Spec, error) {
	for _, a := range c.Agents {
		if a.Name == name {
			s, _ := c.skill(a.Skill)
			return Spec{Agent: a.Name, AgentVersion: a.Version, Skill: s.Name, SkillVersion: s.Version,
				SkillContent: s.Content, Model: a.Model, BudgetUSD: a.BudgetUSD, Tools: a.Tools}, nil
		}
	}
	return Spec{}, fmt.Errorf("agente %q não existe no catálogo", name)
}

// ParseSkill lê o frontmatter (name e description) de uma SKILL.md.
func ParseSkill(dirName string, raw []byte) (Skill, error) {
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return Skill{}, fmt.Errorf("skill %s: SKILL.md sem frontmatter", dirName)
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return Skill{}, fmt.Errorf("skill %s: frontmatter sem fechamento", dirName)
	}
	s := Skill{Content: text, Version: Version(raw)}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "name":
			s.Name = strings.TrimSpace(v)
		case "description":
			s.Description = strings.TrimSpace(v)
		}
	}
	switch {
	case s.Name != dirName:
		return Skill{}, fmt.Errorf("skill %s: name %q diferente do diretório", dirName, s.Name)
	case !namePattern.MatchString(s.Name):
		return Skill{}, fmt.Errorf("skill %s: nome fora do formato", dirName)
	case s.Description == "":
		return Skill{}, fmt.Errorf("skill %s: description obrigatória", dirName)
	}
	return s, nil
}

// ParseAgent lê e valida um agents/<nome>.json.
func ParseAgent(fileName string, raw []byte) (Agent, error) {
	var a Agent
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return Agent{}, fmt.Errorf("agente %s: %w", fileName, err)
	}
	a.Version = ""
	switch {
	case a.Name != fileName:
		return Agent{}, fmt.Errorf("agente %s: name %q diferente do arquivo", fileName, a.Name)
	case !namePattern.MatchString(a.Name):
		return Agent{}, fmt.Errorf("agente %s: nome fora do formato", fileName)
	case strings.TrimSpace(a.Description) == "":
		return Agent{}, fmt.Errorf("agente %s: description obrigatória", fileName)
	case !namePattern.MatchString(a.Skill):
		return Agent{}, fmt.Errorf("agente %s: skill fora do formato", fileName)
	case !modelPattern.MatchString(a.Model):
		return Agent{}, fmt.Errorf("agente %s: model fora do formato", fileName)
	case a.BudgetUSD <= 0 || a.BudgetUSD > MaxBudgetUSD:
		return Agent{}, fmt.Errorf("agente %s: budget_usd deve ficar entre 0 e %.0f", fileName, MaxBudgetUSD)
	case len(a.Tools) == 0:
		return Agent{}, fmt.Errorf("agente %s: tools obrigatório", fileName)
	}
	// Um agente só restringe: nenhuma tool fora da lista que o worker permite.
	for _, t := range a.Tools {
		if !allowed(t) {
			return Agent{}, fmt.Errorf("agente %s: tool %q fora das permitidas pelo worker", fileName, t)
		}
	}
	return a, nil
}

func allowed(tool string) bool {
	for _, t := range agent.AllowedTools {
		if t == tool {
			return true
		}
	}
	return false
}

// Version é o hash curto de um conteúdo.
func Version(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func agentVersion(a Agent, skillVersion string) string {
	a.Version = ""
	raw, _ := json.Marshal(struct {
		Agent
		SkillVersion string
	}{a, skillVersion})
	return Version(raw)
}
