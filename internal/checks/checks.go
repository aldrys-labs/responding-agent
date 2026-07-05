// Package checks runs a single Check and turns it into a protocol.Result. Each
// check type (http, tcp, tls, ping) has its own runner in this package; the
// Runner dispatches on Check.Type.
package checks

import (
	"context"
	"crypto/tls"
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

// Runner executes checks. It holds shared HTTP clients so connections are
// pooled across runs; per-check timeouts are applied via context. A second
// client whose transport skips certificate verification serves checks that set
// InsecureSkipVerify.
type Runner struct {
	client         *http.Client
	insecureClient *http.Client
}

// NewRunner builds a Runner with sensible transport defaults.
func NewRunner() *Runner {
	newClient := func(insecure bool) *http.Client {
		t := &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			TLSHandshakeTimeout: 10 * time.Second,
		}
		if insecure {
			t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in per check
		}
		return &http.Client{Transport: t}
	}
	return &Runner{
		client:         newClient(false),
		insecureClient: newClient(true),
	}
}

// httpClient returns the client to use for a check.
func (r *Runner) httpClient(c protocol.Check) *http.Client {
	if c.InsecureSkipVerify {
		return r.insecureClient
	}
	return r.client
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
// with an Error message, which is what the backend wants to record. A panic in
// a check is recovered and reported as down so one bad check cannot take down
// the agent.
func (r *Runner) Run(ctx context.Context, c protocol.Check) (res protocol.Result) {
	defer func() {
		if rec := recover(); rec != nil {
			res = protocol.Result{
				CheckID:   c.ID,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Status:    protocol.StatusDown,
				Error:     fmt.Sprintf("panic: %v", rec),
			}
		}
	}()

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

	// Sub-second precision: the backend dedupes results on (monitor, agent, ts),
	// so two runs completing in the same wall second must not share a timestamp.
	res = protocol.Result{
		CheckID:   c.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
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
