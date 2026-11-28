// Comando adl: o binário do agentic-delivery-lab.
//
//	adl migrate                     aplica as migrations no Postgres
//	adl catalog sync                copia agents/ e skills/ do repositório para o banco
//	adl card create <card.json>     grava um card no backlog
//	adl card assign <id> <agente>   atribui um agente do catálogo ao card
//	adl card ready <id>             move o card para Pronto e enfileira um run
//	adl card list                   lista os cards com o último run
//	adl board                       sobe o board web (só fala com o Postgres)
//	adl orchestrator                tira runs da fila, roda o worker, publica e acompanha os PRs
//	adl worker                      roda o agente para um card (sem credencial do GitHub)
//	adl publish                     abre o PR do resultado do worker, como o GitHub App
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/board"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/card"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/catalog"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/dockerapi"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/orchestrator"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/publish"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/review"
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
	case "board":
		err = cmdBoard(ctx)
	case "catalog":
		err = cmdCatalog(ctx, os.Args[2:])
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
	// O agente congelado no run também chega por variável de ambiente. Sem ele, vale o catálogo da imagem.
	var spec *catalog.Spec
	if raw := os.Getenv("ADL_AGENT_SPEC"); raw != "" {
		spec = &catalog.Spec{}
		if err := json.Unmarshal([]byte(raw), spec); err != nil {
			return fmt.Errorf("ADL_AGENT_SPEC inválido: %w", err)
		}
	}
	// O tipo do run (implementar ou corrigir o PR) e a entrada da correção. Sem ela, o run implementa o card.
	task := run.Task{Kind: run.KindImplement}
	if raw := os.Getenv("ADL_RUN_TASK"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &task); err != nil || !task.Kind.Valid() {
			return fmt.Errorf("ADL_RUN_TASK inválido: %v", err)
		}
	}
	outDir := env("ADL_OUT_DIR", "/out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	res, err := worker.Run(ctx, worker.Config{
		CardPath:     cardPath,
		Spec:         spec,
		Task:         task,
		CatalogDir:   env("ADL_CATALOG_DIR", "/lab"),
		WorkDir:      env("ADL_WORK_DIR", "/home/agent/work"),
		OutDir:       outDir,
		BudgetCapUSD: envFloat("RUN_BUDGET_USD", 1.00),
		Timeout:      envDuration("RUN_TIMEOUT", 20*time.Minute),
	})
	if err != nil {
		return err
	}
	return printResult(res)
}

func publishConfig(cardPath, outDir string, task run.Task) publish.Config {
	return publish.Config{
		Task:           task,
		CardPath:       cardPath,
		OutDir:         outDir,
		WorkDir:        env("ADL_WORK_DIR", os.TempDir()),
		AppID:          must("GITHUB_APP_ID"),
		InstallationID: must("GITHUB_APP_INSTALLATION_ID"),
		KeyPath:        env("GITHUB_APP_PRIVATE_KEY_PATH", "/run/secrets/github-app.pem"),
	}
}

func cmdPublish(ctx context.Context) error {
	res, err := publish.Run(ctx, publishConfig(env("ADL_CARD", "/lab/card.json"), env("ADL_OUT_DIR", "/out"), run.Task{Kind: run.KindImplement}))
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

// syncCatalog lê o catálogo da imagem e copia para o banco.
func syncCatalog(ctx context.Context, s *store.Store) (catalog.Catalog, error) {
	cat, err := catalog.Load(env("ADL_CATALOG_DIR", "/lab"))
	if err != nil {
		return cat, fmt.Errorf("catálogo: %w", err)
	}
	return cat, s.SyncCatalog(ctx, cat)
}

func cmdCatalog(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		usage()
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return err
	}
	cat, err := syncCatalog(ctx, s)
	if err != nil {
		return err
	}
	for _, a := range cat.Agents {
		fmt.Printf("agente %s %s (skill %s, %s, até US$ %.2f)\n", a.Name, a.Version, a.Skill, a.Model, a.BudgetUSD)
	}
	return nil
}

// cmdBoard sobe o board. Ele só fala com o Postgres: sem socket do Docker e sem segredo.
func cmdBoard(ctx context.Context) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return err
	}
	if _, err := syncCatalog(ctx, s); err != nil {
		return err
	}
	b, err := board.New(s, env("TARGET_REPO", ""), log)
	if err != nil {
		return err
	}
	addr := env("BOARD_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: b.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("board no ar", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
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
	case args[0] == "assign" && len(args) == 3:
		if err := s.AssignAgent(ctx, args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("card %s com o agente %s\n", args[1], args[2])
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
		fmt.Fprintln(w, "CARD\tSTATUS\tAGENTE\tRUN\tFALHA\tCUSTO\tPR\tTÍTULO")
		for _, v := range cards {
			agent := v.Agent
			if agent == "" {
				agent = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Status, agent, deref(v.RunID, v.RunStatus), str(v.FailureCode), money(v.CostUSD), str(v.PRURL), v.Title)
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
	cat, err := syncCatalog(ctx, s)
	if err != nil {
		return err
	}
	log.Info("catálogo sincronizado", "agents", len(cat.Agents), "skills", len(cat.Skills))
	if n, err := s.RecoverInterrupted(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("runs interrompidos por reinício encerrados", "count", n)
	}
	timeout := envDuration("RUN_TIMEOUT", 20*time.Minute)
	pub := func(ctx context.Context, cardPath, outDir string, task run.Task) (run.Result, error) {
		return publish.Run(ctx, publishConfig(cardPath, outDir, task))
	}
	o := &orchestrator.Orchestrator{
		Store: s,
		Executor: orchestrator.DockerExecutor{
			Docker: dockerapi.New(os.Getenv("DOCKER_SOCKET")),
			Image:  env("WORKER_IMAGE", "agentic-delivery-lab:dev"),
			Env: []string{
				"ANTHROPIC_API_KEY=" + must("ANTHROPIC_API_KEY"),
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
	// O observador acompanha os PRs dos cards em revisão: CI, revisões e merge.
	tokens, err := appTokens()
	if err != nil {
		return err
	}
	defer tokens.Revoke(context.WithoutCancel(ctx))
	w := &review.Watcher{GitHub: githubapp.Client{}, Store: s, Token: tokens.Token, Log: log.With("component", "review")}
	reviewEvery := envDuration("REVIEW_POLL_INTERVAL", 30*time.Second)
	go w.Loop(ctx, reviewEvery)
	log.Info("orquestrador no ar", "worker_image", env("WORKER_IMAGE", "agentic-delivery-lab:dev"), "review_poll", reviewEvery.String())
	return o.Loop(ctx, envDuration("POLL_INTERVAL", 3*time.Second))
}

// appTokens prepara os tokens de instalação do GitHub App para o observador de PRs.
func appTokens() (*githubapp.TokenSource, error) {
	keyPEM, err := os.ReadFile(env("GITHUB_APP_PRIVATE_KEY_PATH", "/run/secrets/github-app.pem"))
	if err != nil {
		return nil, fmt.Errorf("chave do GitHub App: %w", err)
	}
	key, err := githubapp.ParseKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &githubapp.TokenSource{AppID: must("GITHUB_APP_ID"), InstallationID: must("GITHUB_APP_INSTALLATION_ID"), Key: key}, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
uso:
  adl migrate
  adl catalog sync
  adl card create <card.json> | assign <id> <agente> | ready <id> | list
  adl board
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
