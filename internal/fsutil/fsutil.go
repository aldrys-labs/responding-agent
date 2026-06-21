// Package fsutil holds small filesystem helpers shared across the agent.
package fsutil

import "os"

// WriteFileAtomic writes data to path atomically: it writes to a temporary file
// in the same directory and renames it into place, so a reader never sees a
// half-written file. The caller is responsible for the path being unique enough
// that concurrent writers do not share the same temp name.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
