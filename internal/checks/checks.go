// Package checks runs a single Check and turns it into a protocol.Result. Each
// check type (http, tcp, tls, ping) has its own runner in this package; the
// Runner dispatches on Check.Type.
package checks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// DefaultTimeout is used when a Check does not set TimeoutMs.
const DefaultTimeout = 10 * time.Second

// outcome is the raw result of a check before it is stamped into a
// protocol.Result.
type outcome struct {
	status  protocol.Status
	latency time.Duration
	err     error
}

// Runner executes checks. It holds a shared HTTP transport so connections are
// pooled across runs; per-check timeouts are applied via context.
type Runner struct {
	transport *http.Transport
}

// NewRunner builds a Runner with sensible transport defaults.
func NewRunner() *Runner {
	return &Runner{
		transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// timeout returns the effective timeout for a check.
func timeout(c protocol.Check) time.Duration {
	if c.TimeoutMs > 0 {
		return time.Duration(c.TimeoutMs) * time.Millisecond
	}
	return DefaultTimeout
}

// Run executes the check and returns a fully-formed Result. The check itself
// never returns an error to the caller: failures are encoded as a down status
// with an Error message, which is what the backend wants to record.
func (r *Runner) Run(ctx context.Context, c protocol.Check) protocol.Result {
	var o outcome
	switch c.Type {
	case protocol.CheckHTTP:
		o = r.runHTTP(ctx, c)
	case protocol.CheckTCP:
		o = runTCP(ctx, c)
	case protocol.CheckTLS:
		o = runTLS(ctx, c)
	case protocol.CheckPing:
		o = runPing(ctx, c)
	default:
		o = outcome{status: protocol.StatusDown, err: fmt.Errorf("unknown check type %q", c.Type)}
	}

	res := protocol.Result{
		CheckID:   c.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Status:    o.status,
		LatencyMs: o.latency.Milliseconds(),
	}
	if o.err != nil {
		res.Error = o.err.Error()
	}
	return res
}

// classifyLatency downgrades an otherwise-up check to degraded when a latency
// threshold is configured and exceeded.
func classifyLatency(c protocol.Check, latency time.Duration) protocol.Status {
	if c.DegradedAboveMs > 0 && latency > time.Duration(c.DegradedAboveMs)*time.Millisecond {
		return protocol.StatusDegraded
	}
	return protocol.StatusUp
}
