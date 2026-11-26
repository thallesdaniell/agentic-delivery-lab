// Comando adl: o binário do agentic-delivery-lab.
//
//	adl migrate                     aplica as migrations no Postgres
//	adl card create <card.json>     grava um card no backlog
//	adl card ready <id>             move o card para Pronto e enfileira um run
//	adl card list                   lista os cards com o último run
//	adl orchestrator                tira runs da fila, roda o worker e publica
//	adl worker                      roda o agente para um card (sem credencial do GitHub)
//	adl publish                     abre o PR do resultado do worker, como o GitHub App
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/dockerapi"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/orchestrator"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/publish"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "worker":
		err = cmdWorker(ctx)
	case "publish":
		err = cmdPublish(ctx)
	case "migrate":
		err = cmdMigrate(ctx)
	case "card":
		err = cmdCard(ctx, os.Args[2:])
	case "orchestrator":
		err = cmdOrchestrator(ctx)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "adl:", err)
		if _, ok := err.(runFailed); ok {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

type runFailed struct{ code run.FailureCode }

func (r runFailed) Error() string { return "run falhou: " + string(r.code) }

func printResult(res run.Result) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	if res.Status != run.StatusSucceeded {
		return runFailed{res.FailureCode}
	}
	return nil
}

func cmdWorker(ctx context.Context) error {
	cardPath := env("ADL_CARD", "/lab/card.json")
	// No orquestrador o card chega por variável de ambiente: o container não tem bind mount.
	if raw := os.Getenv("ADL_CARD_JSON"); raw != "" {
		cardPath = filepath.Join(os.TempDir(), "card.json")
		if err := os.WriteFile(cardPath, []byte(raw), 0o600); err != nil {
			return err
		}
	}
	outDir := env("ADL_OUT_DIR", "/out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	res, err := worker.Run(ctx, worker.Config{
		CardPath:  cardPath,
		SkillsDir: env("ADL_SKILLS_DIR", "/lab/skills"),
		WorkDir:   env("ADL_WORK_DIR", "/home/agent/work"),
		OutDir:    outDir,
		Model:     env("AGENT_MODEL", "claude-sonnet-5-5"),
		BudgetUSD: envFloat("RUN_BUDGET_USD", 1.00),
		Timeout:   envDuration("RUN_TIMEOUT", 20*time.Minute),
	})
	if err != nil {
		return err
	}
	return printResult(res)
}

func publishConfig(cardPath, outDir string) publish.Config {
	return publish.Config{
		CardPath:       cardPath,
		OutDir:         outDir,
		WorkDir:        env("ADL_WORK_DIR", os.TempDir()),
		AppID:          must("GITHUB_APP_ID"),
		InstallationID: must("GITHUB_APP_INSTALLATION_ID"),
		KeyPath:        env("GITHUB_APP_PRIVATE_KEY_PATH", "/run/secrets/github-app.pem"),
	}
}

func cmdPublish(ctx context.Context) error {
	res, err := publish.Run(ctx, publishConfig(env("ADL_CARD", "/lab/card.json"), env("ADL_OUT_DIR", "/out")))
	if err != nil {
		return err
	}
	return printResult(res)
}

func openStore(ctx context.Context) (*store.Store, error) {
	return store.Open(ctx, must("DATABASE_URL"))
}

func cmdMigrate(ctx context.Context) error {
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	applied, err := s.Migrate(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("migrations aplicadas: %d %v\n", len(applied), applied)
	return nil
}

func cmdCard(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch {
	case args[0] == "create" && len(args) == 2:
		c, err := card.Load(args[1])
		if err != nil {
			return err
		}
		if err := s.CreateCard(ctx, c); err != nil {
			return err
		}
		fmt.Printf("card %s criado no backlog\n", c.ID)
	case args[0] == "ready" && len(args) == 2:
		runID, err := s.MarkReady(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("card %s em Pronto; run %d na fila\n", args[1], runID)
	case args[0] == "list":
		cards, err := s.ListCards(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CARD\tSTATUS\tRUN\tFALHA\tCUSTO\tPR\tTÍTULO")
		for _, v := range cards {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Status, deref(v.RunID, v.RunStatus), str(v.FailureCode), money(v.CostUSD), str(v.PRURL), v.Title)
		}
		return w.Flush()
	default:
		usage()
	}
	return nil
}

func cmdOrchestrator(ctx context.Context) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if applied, err := s.Migrate(ctx); err != nil {
		return err
	} else if len(applied) > 0 {
		log.Info("migrations aplicadas", "names", applied)
	}
	if n, err := s.RecoverInterrupted(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("runs interrompidos por reinício encerrados", "count", n)
	}
	timeout := envDuration("RUN_TIMEOUT", 20*time.Minute)
	pub := func(ctx context.Context, cardPath, outDir string) (run.Result, error) {
		return publish.Run(ctx, publishConfig(cardPath, outDir))
	}
	o := &orchestrator.Orchestrator{
		Store: s,
		Executor: orchestrator.DockerExecutor{
			Docker: dockerapi.New(os.Getenv("DOCKER_SOCKET")),
			Image:  env("WORKER_IMAGE", "agentic-delivery-lab:dev"),
			Env: []string{
				"ANTHROPIC_API_KEY=" + must("ANTHROPIC_API_KEY"),
				"AGENT_MODEL=" + env("AGENT_MODEL", "claude-sonnet-5-5"),
				"RUN_BUDGET_USD=" + env("RUN_BUDGET_USD", "1.00"),
				"RUN_TIMEOUT=" + timeout.String(),
			},
		},
		Publish: pub,
		WorkDir: os.TempDir(),
		// Margem para clone e instalação de dependências, além do tempo do agente.
		Timeout: timeout + 5*time.Minute,
		Log:     log,
	}
	log.Info("orquestrador no ar", "worker_image", env("WORKER_IMAGE", "agentic-delivery-lab:dev"))
	return o.Loop(ctx, envDuration("POLL_INTERVAL", 3*time.Second))
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
uso:
  adl migrate
  adl card create <card.json> | ready <id> | list
  adl orchestrator
  adl worker | adl publish`))
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

func str(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

func money(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("US$ %.2f", *p)
}

func deref(id *int64, status *string) string {
	if id == nil {
		return "-"
	}
	return fmt.Sprintf("#%d %s", *id, str(status))
}
