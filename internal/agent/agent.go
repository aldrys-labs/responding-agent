// Package agent orchestrates the agent runtime: it loads the check
// configuration (from a backend or a local file), schedules each check on its
// own interval, batches the results and pushes them to the backend, and sends
// periodic heartbeats. A config refresh reconciles the running checks against
// the latest configuration.
package agent

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/checks"
	"github.com/aldrys-labs/responding-agent/internal/config"
	"github.com/aldrys-labs/responding-agent/internal/dispatch"
	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// Tunables for the dispatch and liveness loops.
const (
	resultBufferSize  = 256
	resultBatchSize   = 50
	resultFlushPeriod = 5 * time.Second
	heartbeatPeriod   = 30 * time.Second
	minCheckInterval  = 1 * time.Second
	defaultCheckEvery = 30 * time.Second
)

// Agent ties together the check runner, the config source and the dispatch
// client.
type Agent struct {
	cfg      config.Config
	version  string
	hostname string
	log      *slog.Logger

	runner *checks.Runner
	client *dispatch.Client // nil when dispatch is disabled (dry run)
}

// New builds an Agent from its runtime configuration.
func New(cfg config.Config, version string, logger *slog.Logger) *Agent {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	a := &Agent{
		cfg:      cfg,
		version:  version,
		hostname: host,
		log:      logger,
		runner:   checks.NewRunner(),
	}
	if cfg.DispatchEnabled() {
		a.client = dispatch.NewClient(cfg.BackendURL, cfg.Token, version)
	}
	return a
}

// loadConfig returns the current check configuration, from the local file when
// one is set, otherwise pulled from the backend.
func (a *Agent) loadConfig(ctx context.Context) (protocol.ConfigResponse, error) {
	if a.cfg.ChecksFile != "" {
		return loadChecksFile(a.cfg.ChecksFile)
	}
	return a.client.FetchConfig(ctx)
}

// Run drives the agent until ctx is cancelled. It starts the dispatch and
// heartbeat loops, applies the initial configuration, then refreshes the
// configuration on each poll tick.
func (a *Agent) Run(ctx context.Context) error {
	results := make(chan protocol.Result, resultBufferSize)

	// The dispatch loop is awaited on shutdown so the final batch is flushed
	// before the process exits, rather than being dropped mid-flush.
	var dispatchDone sync.WaitGroup
	dispatchDone.Add(1)
	go func() {
		defer dispatchDone.Done()
		a.dispatchLoop(ctx, results)
	}()
	if a.client != nil {
		go a.heartbeatLoop(ctx)
	}

	refresh := time.NewTicker(time.Duration(a.cfg.PollIntervalSeconds) * time.Second)
	defer refresh.Stop()

	var genCancel context.CancelFunc

	apply := func() {
		cfg, err := a.loadConfig(ctx)
		if err != nil {
			a.log.Error("load config failed, keeping current checks", "err", err)
			return
		}
		if cfg.PollIntervalSeconds > 0 {
			refresh.Reset(time.Duration(cfg.PollIntervalSeconds) * time.Second)
		}
		if genCancel != nil {
			genCancel()
		}
		var genCtx context.Context
		genCtx, genCancel = context.WithCancel(ctx)
		for _, chk := range cfg.Checks {
			go a.runCheckLoop(genCtx, chk, results)
		}
		a.log.Info("configuration applied", "checks", len(cfg.Checks))
	}

	apply()
	for {
		select {
		case <-ctx.Done():
			// Stop the running checks, then wait for the dispatch loop to drain
			// and flush the buffered results before returning.
			if genCancel != nil {
				genCancel()
			}
			dispatchDone.Wait()
			return nil
		case <-refresh.C:
			apply()
		}
	}
}

// runCheckLoop runs one check immediately, then on its own interval, sending
// each result to out until ctx is cancelled.
func (a *Agent) runCheckLoop(ctx context.Context, chk protocol.Check, out chan<- protocol.Result) {
	interval := time.Duration(chk.IntervalSeconds) * time.Second
	switch {
	case chk.IntervalSeconds <= 0:
		interval = defaultCheckEvery
	case interval < minCheckInterval:
		interval = minCheckInterval
	}

	run := func() {
		res := a.runner.Run(ctx, chk)
		if res.Status != protocol.StatusUp {
			a.log.Warn("check not up", "id", chk.ID, "type", chk.Type, "status", res.Status, "latencyMs", res.LatencyMs, "err", res.Error)
		} else {
			a.log.Debug("check up", "id", chk.ID, "type", chk.Type, "latencyMs", res.LatencyMs)
		}
		select {
		case out <- res:
		case <-ctx.Done():
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// dispatchLoop batches results and flushes them to the backend by size or on a
// timer. When dispatch is disabled it drains the channel (results are already
// logged by the check loop).
func (a *Agent) dispatchLoop(ctx context.Context, in <-chan protocol.Result) {
	batch := make([]protocol.Result, 0, resultBatchSize)
	flush := time.NewTicker(resultFlushPeriod)
	defer flush.Stop()

	send := func() {
		if len(batch) == 0 || a.client == nil {
			batch = batch[:0]
			return
		}
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.client.PostResults(sendCtx, batch); err != nil {
			a.log.Error("post results failed, dropping batch", "count", len(batch), "err", err)
		} else {
			a.log.Debug("results posted", "count", len(batch))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Drain whatever results are already buffered, then flush, so a
			// clean shutdown does not lose the last observations.
			for {
				select {
				case res := <-in:
					batch = append(batch, res)
					if len(batch) >= resultBatchSize {
						send()
					}
				default:
					send()
					return
				}
			}
		case res := <-in:
			batch = append(batch, res)
			if len(batch) >= resultBatchSize {
				send()
			}
		case <-flush.C:
			send()
		}
	}
}

// heartbeatLoop sends a heartbeat immediately and then on a fixed period.
func (a *Agent) heartbeatLoop(ctx context.Context) {
	beat := func() {
		hbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := a.client.Heartbeat(hbCtx, a.hostname); err != nil {
			a.log.Warn("heartbeat failed", "err", err)
		}
	}
	beat()
	ticker := time.NewTicker(heartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}
