// Package dispatch is the agent's HTTP client to the respondi.ng backend
// (control-plane). It pulls the check configuration, pushes result batches and
// sends heartbeats, all authenticated with the agent's Bearer token.
package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// API paths on the backend base URL.
const (
	pathConfig    = "/api/agent/config"
	pathResults   = "/api/agent/results"
	pathHeartbeat = "/api/agent/heartbeat"
)

// maxErrBody caps how much of an error response body we include in messages.
const maxErrBody = 2 << 10 // 2 KiB

// Client talks to one backend with one agent token.
type Client struct {
	baseURL string
	token   string
	version string
	http    *http.Client
}

// NewClient builds a dispatch client. baseURL must not have a trailing slash
// (config.Load already trims it).
func NewClient(baseURL, token, version string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		version: version,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchConfig pulls the current check list from the backend.
func (c *Client) FetchConfig(ctx context.Context) (protocol.ConfigResponse, error) {
	var out protocol.ConfigResponse
	err := c.do(ctx, http.MethodGet, pathConfig, nil, &out)
	return out, err
}

// PostResults pushes a batch of results. An empty batch is a no-op.
func (c *Client) PostResults(ctx context.Context, results []protocol.Result) error {
	if len(results) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPost, pathResults, results, nil)
}

// Heartbeat tells the backend this agent is alive.
func (c *Client) Heartbeat(ctx context.Context, hostname string) error {
	hb := protocol.Heartbeat{
		AgentVersion: c.version,
		Hostname:     hostname,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}
	return c.do(ctx, http.MethodPost, pathHeartbeat, hb, nil)
}

// do performs a JSON request. When body is non-nil it is JSON-encoded; when out
// is non-nil the response body is JSON-decoded into it. Non-2xx responses are
// returned as errors with a truncated body snippet.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "responding-agent/"+c.version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(snippet))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
