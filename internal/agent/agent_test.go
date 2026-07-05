package agent

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
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

// Reconciling an unchanged config must leave running loops alone (preserving
// their ticker phase), while changed or removed checks are restarted/stopped.
func TestReconcile(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
	}))
	defer srv.Close()
	countHits := func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
	waitForHit := func(path string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for countHits(path) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("no request on %s", path)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	a := newTestAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan protocol.Result, 16)
	running := make(map[string]runningCheck)
	chk := func(path string) protocol.Check {
		return protocol.Check{ID: "a", Type: protocol.CheckHTTP, Target: srv.URL + path, IntervalSeconds: 300, TimeoutMs: 5000}
	}

	if started, stopped := a.reconcile(ctx, running, []protocol.Check{chk("/a")}, results); started != 1 || stopped != 0 {
		t.Fatalf("initial reconcile: started=%d stopped=%d, want 1/0", started, stopped)
	}
	waitForHit("/a")

	// Same spec: nothing restarts, and no immediate re-run happens (a restarted
	// loop would probe again within the 3s startup jitter).
	if started, stopped := a.reconcile(ctx, running, []protocol.Check{chk("/a")}, results); started != 0 || stopped != 0 {
		t.Fatalf("unchanged reconcile: started=%d stopped=%d, want 0/0", started, stopped)
	}
	time.Sleep(3500 * time.Millisecond)
	if n := countHits("/a"); n != 1 {
		t.Fatalf("unchanged check ran %d times, want 1 (loop was restarted)", n)
	}

	// Changed spec: the old loop stops and a new one starts against the new target.
	if started, stopped := a.reconcile(ctx, running, []protocol.Check{chk("/b")}, results); started != 1 || stopped != 1 {
		t.Fatalf("changed reconcile: started=%d stopped=%d, want 1/1", started, stopped)
	}
	waitForHit("/b")

	// Removed: the loop stops and the running set empties.
	if started, stopped := a.reconcile(ctx, running, nil, results); started != 0 || stopped != 1 {
		t.Fatalf("removal reconcile: started=%d stopped=%d, want 0/1", started, stopped)
	}
	if len(running) != 0 {
		t.Fatalf("running set not empty after removal: %v", running)
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
