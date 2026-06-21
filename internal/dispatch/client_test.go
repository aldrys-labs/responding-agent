package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func TestFetchConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathConfig {
			t.Errorf("path = %q, want %q", r.URL.Path, pathConfig)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth = %q, want Bearer tok", got)
		}
		json.NewEncoder(w).Encode(protocol.ConfigResponse{
			PollIntervalSeconds: 30,
			Checks: []protocol.Check{
				{ID: "a", Type: protocol.CheckHTTP, Target: "https://x", IntervalSeconds: 10},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "test")
	cfg, err := c.FetchConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalSeconds != 30 || len(cfg.Checks) != 1 || cfg.Checks[0].ID != "a" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestPostResults(t *testing.T) {
	var received []protocol.Result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathResults {
			t.Errorf("path = %q, want %q", r.URL.Path, pathResults)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "test")
	results := []protocol.Result{
		{CheckID: "a", Status: protocol.StatusUp, LatencyMs: 12, Timestamp: "2026-06-21T00:00:00Z"},
	}
	if err := c.PostResults(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].CheckID != "a" {
		t.Fatalf("server received %+v", received)
	}
}

func TestPostResultsEmptyIsNoop(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "tok", "test")
	if err := c.PostResults(context.Background(), nil); err != nil {
		t.Errorf("empty batch should be a no-op, got %v", err)
	}
}

func TestHeartbeat(t *testing.T) {
	var hb protocol.Heartbeat
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathHeartbeat {
			t.Errorf("path = %q, want %q", r.URL.Path, pathHeartbeat)
		}
		json.NewDecoder(r.Body).Decode(&hb)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "v1.2.3")
	if err := c.Heartbeat(context.Background(), "host-a"); err != nil {
		t.Fatal(err)
	}
	if hb.Hostname != "host-a" || hb.AgentVersion != "v1.2.3" {
		t.Fatalf("unexpected heartbeat: %+v", hb)
	}
}

func TestNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bad", "test")
	if _, err := c.FetchConfig(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}
