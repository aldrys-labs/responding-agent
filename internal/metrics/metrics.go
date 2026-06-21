// Package metrics holds the agent's runtime counters and renders them in the
// Prometheus text exposition format for the local /metrics endpoint.
package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// Metrics is a set of process-lifetime counters and gauges. All fields are
// accessed atomically and are safe for concurrent use.
type Metrics struct {
	startUnix int64

	ChecksConfigured atomic.Int64  // gauge: checks currently scheduled
	ConfigReloads    atomic.Uint64 // config successfully applied
	ConfigFailures   atomic.Uint64 // config load failures
	UsingCache       atomic.Bool   // last config came from the on-disk cache

	ChecksRun     atomic.Uint64
	CheckUp       atomic.Uint64
	CheckDegraded atomic.Uint64
	CheckDown     atomic.Uint64

	ResultsPosted    atomic.Uint64 // results successfully delivered
	ResultsSpooled   atomic.Uint64 // results written to the spool after a failure
	DispatchFailures atomic.Uint64
	Heartbeats       atomic.Uint64
	SpoolDepth       atomic.Int64 // gauge: result batches waiting in the spool
}

// New returns a Metrics whose process_start_time is startUnix (seconds).
func New(startUnix int64) *Metrics {
	return &Metrics{startUnix: startUnix}
}

// RecordResult bumps the per-status counters for one check result.
func (m *Metrics) RecordResult(status protocol.Status) {
	m.ChecksRun.Add(1)
	switch status {
	case protocol.StatusUp:
		m.CheckUp.Add(1)
	case protocol.StatusDegraded:
		m.CheckDegraded.Add(1)
	case protocol.StatusDown:
		m.CheckDown.Add(1)
	}
}

// Ready reports whether at least one configuration has been applied, which is
// what the /readyz endpoint checks.
func (m *Metrics) Ready() bool {
	return m.ConfigReloads.Load() > 0
}

// Render writes the counters in Prometheus text exposition format.
func (m *Metrics) Render() string {
	var b strings.Builder
	g := func(name, help string, val int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, val)
	}
	c := func(name, help string, val uint64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, val)
	}
	boolToInt := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}

	g("responding_agent_start_time_seconds", "Process start time, unix seconds.", m.startUnix)
	g("responding_agent_checks_configured", "Checks currently scheduled.", m.ChecksConfigured.Load())
	g("responding_agent_spool_depth", "Result batches waiting in the spool.", m.SpoolDepth.Load())
	g("responding_agent_using_config_cache", "1 when the active config came from the on-disk cache.", boolToInt(m.UsingCache.Load()))

	c("responding_agent_config_reloads_total", "Configurations successfully applied.", m.ConfigReloads.Load())
	c("responding_agent_config_failures_total", "Configuration load failures.", m.ConfigFailures.Load())
	c("responding_agent_checks_run_total", "Checks executed.", m.ChecksRun.Load())
	c("responding_agent_checks_up_total", "Checks observed up.", m.CheckUp.Load())
	c("responding_agent_checks_degraded_total", "Checks observed degraded.", m.CheckDegraded.Load())
	c("responding_agent_checks_down_total", "Checks observed down.", m.CheckDown.Load())
	c("responding_agent_results_posted_total", "Results delivered to the backend.", m.ResultsPosted.Load())
	c("responding_agent_results_spooled_total", "Results written to the spool after a failure.", m.ResultsSpooled.Load())
	c("responding_agent_dispatch_failures_total", "Failed result deliveries.", m.DispatchFailures.Load())
	c("responding_agent_heartbeats_total", "Heartbeats sent.", m.Heartbeats.Load())
	return b.String()
}
