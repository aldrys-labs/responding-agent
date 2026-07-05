package agent

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/config"
	"github.com/aldrys-labs/responding-agent/internal/metrics"
	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	return newTestAgentWithBackend(t, "")
}

// newTestAgentWithBackend builds an agent; a non-empty backendURL enables the
// dispatch client.
func newTestAgentWithBackend(t *testing.T, backendURL string) *Agent {
	t.Helper()
	cfg := config.Config{
		BackendURL:          backendURL,
		SpoolDir:            t.TempDir(),
		SpoolMaxBatches:     10,
		MaxConcurrentChecks: 4,
		PollIntervalSeconds: 60,
	}
	if backendURL != "" {
		cfg.Token = "test-token"
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

// When the dispatch buffer is full (stalled backend), a check loop must spool
// its result and keep its schedule instead of blocking on the channel.
func TestCheckLoopSpoolsWhenDispatchStalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Unbuffered channel with no reader: the dispatcher is fully stalled.
	results := make(chan protocol.Result)
	chk := protocol.Check{ID: "c1", Type: protocol.CheckHTTP, Target: srv.URL, IntervalSeconds: 1, TimeoutMs: 5000}

	go a.runCheckLoop(ctx, chk, results)

	deadline := time.Now().Add(10 * time.Second)
	for a.spool.Depth() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("check result was never spooled while dispatch was stalled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// After a failed post the dispatch loop backs off: the next batch is spooled
// without another delivery attempt, and everything is replayed once the
// backend recovers.
func TestDispatchLoopBacksOffAndRecovers(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	failing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAgentWithBackend(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan protocol.Result)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.dispatchLoop(ctx, results)
	}()

	feed := func(n int) {
		for i := 0; i < n; i++ {
			results <- protocol.Result{CheckID: "c1", Status: protocol.StatusUp, Timestamp: "t"}
		}
	}
	waitDepth := func(want int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for a.spool.Depth() != want {
			if time.Now().After(deadline) {
				t.Fatalf("spool depth = %d, want %d", a.spool.Depth(), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// First full batch: one delivery attempt fails, the batch is spooled and
	// the loop enters backoff.
	feed(resultBatchSize)
	waitDepth(1)
	mu.Lock()
	after1 := requests
	mu.Unlock()
	if after1 != 1 {
		t.Fatalf("requests after first failure = %d, want 1", after1)
	}

	// Second full batch lands inside the backoff window: spooled with no new
	// delivery attempt.
	feed(resultBatchSize)
	waitDepth(2)
	mu.Lock()
	after2 := requests
	failing = false
	mu.Unlock()
	if after2 != 1 {
		t.Fatalf("requests during backoff = %d, want still 1", after2)
	}

	// Once the backoff expires and the backend recovers, the spool drains.
	waitDepth(0)

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}
}

// On shutdown the dispatch loop must persist the pending batch to the spool
// without touching the network, so a hanging backend cannot make the flush
// outlive the SIGTERM grace period.
func TestDispatchLoopShutdownSpoolsWithoutNetwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done() // black-hole: never respond
	}))
	defer srv.Close()

	a := newTestAgentWithBackend(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan protocol.Result, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.dispatchLoop(ctx, results)
	}()

	for i := 0; i < 3; i++ {
		results <- protocol.Result{CheckID: "c1", Status: protocol.StatusUp, Timestamp: "t"}
	}
	cancel()

	start := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch loop did not exit after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown drain took %v, want a few seconds at most", elapsed)
	}
	if depth := a.spool.Depth(); depth != 1 {
		t.Fatalf("spool depth = %d, want 1 (pending batch persisted)", depth)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("backend received %d requests during shutdown, want 0", n)
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
