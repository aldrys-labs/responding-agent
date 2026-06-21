// Command responding-agent runs the respondi.ng monitoring agent: it loads its
// check configuration, runs the checks on their schedules and pushes the
// results to a respondi.ng backend (or runs against a local checks file).
//
// The "healthcheck" subcommand probes a running agent's local health endpoint
// and exits 0 (healthy) or 1; it backs the container HEALTHCHECK.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/agent"
	"github.com/aldrys-labs/responding-agent/internal/config"
	"github.com/aldrys-labs/responding-agent/internal/metrics"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Subcommand: container/orchestrator health probe.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck(os.Args[2:]))
	}

	var (
		checksFile  = flag.String("checks", "", "path to a local checks JSON file (overrides RESPONDING_CHECKS_FILE)")
		logLevel    = flag.String("log-level", "info", "log level: debug, info, warn, error")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("responding-agent", version)
		return
	}

	logger := newLogger(*logLevel)

	cfg, err := config.Load(*checksFile)
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	mode := "dispatch"
	if !cfg.DispatchEnabled() {
		mode = "dry-run (results logged, not dispatched)"
	}
	logger.Info("starting responding-agent",
		"version", version,
		"mode", mode,
		"backend", cfg.BackendURL,
		"checksFile", cfg.ChecksFile,
		"spoolDir", cfg.SpoolDir,
		"healthAddr", cfg.HealthAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	m := metrics.New(time.Now().Unix())
	ag, err := agent.New(cfg, version, logger, m)
	if err != nil {
		logger.Error("could not start agent", "err", err)
		os.Exit(1)
	}

	// SIGHUP triggers an out-of-band config reload.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			ag.Reload()
		}
	}()

	if err := ag.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("agent stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}

// runHealthcheck probes the local health endpoint and returns a process exit
// code: 0 when /healthz answers 200, 1 otherwise.
func runHealthcheck(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	addr := fs.String("addr", "", "health server address (default: RESPONDING_HEALTH_ADDR or :9090)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	target := *addr
	if target == "" {
		target = os.Getenv("RESPONDING_HEALTH_ADDR")
	}
	if target == "" {
		target = ":9090"
	}
	// A bare ":port" means localhost from the probe's point of view.
	if strings.HasPrefix(target, ":") {
		target = "127.0.0.1" + target
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + target + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.Status)
		return 1
	}
	return 0
}

// newLogger builds a structured logger at the requested level.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
