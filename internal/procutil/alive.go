// Package procutil holds tiny process-introspection helpers.
//
// TEAM-002 lands just SafePidAlive (stdlib-only). The gopsutil-backed
// CC-ancestor walk lands later in TEAM-006.
package procutil

import (
	"errors"
	"syscall"
)

// SafePidAlive reports whether a process with the given pid is alive.
//
// Semantics match shared.py::safe_pid_alive:
//   - pid <= 0          → false
//   - kill(pid, 0) ok   → true
//   - ESRCH             → false
//   - EPERM             → true (exists, owned by another user)
//   - other errors      → false
func SafePidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
