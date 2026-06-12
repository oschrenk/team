// Package logfile owns size-based log rotation and JSONL appending for
// the team server's messages log.
//
// Single-writer assumption: only the server appends; rotation is
// best-effort and ignores ENOENT-style errors from a concurrent
// rotator (there shouldn't be one).
package logfile

import (
	"encoding/json"
	"fmt"
	"os"
)

// Rotate shifts path → path.1 → path.2 … → path.<backups>, dropping the
// oldest backup. No-op if path is missing or under maxBytes.
//
// Mirrors shared.py::rotate_log_if_needed.
func Rotate(path string, maxBytes int64, backups int) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	for i := backups; i >= 1; i-- {
		next := fmt.Sprintf("%s.%d", path, i)
		var prev string
		if i == 1 {
			prev = path
		} else {
			prev = fmt.Sprintf("%s.%d", path, i-1)
		}
		if i == backups {
			// Drop oldest. Best-effort.
			_ = os.Remove(next)
		}
		// Best-effort: prev may not exist yet if the log hasn't
		// rotated `backups` times.
		_ = os.Rename(prev, next)
	}
	return nil
}

// AppendJSONL marshals record to JSON, writes it as a single line, and
// re-applies mode. Caller is responsible for calling Rotate first if
// rotation is desired.
func AppendJSONL(path string, record any, mode os.FileMode) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	// Re-chmod in case the file pre-existed with looser perms (e.g.
	// from a previous version or a restore).
	return os.Chmod(path, mode)
}
