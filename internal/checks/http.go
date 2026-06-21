package checks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// maxBodyDrain caps how much of the response body we read and discard so the
// connection can be reused without buffering large payloads.
const maxBodyDrain = 64 << 10 // 64 KiB

// runHTTP performs an HTTP(S) request and classifies the response. A check is:
//   - down  on transport error, timeout, or a status code other than the
//     expected one (defaulting to 2xx when none is configured);
//   - degraded when reachable but slower than DegradedAboveMs;
//   - up otherwise.
func (r *Runner) runHTTP(ctx context.Context, c protocol.Check) outcome {
	method := c.Method
	if method == "" {
		method = http.MethodGet
	}

	ctx, cancel := context.WithTimeout(ctx, timeout(c))
	defer cancel()

	var body io.Reader
	if c.Body != "" {
		body = strings.NewReader(c.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Target, body)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("build request: %w", err)}
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Transport: r.httpTransport(c)}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return outcome{status: protocol.StatusDown, latency: latency, err: err}
	}
	defer resp.Body.Close()
	io.CopyN(io.Discard, resp.Body, maxBodyDrain) //nolint:errcheck // best-effort drain

	if !statusMatches(c.ExpectedStatus, resp.StatusCode) {
		return outcome{
			status:  protocol.StatusDown,
			latency: latency,
			err:     fmt.Errorf("unexpected status %d", resp.StatusCode),
		}
	}
	return outcome{status: classifyLatency(c, latency), latency: latency}
}

// statusMatches reports whether the observed status code satisfies the check.
// When no expected status is configured, any 2xx is considered a match.
func statusMatches(expected, got int) bool {
	if expected == 0 {
		return got >= 200 && got < 300
	}
	return got == expected
}
