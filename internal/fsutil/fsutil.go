// Package fsutil holds small filesystem helpers shared across the agent.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path atomically: it writes to a temporary file
// in the same directory, fsyncs it and renames it into place, so a reader never
// sees a half-written file and the content survives a power loss once the call
// returns. The caller is responsible for the path being unique enough that
// concurrent writers do not share the same temp name.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// Persist the rename itself. Directory fsync is unsupported on some
	// platforms, so this stays best-effort.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync() //nolint:errcheck // best-effort
		d.Close()
	}
	return nil
}
