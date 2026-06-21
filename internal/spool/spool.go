// Package spool buffers result batches that could not be delivered, so they
// survive a backend outage and, when a directory is configured, an agent
// restart. It is a bounded FIFO: when full, the oldest batches are dropped.
package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aldrys-labs/responding-agent/internal/fsutil"
	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// fileExt marks spooled batch files on disk.
const fileExt = ".batch.json"

// Spool is a bounded FIFO of result batches. With a directory it persists to
// disk (durable across restarts); without one it keeps batches in memory
// (durable across a backend outage within a single run). All methods are safe
// for concurrent use. The current depth is tracked in memory so the common
// read paths (Depth, replay short-circuit) never touch the filesystem.
type Spool struct {
	dir        string
	maxBatches int

	mu      sync.Mutex
	mem     [][]protocol.Result // used when dir == ""
	count   int                 // number of spooled batches (both modes)
	seq     atomic.Uint64       // disambiguates same-nanosecond filenames
	nowNano func() int64        // injectable clock for deterministic tests
}

// Open creates a spool. When dir is non-empty it is created if needed and any
// previously spooled batches are retained (and counted). maxBatches caps how
// many batches are kept; older batches are dropped first. maxBatches <= 0
// disables the cap.
func Open(dir string, maxBatches int, nowNano func() int64) (*Spool, error) {
	s := &Spool{dir: dir, maxBatches: maxBatches, nowNano: nowNano}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create spool dir: %w", err)
		}
		files, err := s.listFiles()
		if err != nil {
			return nil, fmt.Errorf("scan spool dir: %w", err)
		}
		s.count = len(files)
	}
	return s, nil
}

// Add appends a batch. It returns the number of batches dropped to honour the
// cap (0 normally). An empty batch is ignored.
func (s *Spool) Add(results []protocol.Result) (dropped int, err error) {
	if len(results) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir == "" {
		s.mem = append(s.mem, results)
	} else {
		name := fmt.Sprintf("%020d-%010d%s", s.nowNano(), s.seq.Add(1), fileExt)
		data, err := json.Marshal(results)
		if err != nil {
			return 0, fmt.Errorf("encode batch: %w", err)
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(s.dir, name), data, 0o600); err != nil {
			return 0, fmt.Errorf("write batch: %w", err)
		}
	}
	s.count++
	return s.trimLocked(), nil
}

// Batch identifies a spooled batch for the replay loop.
type Batch struct {
	Results []protocol.Result
	ref     string // file path on disk, or "" for the in-memory head
}

// Oldest returns the oldest spooled batch and true, or a zero Batch and false
// when the spool is empty.
func (s *Spool) Oldest() (Batch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir == "" {
		if len(s.mem) == 0 {
			return Batch{}, false
		}
		return Batch{Results: s.mem[0]}, true
	}

	path, ok := s.oldestFile()
	if !ok {
		return Batch{}, false
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var results []protocol.Result
		if err = json.Unmarshal(data, &results); err == nil {
			return Batch{Results: results, ref: path}, true
		}
	}
	// Unreadable or corrupt file: drop it so the loop can make progress.
	os.Remove(path)
	s.count--
	return Batch{}, false
}

// Remove discards a batch previously returned by Oldest after it was delivered.
func (s *Spool) Remove(b Batch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir == "" {
		if len(s.mem) > 0 {
			s.mem = s.mem[1:]
			s.count--
		}
		return
	}
	if b.ref != "" {
		os.Remove(b.ref)
		s.count--
	}
}

// Depth returns the number of spooled batches. It is O(1): the count is kept in
// memory rather than re-listing the directory.
func (s *Spool) Depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// trimLocked drops the oldest batches until the cap is met, returning how many
// were dropped. The caller holds the mutex.
func (s *Spool) trimLocked() (dropped int) {
	if s.maxBatches <= 0 || s.count <= s.maxBatches {
		return 0
	}
	if s.dir == "" {
		for len(s.mem) > s.maxBatches {
			s.mem = s.mem[1:]
			s.count--
			dropped++
		}
		return dropped
	}
	files, err := s.listFiles()
	if err != nil {
		return 0
	}
	for len(files) > s.maxBatches {
		os.Remove(files[0])
		files = files[1:]
		s.count--
		dropped++
	}
	return dropped
}

// oldestFile returns the lexically smallest (oldest, given zero-padded names)
// batch file in a single pass, without sorting the whole directory.
func (s *Spool) oldestFile() (string, bool) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return "", false
	}
	min := ""
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, fileExt) {
			continue
		}
		if min == "" || name < min {
			min = name
		}
	}
	if min == "" {
		return "", false
	}
	return filepath.Join(s.dir, min), true
}

// listFiles returns the batch files sorted oldest-first. Used only on the cold
// paths (Open and trim), not on every Depth/replay call.
func (s *Spool) listFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), fileExt) {
			files = append(files, filepath.Join(s.dir, e.Name()))
		}
	}
	sort.Strings(files) // zero-padded names sort chronologically
	return files, nil
}
