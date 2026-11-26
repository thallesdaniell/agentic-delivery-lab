// Package worker roda um card: clona o repo, roda o agente e grava o patch e o resultado.
// O worker não recebe nenhuma credencial do GitHub; quem publica é o publisher, depois.
package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/agent"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/gitops"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

// Config do worker.
type Config struct {
	CardPath  string
	SkillsDir string
	WorkDir   string
	OutDir    string
	Model     string
	BudgetUSD float64
	Timeout   time.Duration
}

// Run executa o card e sempre grava out/result.json, com sucesso ou com falha e código.
func Run(ctx context.Context, cfg Config) (run.Result, error) {
	start := time.Now()
	res := run.Result{Status: run.StatusSucceeded, Model: cfg.Model}
	if e := execute(ctx, cfg, &res); e != nil {
		res.Failed(e)
	}
	res.DurationSec = time.Since(start).Round(time.Second).Seconds()
	if err := res.Write(filepath.Join(cfg.OutDir, "result.json")); err != nil {
		return res, fmt.Errorf("gravando result.json: %w", err)
	}
	return res, nil
}

func execute(ctx context.Context, cfg Config, res *run.Result) *run.Error {
	c, err := card.Load(cfg.CardPath)
	if err != nil {
		return run.Fail(run.FailureCardInvalid, err.Error())
	}
	res.CardID, res.Branch = c.ID, c.Branch()

	skill, err := os.ReadFile(filepath.Join(cfg.SkillsDir, c.Skill, "SKILL.md"))
	if err != nil {
		return run.Fail(run.FailureCardInvalid, "skill "+c.Skill+" não encontrada")
	}

	repoDir := filepath.Join(cfg.WorkDir, "repo")
	g, err := gitops.Clone(ctx, c.Repo, repoDir)
	if err != nil {
		return run.Fail(run.FailureWorkspaceError, err.Error())
	}
	if _, err := os.Stat(filepath.Join(repoDir, "uv.lock")); err == nil {
		// Dependências instaladas antes do agente: ele roda os checks sem precisar de rede.
		sync := exec.CommandContext(ctx, "uv", "sync", "--frozen", "--quiet")
		sync.Dir = repoDir
		if out, err := sync.CombinedOutput(); err != nil {
			return run.Fail(run.FailureWorkspaceError, "uv sync: "+strings.TrimSpace(string(out)))
		}
	}

	actx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	out, rerr := agent.Run(actx, c, agent.Config{Model: cfg.Model, BudgetUSD: cfg.BudgetUSD, Workdir: repoDir, Skill: string(skill)})
	res.CostUSD, res.NumTurns, res.Denials = out.CostUSD, out.NumTurns, out.Denials
	if out.Handoff.Summary != "" {
		h := out.Handoff
		res.Handoff = &h
	}
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
	return nil
}
