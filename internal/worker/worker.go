// Package worker roda um card: clona o repo, roda o agente e grava o patch e o resultado.
// O worker não recebe nenhuma credencial do GitHub; quem publica é o publisher, depois.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/agent"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/gitops"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

// Config do worker.
type Config struct {
	CardPath string
	// Spec é o agente congelado no run. Sem ele (run avulso), o agente do card é lido de CatalogDir.
	Spec       *catalog.Spec
	CatalogDir string
	WorkDir    string
	OutDir     string
	// BudgetCapUSD é o teto global: o agente nunca gasta mais que o menor entre o dele e este.
	BudgetCapUSD float64
	Timeout      time.Duration
	Log          *slog.Logger
}

// Run executa o card e sempre grava out/result.json, com sucesso ou com falha e código.
func Run(ctx context.Context, cfg Config) (run.Result, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	start := time.Now()
	res := run.Result{Status: run.StatusSucceeded}
	if e := execute(ctx, cfg, &res); e != nil {
		res.Failed(e)
	}
	res.DurationSec = time.Since(start).Round(time.Second).Seconds()
	cfg.Log.Info("worker terminou", "status", res.Status, "failure_code", res.FailureCode, "cost_usd", res.CostUSD)
	if err := res.Write(filepath.Join(cfg.OutDir, "result.json")); err != nil {
		return res, fmt.Errorf("gravando result.json: %w", err)
	}
	return res, nil
}

func resolveSpec(cfg Config, c card.Card) (catalog.Spec, *run.Error) {
	if cfg.Spec != nil {
		return *cfg.Spec, nil
	}
	if c.Agent == "" {
		return catalog.Spec{}, run.Fail(run.FailureCardInvalid, "o card não tem agente atribuído")
	}
	cat, err := catalog.Load(cfg.CatalogDir)
	if err != nil {
		return catalog.Spec{}, run.Fail(run.FailureCardInvalid, "catálogo inválido: "+err.Error())
	}
	spec, err := cat.Spec(c.Agent)
	if err != nil {
		return catalog.Spec{}, run.Fail(run.FailureCardInvalid, err.Error())
	}
	return spec, nil
}

func execute(ctx context.Context, cfg Config, res *run.Result) *run.Error {
	log := cfg.Log
	c, err := card.Load(cfg.CardPath)
	if err != nil {
		return run.Fail(run.FailureCardInvalid, err.Error())
	}
	res.CardID, res.Branch = c.ID, c.Branch()
	spec, e := resolveSpec(cfg, c)
	if e != nil {
		return e
	}
	res.Model = spec.Model
	budget := spec.BudgetUSD
	if cfg.BudgetCapUSD > 0 && cfg.BudgetCapUSD < budget {
		budget = cfg.BudgetCapUSD
	}
	log.Info("agente", "agent", spec.Agent, "agent_version", spec.AgentVersion, "skill", spec.Skill,
		"skill_version", spec.SkillVersion, "model", spec.Model, "budget_usd", budget)

	log.Info("clonando", "repo", c.Repo)
	repoDir := filepath.Join(cfg.WorkDir, "repo")
	g, err := gitops.Clone(ctx, c.Repo, repoDir)
	if err != nil {
		return run.Fail(run.FailureWorkspaceError, err.Error())
	}
	if _, err := os.Stat(filepath.Join(repoDir, "uv.lock")); err == nil {
		// Dependências instaladas antes do agente: ele roda os checks sem precisar de rede.
		log.Info("instalando dependências (uv sync)")
		sync := exec.CommandContext(ctx, "uv", "sync", "--frozen", "--quiet")
		sync.Dir = repoDir
		if out, err := sync.CombinedOutput(); err != nil {
			return run.Fail(run.FailureWorkspaceError, "uv sync: "+strings.TrimSpace(string(out)))
		}
	}

	log.Info("agente trabalhando", "timeout", cfg.Timeout.String())
	actx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	out, rerr := agent.Run(actx, c, agent.Config{Model: spec.Model, BudgetUSD: budget, Workdir: repoDir,
		Skill: spec.SkillContent, Tools: spec.Tools})
	res.CostUSD, res.NumTurns, res.Denials = out.CostUSD, out.NumTurns, out.Denials
	if out.Handoff.Summary != "" {
		h := out.Handoff
		res.Handoff = &h
	}
	log.Info("agente terminou", "turns", out.NumTurns, "cost_usd", out.CostUSD, "denials", len(out.Denials))
	if rerr != nil {
		return rerr
	}

	patch, files, err := g.StagedPatch(ctx)
	if err != nil {
		return run.Fail(run.FailureWorkspaceError, err.Error())
	}
	res.ChangedFiles = files
	if len(files) == 0 {
		return run.Fail(run.FailureNoChanges, "o agente declarou done sem alterar nenhum arquivo")
	}
	if bad := gitops.ForbiddenPaths(files); len(bad) > 0 {
		return run.Fail(run.FailureForbiddenPath, "o agente alterou caminhos proibidos: "+strings.Join(bad, ", "))
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "changes.patch"), patch, 0o644); err != nil {
		return run.Fail(run.FailureWorkspaceError, err.Error())
	}
	log.Info("patch gravado", "files", len(files))
	return nil
}
