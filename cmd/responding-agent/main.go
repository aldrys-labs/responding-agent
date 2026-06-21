// Command responding-agent runs the respondi.ng monitoring agent: it loads its
// check configuration, runs the checks on their schedules and pushes the
// results to a respondi.ng backend (or runs against a local checks file).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aldrys-labs/responding-agent/internal/agent"
	"github.com/aldrys-labs/responding-agent/internal/config"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
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
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.New(cfg, version, logger).Run(ctx); err != nil && err != context.Canceled {
		logger.Error("agent stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
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
