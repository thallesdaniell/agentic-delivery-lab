package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const skillMD = "---\nname: python-feature\ndescription: Funcionalidade em Python.\n---\n\n# Corpo\n"

const agentJSON = `{"name": "dev", "description": "d", "skill": "python-feature", "model": "claude-sonnet-5-5",
 "budget_usd": 1, "tools": ["Read", "Edit"]}`

func writeCatalog(t *testing.T, skill, agent string) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "skills", "python-feature"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "agents"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "skills", "python-feature", "SKILL.md"), []byte(skill), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "agents", "dev.json"), []byte(agent), 0o644))
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadResolvesAgentWithSkill(t *testing.T) {
	cat, err := Load(writeCatalog(t, skillMD, agentJSON))
	must(t, err)
	spec, err := cat.Spec("dev")
	must(t, err)
	if spec.Skill != "python-feature" || spec.SkillContent != skillMD || spec.Model != "claude-sonnet-5-5" || len(spec.Tools) != 2 {
		t.Fatalf("spec: %+v", spec)
	}
	if spec.AgentVersion == "" || spec.SkillVersion == "" {
		t.Fatal("versões vazias")
	}
	if _, err := cat.Spec("outro"); err == nil {
		t.Fatal("agente inexistente deveria falhar")
	}
}

// Mudar a skill muda a versão da skill e a do agente que a usa.
func TestVersionFollowsContent(t *testing.T) {
	a, err := Load(writeCatalog(t, skillMD, agentJSON))
	must(t, err)
	b, err := Load(writeCatalog(t, skillMD+"\nmais uma regra\n", agentJSON))
	must(t, err)
	if a.Skills[0].Version == b.Skills[0].Version || a.Agents[0].Version == b.Agents[0].Version {
		t.Fatal("a versão deveria mudar com o conteúdo da skill")
	}
	c, err := Load(writeCatalog(t, skillMD, agentJSON))
	must(t, err)
	if a.Agents[0].Version != c.Agents[0].Version {
		t.Fatal("mesmo conteúdo, mesma versão")
	}
}

func TestRejectsInvalidCatalog(t *testing.T) {
	cases := map[string][2]string{
		"tool fora do worker":   {skillMD, strings.Replace(agentJSON, `"Edit"`, `"Bash(curl:*)"`, 1)},
		"skill inexistente":     {skillMD, strings.Replace(agentJSON, `"python-feature"`, `"go-feature"`, 1)},
		"nome difere do arq.":   {skillMD, strings.Replace(agentJSON, `"dev"`, `"dev2"`, 1)},
		"teto acima do máximo":  {skillMD, strings.Replace(agentJSON, `"budget_usd": 1`, `"budget_usd": 50`, 1)},
		"campo desconhecido":    {skillMD, strings.Replace(agentJSON, `"name"`, `"x": 1, "name"`, 1)},
		"skill sem frontmatter": {"# só corpo\n", agentJSON},
		"skill nome errado":     {strings.Replace(skillMD, "name: python-feature", "name: outra", 1), agentJSON},
	}
	for name, c := range cases {
		if _, err := Load(writeCatalog(t, c[0], c[1])); err == nil {
			t.Errorf("%s: deveria recusar", name)
		}
	}
}

// O catálogo versionado no repositório precisa carregar.
func TestRepositoryCatalogLoads(t *testing.T) {
	cat, err := Load("../..")
	must(t, err)
	if len(cat.Agents) == 0 || len(cat.Skills) == 0 {
		t.Fatalf("catálogo vazio: %+v", cat)
	}
}
