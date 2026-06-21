// Package agent orchestrates the agent runtime: it loads the check
// configuration (from a backend or a local file, with an on-disk cache),
// schedules each check on its own interval, batches the results and pushes them
// to the backend (spooling on failure), and sends periodic heartbeats. A config
// refresh reconciles the running checks against the latest configuration.
package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/checks"
	"github.com/aldrys-labs/responding-agent/internal/config"
	"github.com/aldrys-labs/responding-agent/internal/dispatch"
	"github.com/aldrys-labs/responding-agent/internal/fsutil"
	"github.com/aldrys-labs/responding-agent/internal/health"
	"github.com/aldrys-labs/responding-agent/internal/metrics"
	"github.com/aldrys-labs/responding-agent/internal/protocol"
	"github.com/aldrys-labs/responding-agent/internal/spool"
)

// Tunables for the dispatch and liveness loops.
const (
	resultBufferSize  = 256
	resultBatchSize   = 50
	resultFlushPeriod = 5 * time.Second
	heartbeatPeriod   = 30 * time.Second
	dispatchTimeout   = 30 * time.Second
	minCheckInterval  = 1 * time.Second
	defaultCheckEvery = 30 * time.Second
	maxStartupJitter  = 3 * time.Second
	configCacheName   = "config-cache.json"
)

// Agent ties together the check runner, the config source, the dispatch client,
// the result spool and the metrics.
type Agent struct {
	cfg      config.Config
	version  string
	hostname string
	log      *slog.Logger

	runner  *checks.Runner
	client  *dispatch.Client // nil when dispatch is disabled (dry run)
	spool   *spool.Spool
	metrics *metrics.Metrics

	sem      chan struct{} // bounds concurrent in-flight checks
	reloadCh chan struct{} // SIGHUP-driven config reload
}

// New builds an Agent from its runtime configuration.
func New(cfg config.Config, version string, logger *slog.Logger, m *metrics.Metrics) (*Agent, error) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	sp, err := spool.Open(cfg.SpoolDir, cfg.SpoolMaxBatches, func() int64 { return time.Now().UnixNano() })
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg:      cfg,
		version:  version,
		hostname: host,
		log:      logger,
		runner:   checks.NewRunner(),
		spool:    sp,
		metrics:  m,
		sem:      make(chan struct{}, cfg.MaxConcurrentChecks),
		reloadCh: make(chan struct{}, 1),
	}
	if cfg.DispatchEnabled() {
		a.client = dispatch.NewClient(cfg.BackendURL, cfg.Token, version)
	}
	a.metrics.SpoolDepth.Store(int64(sp.Depth()))
	return a, nil
}

// Reload requests an out-of-band configuration refresh (wired to SIGHUP). It
// never blocks: a refresh is already pending if the channel is full.
func (a *Agent) Reload() {
	select {
	case a.reloadCh <- struct{}{}:
	default:
	}
}

// loadConfig returns the current check configuration, from the local file when
// one is set, otherwise pulled from the backend. Invalid checks are dropped.
// A config pulled from the backend is written through to the on-disk cache, so
// caching is a property of the source rather than something the caller decides.
func (a *Agent) loadConfig(ctx context.Context) (protocol.ConfigResponse, error) {
	if a.cfg.ChecksFile != "" {
		cfg, err := loadChecksFile(a.cfg.ChecksFile)
		if err != nil {
			return protocol.ConfigResponse{}, err
		}
		cfg.Checks = a.validateChecks(cfg.Checks)
		return cfg, nil
	}

	cfg, err := a.client.FetchConfig(ctx)
	if err != nil {
		return protocol.ConfigResponse{}, err
	}
	cfg.Checks = a.validateChecks(cfg.Checks)
	a.saveCachedConfig(cfg)
	return cfg, nil
}

// validateChecks keeps only well-formed checks, logging the ones it drops.
func (a *Agent) validateChecks(in []protocol.Check) []protocol.Check {
	out := in[:0:0]
	for _, c := range in {
		if err := c.Validate(); err != nil {
			a.log.Warn("skipping invalid check", "err", err)
			continue
		}
		out = append(out, c)
	}
	return out
}

// Run drives the agent until ctx is cancelled. It starts the health, dispatch
// and heartbeat loops, applies the initial configuration, then refreshes it on
// each poll tick and on demand (SIGHUP).
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.HealthAddr != "" {
		go func() { _ = health.Serve(ctx, a.cfg.HealthAddr, a.metrics, a.log) }()
	}

	results := make(chan protocol.Result, resultBufferSize)

	// The dispatch loop is awaited on shutdown so the final batch is flushed (or
	// spooled) before the process exits, rather than being dropped mid-flush.
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
			a.metrics.ConfigFailures.Add(1)
			if cached, ok := a.loadCachedConfig(); ok && !a.metrics.Ready() {
				a.log.Warn("config load failed at startup, using cached config", "err", err)
				cfg = cached
				a.metrics.UsingCache.Store(true)
			} else {
				a.log.Error("load config failed, keeping current checks", "err", err)
				return
			}
		} else {
			a.metrics.UsingCache.Store(false)
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
		a.metrics.ChecksConfigured.Store(int64(len(cfg.Checks)))
		a.metrics.ConfigReloads.Add(1)
		a.log.Info("configuration applied", "checks", len(cfg.Checks))
	}

	apply()
	for {
		select {
		case <-ctx.Done():
			if genCancel != nil {
				genCancel()
			}
			dispatchDone.Wait()
			return nil
		case <-refresh.C:
			apply()
		case <-a.reloadCh:
			a.log.Info("reloading configuration on request")
			apply()
		}
	}
}

// runCheckLoop runs one check after a small startup jitter, then on its own
// interval, sending each result to out until ctx is cancelled. A concurrency
// semaphore bounds how many checks execute at once.
func (a *Agent) runCheckLoop(ctx context.Context, chk protocol.Check, out chan<- protocol.Result) {
	interval := time.Duration(chk.IntervalSeconds) * time.Second
	switch {
	case chk.IntervalSeconds <= 0:
		interval = defaultCheckEvery
	case interval < minCheckInterval:
		interval = minCheckInterval
	}

	// Spread first runs so a large config does not stampede the network.
	if j := jitterUpTo(interval); j > 0 {
		select {
		case <-time.After(j):
		case <-ctx.Done():
			return
		}
	}

	run := func() {
		select {
		case a.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		res := a.runner.Run(ctx, chk)
		<-a.sem

		a.metrics.RecordResult(res.Status)
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
// timer. Undelivered batches are spooled and replayed on the next flush. When
// dispatch is disabled it drains the channel (results are already logged).
func (a *Agent) dispatchLoop(ctx context.Context, in <-chan protocol.Result) {
	batch := make([]protocol.Result, 0, resultBatchSize)
	flush := time.NewTicker(resultFlushPeriod)
	defer flush.Stop()

	send := func() {
		pending := batch
		batch = make([]protocol.Result, 0, resultBatchSize)
		if a.client == nil {
			return // dry run: nothing to deliver
		}
		a.replaySpool()
		if len(pending) == 0 {
			return
		}
		if err := a.postWithTimeout(pending); err != nil {
			a.metrics.DispatchFailures.Add(1)
			a.spoolBatch(pending, err)
			return
		}
		a.metrics.ResultsPosted.Add(uint64(len(pending)))
		a.log.Debug("results posted", "count", len(pending))
	}

	recv := func(res protocol.Result) {
		batch = append(batch, res)
		if len(batch) >= resultBatchSize {
			send()
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Drain whatever results are buffered, then flush, so a clean
			// shutdown does not lose the last observations.
			for {
				select {
				case res := <-in:
					recv(res)
				default:
					send()
					return
				}
			}
		case res := <-in:
			recv(res)
		case <-flush.C:
			send()
		}
	}
}

// postWithTimeout delivers a batch under the shared dispatch timeout.
func (a *Agent) postWithTimeout(results []protocol.Result) error {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
	defer cancel()
	return a.client.PostResults(ctx, results)
}

// replaySpool tries to deliver spooled batches oldest-first. It stops at the
// first failure (the backend is still unavailable) to preserve ordering, and
// refreshes the depth gauge once when done.
func (a *Agent) replaySpool() {
	if a.spool.Depth() == 0 {
		return
	}
	for {
		b, ok := a.spool.Oldest()
		if !ok {
			break
		}
		if err := a.postWithTimeout(b.Results); err != nil {
			a.log.Debug("spool replay paused, backend still failing", "err", err)
			break
		}
		a.spool.Remove(b)
		a.metrics.ResultsPosted.Add(uint64(len(b.Results)))
		a.log.Info("replayed spooled results", "count", len(b.Results))
	}
	a.metrics.SpoolDepth.Store(int64(a.spool.Depth()))
}

// spoolBatch stores an undelivered batch for later replay.
func (a *Agent) spoolBatch(batch []protocol.Result, cause error) {
	dropped, err := a.spool.Add(batch)
	if err != nil {
		a.log.Error("post failed and spooling failed, dropping batch", "count", len(batch), "err", err, "cause", cause)
		return
	}
	a.metrics.ResultsSpooled.Add(uint64(len(batch)))
	a.metrics.SpoolDepth.Store(int64(a.spool.Depth()))
	if dropped > 0 {
		a.log.Warn("spool full, dropped oldest batches", "dropped", dropped)
	}
	a.log.Warn("post failed, spooled batch for retry", "count", len(batch), "err", cause)
}

// heartbeatLoop sends a heartbeat immediately and then on a fixed period.
func (a *Agent) heartbeatLoop(ctx context.Context) {
	beat := func() {
		hbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := a.client.Heartbeat(hbCtx, a.hostname); err != nil {
			a.log.Warn("heartbeat failed", "err", err)
			return
		}
		a.metrics.Heartbeats.Add(1)
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

// configCachePath returns the on-disk cache path, or "" when no spool dir is set.
func (a *Agent) configCachePath() string {
	if a.cfg.SpoolDir == "" {
		return ""
	}
	return filepath.Join(a.cfg.SpoolDir, configCacheName)
}

func (a *Agent) saveCachedConfig(cfg protocol.ConfigResponse) {
	path := a.configCachePath()
	if path == "" {
		return
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		a.log.Debug("could not write config cache", "err", err)
	}
}

func (a *Agent) loadCachedConfig() (protocol.ConfigResponse, bool) {
	path := a.configCachePath()
	if path == "" {
		return protocol.ConfigResponse{}, false
	}
	cfg, err := loadChecksFile(path)
	if err != nil {
		return protocol.ConfigResponse{}, false
	}
	cfg.Checks = a.validateChecks(cfg.Checks)
	return cfg, true
}

// jitterUpTo returns a random duration in [0, min(d, maxStartupJitter)).
func jitterUpTo(d time.Duration) time.Duration {
	limit := d
	if limit > maxStartupJitter {
		limit = maxStartupJitter
	}
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}
