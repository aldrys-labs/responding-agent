package agent

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/config"
	"github.com/aldrys-labs/responding-agent/internal/metrics"
	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	cfg := config.Config{
		SpoolDir:            t.TempDir(),
		SpoolMaxBatches:     10,
		MaxConcurrentChecks: 4,
		PollIntervalSeconds: 60,
	}
	a, err := New(cfg, "test", slog.New(slog.DiscardHandler), metrics.New(time.Now().Unix()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// A check aborted by a generation swap (config refresh) or shutdown must not
// deliver its spurious "context canceled" down result to the dispatch loop.
func TestRunCheckLoopDropsResultOfCancelledRun(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	a := newTestAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan protocol.Result, 1)
	chk := protocol.Check{ID: "c1", Type: protocol.CheckHTTP, Target: srv.URL, IntervalSeconds: 60, TimeoutMs: 5000}

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runCheckLoop(ctx, chk, results)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("check never reached the test server")
	}
	cancel() // simulate the generation swap while the check is in flight

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runCheckLoop did not exit after cancellation")
	}
	select {
	case res := <-results:
		t.Fatalf("aborted run delivered a result: %+v", res)
	default:
	}
}

// A run that completes normally still delivers its result.
func TestRunCheckLoopDeliversCompletedRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan protocol.Result, 1)
	chk := protocol.Check{ID: "c1", Type: protocol.CheckHTTP, Target: srv.URL, IntervalSeconds: 60, TimeoutMs: 5000}

	go a.runCheckLoop(ctx, chk, results)

	select {
	case res := <-results:
		if res.Status != protocol.StatusUp {
			t.Fatalf("status = %q, want up (err: %s)", res.Status, res.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result delivered")
	}
}
