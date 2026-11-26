// Comando adl: o binário do agentic-delivery-lab.
//
//	adl worker   roda o agente para um card (sem credencial do GitHub)
//	adl publish  abre o PR do resultado do worker, como o GitHub App
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/publish"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		res run.Result
		err error
	)
	switch os.Args[1] {
	case "worker":
		res, err = worker.Run(ctx, worker.Config{
			CardPath:  env("ADL_CARD", "/lab/card.json"),
			SkillsDir: env("ADL_SKILLS_DIR", "/lab/skills"),
			WorkDir:   env("ADL_WORK_DIR", "/home/agent/work"),
			OutDir:    env("ADL_OUT_DIR", "/out"),
			Model:     env("AGENT_MODEL", "claude-sonnet-5-5"),
			BudgetUSD: envFloat("RUN_BUDGET_USD", 1.00),
			Timeout:   envDuration("RUN_TIMEOUT", 20*time.Minute),
		})
	case "publish":
		res, err = publish.Run(ctx, publish.Config{
			CardPath:       env("ADL_CARD", "/lab/card.json"),
			OutDir:         env("ADL_OUT_DIR", "/out"),
			WorkDir:        env("ADL_WORK_DIR", "/home/agent/work"),
			AppID:          must("GITHUB_APP_ID"),
			InstallationID: must("GITHUB_APP_INSTALLATION_ID"),
			KeyPath:        env("GITHUB_APP_PRIVATE_KEY_PATH", "/run/secrets/github-app.pem"),
		})
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "adl:", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	if res.Status != run.StatusSucceeded {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "uso: adl worker | adl publish")
	os.Exit(2)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "adl: variável %s obrigatória\n", k)
		os.Exit(2)
	}
	return v
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil && v > 0 {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
