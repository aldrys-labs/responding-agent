package spool

import (
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// counterClock returns a monotonically increasing nanosecond stamp so spooled
// file names sort deterministically in tests.
func counterClock() func() int64 {
	var n int64
	return func() int64 { n++; return n }
}

func batch(id string) []protocol.Result {
	return []protocol.Result{{CheckID: id, Status: protocol.StatusDown}}
}

func runFIFOAndCap(t *testing.T, dir string) {
	t.Helper()
	s, err := Open(dir, 3, counterClock())
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"a", "b", "c"} {
		dropped, err := s.Add(batch(id))
		if err != nil {
			t.Fatal(err)
		}
		if dropped != 0 {
			t.Fatalf("unexpected drop adding %s", id)
		}
	}
	if d := s.Depth(); d != 3 {
		t.Fatalf("depth = %d, want 3", d)
	}

	// Exceeding the cap drops the oldest ("a").
	if dropped, _ := s.Add(batch("d")); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if d := s.Depth(); d != 3 {
		t.Fatalf("depth after cap = %d, want 3", d)
	}

	// FIFO: oldest surviving batch is "b".
	b, ok := s.Oldest()
	if !ok || b.Results[0].CheckID != "b" {
		t.Fatalf("oldest = %+v ok=%v, want b", b, ok)
	}
	s.Remove(b)
	if d := s.Depth(); d != 2 {
		t.Fatalf("depth after remove = %d, want 2", d)
	}
	b2, _ := s.Oldest()
	if b2.Results[0].CheckID != "c" {
		t.Fatalf("next oldest = %s, want c", b2.Results[0].CheckID)
	}
}

func TestSpoolMemoryFIFOAndCap(t *testing.T) { runFIFOAndCap(t, "") }
func TestSpoolDiskFIFOAndCap(t *testing.T)   { runFIFOAndCap(t, t.TempDir()) }

func TestSpoolEmptyBatchIgnored(t *testing.T) {
	s, _ := Open("", 10, counterClock())
	if dropped, err := s.Add(nil); err != nil || dropped != 0 {
		t.Fatalf("empty add: dropped=%d err=%v", dropped, err)
	}
	if s.Depth() != 0 {
		t.Fatal("empty batch should not be stored")
	}
}

func TestSpoolDiskPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir, 10, counterClock())
	s1.Add(batch("x"))
	s1.Add(batch("y"))

	// A fresh Spool over the same dir must see the persisted batches in order.
	s2, err := Open(dir, 10, counterClock())
	if err != nil {
		t.Fatal(err)
	}
	if d := s2.Depth(); d != 2 {
		t.Fatalf("reopened depth = %d, want 2", d)
	}
	b, ok := s2.Oldest()
	if !ok || b.Results[0].CheckID != "x" {
		t.Fatalf("reopened oldest = %+v, want x", b)
	}
}

func TestSpoolEmpty(t *testing.T) {
	s, _ := Open("", 10, counterClock())
	if _, ok := s.Oldest(); ok {
		t.Fatal("empty spool should report no oldest")
	}
	s.Remove(Batch{}) // must not panic
}
