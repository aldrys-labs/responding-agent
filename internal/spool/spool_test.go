package spool

import (
	"os"
	"path/filepath"
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

// A corrupt head file must be dropped and replay must reach the next good
// batch in the same call, without stalling until the next flush tick.
func TestSpoolDiskSkipsCorruptHead(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 10, counterClock())
	s.Add(batch("good"))

	// A corrupt file named to sort before the good batch.
	corrupt := filepath.Join(dir, "00000000000000000000-0000000000"+fileExt)
	if err := os.WriteFile(corrupt, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	b, ok := s.Oldest()
	if !ok || b.Results[0].CheckID != "good" {
		t.Fatalf("oldest = %+v ok=%v, want the good batch", b, ok)
	}
	if _, err := os.Stat(corrupt); !os.IsNotExist(err) {
		t.Error("corrupt file was not removed")
	}
	if d := s.Depth(); d != 1 {
		t.Errorf("depth = %d, want 1", d)
	}
}

// A corrupt head file that cannot be removed (read-only dir) must not drive
// the count negative, must stay counted, and must not hide the good batches.
func TestSpoolDiskUnremovableCorruptHead(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 10, counterClock())
	s.Add(batch("good"))

	corrupt := filepath.Join(dir, "00000000000000000000-0000000000"+fileExt)
	if err := os.WriteFile(corrupt, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	// Repeated replay passes, as the dispatch loop would do every flush tick.
	for i := 0; i < 5; i++ {
		b, ok := s.Oldest()
		if !ok || b.Results[0].CheckID != "good" {
			t.Fatalf("pass %d: oldest = %+v ok=%v, want the good batch", i, b, ok)
		}
	}
	// Both files are still on disk, so both stay counted: the cap gate in
	// trimLocked must keep working.
	if d := s.Depth(); d != 2 {
		t.Errorf("depth = %d, want 2 (corrupt file still occupies the spool)", d)
	}
}

// Orphan *.tmp files from a crash mid-write are swept at Open and never
// counted as batches.
func TestSpoolOpenSweepsOrphanTmp(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "00000000000000000001-0000000000"+fileExt+".tmp")
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, 10, counterClock())
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Error("orphan tmp file was not swept")
	}
	if d := s.Depth(); d != 0 {
		t.Errorf("depth = %d, want 0", d)
	}
}

func TestSpoolEmpty(t *testing.T) {
	s, _ := Open("", 10, counterClock())
	if _, ok := s.Oldest(); ok {
		t.Fatal("empty spool should report no oldest")
	}
	s.Remove(Batch{}) // must not panic
}
