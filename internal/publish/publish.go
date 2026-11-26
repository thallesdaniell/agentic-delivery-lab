// Package publish pega o resultado do worker e abre o PR como o GitHub App.
// Roda em outro container, depois que o agente terminou: o agente nunca vê o token.
package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/gitops"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
)

// Config do publisher.
type Config struct {
	CardPath       string
	OutDir         string
	WorkDir        string
	AppID          string
	InstallationID string
	KeyPath        string
	GitHub         githubapp.Client
}

// Run publica o resultado do worker. Só publica um run que terminou com sucesso.
func Run(ctx context.Context, cfg Config) (run.Result, error) {
	resPath := filepath.Join(cfg.OutDir, "result.json")
	res, err := run.ReadResult(resPath)
	if err != nil {
		return res, fmt.Errorf("lendo result.json: %w", err)
	}
	if res.Status != run.StatusSucceeded {
		return res, nil
	}
	if e := publish(ctx, cfg, &res); e != nil {
		res.Failed(e)
	}
	if err := res.Write(resPath); err != nil {
		return res, err
	}
	return res, nil
}

func publish(ctx context.Context, cfg Config, res *run.Result) *run.Error {
	c, err := card.Load(cfg.CardPath)
	if err != nil {
		return run.Fail(run.FailureCardInvalid, err.Error())
	}
	patch, err := os.ReadFile(filepath.Join(cfg.OutDir, "changes.patch"))
	if err != nil {
		return run.Fail(run.FailurePublishError, "changes.patch ausente")
	}

	repoDir := filepath.Join(cfg.WorkDir, "publish")
	g, err := gitops.Clone(ctx, c.Repo, repoDir)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	files, err := g.Apply(ctx, patch)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	// Segunda barreira: o publisher confere de novo os caminhos antes de usar a credencial.
	if bad := gitops.ForbiddenPaths(files); len(bad) > 0 {
		return run.Fail(run.FailureForbiddenPath, "patch altera caminhos proibidos: "+strings.Join(bad, ", "))
	}

	keyPEM, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return run.Fail(run.FailurePublishError, "chave do GitHub App ilegível")
	}
	key, err := githubapp.ParseKey(keyPEM)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	jwt, err := githubapp.JWT(cfg.AppID, key, time.Now())
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	name, email, err := cfg.GitHub.BotIdentity(ctx, jwt)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	token, err := cfg.GitHub.InstallationToken(ctx, jwt, cfg.InstallationID)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	defer func() { _ = cfg.GitHub.RevokeToken(context.Background(), token) }()

	if err := g.Commit(ctx, res.Branch, name, email, CommitMessage(c, *res)); err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	if err := g.Push(ctx, res.Branch, token); err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	base, err := cfg.GitHub.DefaultBranch(ctx, token, c.Repo)
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	pr, err := cfg.GitHub.CreatePullRequest(ctx, token, c.Repo, res.Branch, base, "["+c.ID+"] "+c.Title, PRBody(c, *res))
	if err != nil {
		return run.Fail(run.FailurePublishError, err.Error())
	}
	res.PRNumber, res.PRURL = pr.Number, pr.HTMLURL
	return nil
}

// CommitMessage é a mensagem do commit do agente.
func CommitMessage(c card.Card, res run.Result) string {
	return fmt.Sprintf("[%s] %s\n\nGerado pelo agentic-delivery-lab (modelo %s).", c.ID, c.Title, res.Model)
}

// PRBody monta a descrição do PR a partir do card e do handoff.
func PRBody(c card.Card, res run.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Card\n\n**%s**: %s\n\n%s\n\n**Critérios de aceite**\n\n", c.ID, c.Title, c.Objective)
	for _, ac := range c.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", ac)
	}
	if h := res.Handoff; h != nil {
		fmt.Fprintf(&b, "\n## O que o agente fez\n\n%s\n\n## Verificação\n\n", h.Summary)
		for _, t := range h.TestsRun {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		if len(h.Limitations) > 0 {
			b.WriteString("\n## Limitações\n\n")
			for _, l := range h.Limitations {
				fmt.Fprintf(&b, "- %s\n", l)
			}
		}
	}
	fmt.Fprintf(&b, "\n---\n\nRun do agente: modelo `%s`, %d turnos, custo estimado US$ %.2f, %.0f s. O merge é de uma pessoa.\n",
		res.Model, res.NumTurns, res.CostUSD, res.DurationSec)
	return b.String()
}
