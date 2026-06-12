// Package flock wraps syscall.Flock as a per-ppid mutual exclusion for
// the team monitor: only one `team connect` can run for a given
// Claude Code session.
package flock

import (
	"errors"
	"os"
	"syscall"
)

// Acquire opens path (creating if absent, mode 0600) and tries to take
// an exclusive non-blocking flock. Returns the open *os.File on
// success — close it to release the lock.
//
// Returns (nil, false, nil) if the lock is already held by another
// process. Other errors propagate.
//
// Does NOT unlink the lock file on release: flock is keyed on the open
// file description (kernel-level), and unlinking would create a TOCTOU
// hole where two callers can hold "the lock" on different inodes for
// the same path.
func Acquire(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return f, true, nil
}
