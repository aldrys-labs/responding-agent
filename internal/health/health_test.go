package health

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/metrics"
)

func TestHealthEndpoints(t *testing.T) {
	// Reserve a free port, then let the server bind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	m := metrics.New(1000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, addr, m, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	base := "http://" + addr
	waitUp(t, base+"/healthz")

	// readyz is 503 until a config is applied, then 200.
	if code := statusOf(t, base+"/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("readyz before config = %d, want 503", code)
	}
	m.ConfigReloads.Add(1)
	if code := statusOf(t, base+"/readyz"); code != http.StatusOK {
		t.Errorf("readyz after config = %d, want 200", code)
	}

	body, code := getBody(t, base+"/metrics")
	if code != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", code)
	}
	if len(body) == 0 {
		t.Error("metrics body is empty")
	}
}

func waitUp(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get(url); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("health server did not come up")
}

func statusOf(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func getBody(t *testing.T, url string) (string, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}
