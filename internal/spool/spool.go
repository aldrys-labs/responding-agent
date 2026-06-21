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
	"sync"
	"sync/atomic"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// fileExt marks spooled batch files on disk.
const fileExt = ".batch.json"

// Spool is a bounded FIFO of result batches. With a directory it persists to
// disk (durable across restarts); without one it keeps batches in memory
// (durable across a backend outage within a single run). All methods are safe
// for concurrent use.
type Spool struct {
	dir        string
	maxBatches int

	mu      sync.Mutex
	mem     [][]protocol.Result // used when dir == ""
	seq     atomic.Uint64       // disambiguates same-nanosecond filenames
	nowNano func() int64        // injectable clock for deterministic tests
}

// Open creates a spool. When dir is non-empty it is created if needed and any
// previously spooled batches are retained. maxBatches caps how many batches are
// kept; older batches are dropped first. A maxBatches <= 0 disables the cap.
func Open(dir string, maxBatches int, nowNano func() int64) (*Spool, error) {
	s := &Spool{dir: dir, maxBatches: maxBatches, nowNano: nowNano}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create spool dir: %w", err)
		}
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
		return s.trimMemLocked(), nil
	}

	name := fmt.Sprintf("%020d-%010d%s", s.nowNano(), s.seq.Add(1), fileExt)
	data, err := json.Marshal(results)
	if err != nil {
		return 0, fmt.Errorf("encode batch: %w", err)
	}
	tmp := filepath.Join(s.dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return 0, fmt.Errorf("write batch: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		return 0, fmt.Errorf("commit batch: %w", err)
	}
	return s.trimDiskLocked()
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

	files, err := s.sortedFilesLocked()
	if err != nil || len(files) == 0 {
		return Batch{}, false
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		// Unreadable file: drop it so the loop can make progress.
		os.Remove(files[0])
		return Batch{}, false
	}
	var results []protocol.Result
	if err := json.Unmarshal(data, &results); err != nil {
		os.Remove(files[0])
		return Batch{}, false
	}
	return Batch{Results: results, ref: files[0]}, true
}

// Remove discards a batch previously returned by Oldest after it was delivered.
func (s *Spool) Remove(b Batch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir == "" {
		if len(s.mem) > 0 {
			s.mem = s.mem[1:]
		}
		return
	}
	if b.ref != "" {
		os.Remove(b.ref)
	}
}

// Depth returns the number of spooled batches.
func (s *Spool) Depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return len(s.mem)
	}
	files, _ := s.sortedFilesLocked()
	return len(files)
}

func (s *Spool) trimMemLocked() (dropped int) {
	if s.maxBatches <= 0 {
		return 0
	}
	for len(s.mem) > s.maxBatches {
		s.mem = s.mem[1:]
		dropped++
	}
	return dropped
}

func (s *Spool) trimDiskLocked() (dropped int, err error) {
	if s.maxBatches <= 0 {
		return 0, nil
	}
	files, err := s.sortedFilesLocked()
	if err != nil {
		return 0, err
	}
	for len(files) > s.maxBatches {
		os.Remove(files[0])
		files = files[1:]
		dropped++
	}
	return dropped, nil
}

// sortedFilesLocked lists committed batch files oldest-first. The caller holds
// the mutex.
func (s *Spool) sortedFilesLocked() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" && hasSpoolExt(e.Name()) {
			files = append(files, filepath.Join(s.dir, e.Name()))
		}
	}
	sort.Strings(files) // zero-padded names sort chronologically
	return files, nil
}

func hasSpoolExt(name string) bool {
	return len(name) > len(fileExt) && name[len(name)-len(fileExt):] == fileExt
}
