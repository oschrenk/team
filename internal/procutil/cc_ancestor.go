package procutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/process"
)

// ProcInfo is the subset of process introspection the CC-ancestor walk
// needs. Used as the unit of injection for tests.
type ProcInfo struct {
	Pid     int
	Cmdline []string
	Ppid    int
}

// ProcFetcher returns ProcInfo for a given pid. Tests inject a stub.
type ProcFetcher func(pid int) (ProcInfo, error)

// DefaultProcFetcher uses gopsutil. Settable for tests.
var DefaultProcFetcher ProcFetcher = gopsutilFetch

func gopsutilFetch(pid int) (ProcInfo, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return ProcInfo{}, err
	}
	cmd, err := p.CmdlineSlice()
	if err != nil {
		return ProcInfo{}, err
	}
	ppid, err := p.Ppid()
	if err != nil {
		return ProcInfo{}, err
	}
	return ProcInfo{Pid: pid, Cmdline: cmd, Ppid: int(ppid)}, nil
}

// FindCCAncestorPid walks the process tree starting from os.Getppid()
// and returns the first pid whose cmdline looks like a Claude Code
// process. Returns -1 if none found.
//
// Mirrors shared.py::find_cc_ancestor_pid. The detection list:
//   - cmdline[0] basename is "claude" or starts with "claude-"
//   - cmdline[0] path contains "/claude/versions/" or "/.local/share/claude/"
//   - cmdline[0] basename is "node"/"node.exe" AND the rest of the
//     cmdline mentions claude
func FindCCAncestorPid() int {
	return findCCAncestorPid(DefaultProcFetcher, os.Getppid())
}

func findCCAncestorPid(fetch ProcFetcher, start int) int {
	pid := start
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		info, err := fetch(pid)
		if err != nil {
			return -1
		}
		if isCCProcess(info.Cmdline) {
			return pid
		}
		pid = info.Ppid
	}
	return -1
}

func isCCProcess(cmd []string) bool {
	if len(cmd) == 0 {
		return false
	}
	exe := strings.ToLower(filepath.Base(cmd[0]))
	if exe == "claude" || strings.HasPrefix(exe, "claude-") {
		return true
	}
	full := strings.ToLower(cmd[0])
	if strings.Contains(full, "/claude/versions/") ||
		strings.Contains(full, "/.local/share/claude/") {
		return true
	}
	if exe == "node" || exe == "node.exe" {
		rest := strings.ToLower(strings.Join(cmd[1:], " "))
		if strings.Contains(rest, "/claude") ||
			strings.Contains(rest, "claude-code") ||
			strings.Contains(rest, "/.claude/") {
			return true
		}
	}
	return false
}

// ResolveListenerKey is the pid used as the state-file key for the
// inter-session listener of the current Claude Code session. Both the
// long-lived client (writer) and helper CLIs (readers) must compute it
// the same way.
//
// Resolution order (matches shared.py::resolve_listener_key):
//  1. TEAM_PPID_OVERRIDE env (test/debug)
//  2. CC ancestor pid via process walk
//  3. os.Getppid() fallback
func ResolveListenerKey() int {
	if v := os.Getenv("TEAM_PPID_OVERRIDE"); v != "" {
		if pid, err := strconv.Atoi(v); err == nil {
			return pid
		}
	}
	if pid := FindCCAncestorPid(); pid > 0 {
		return pid
	}
	return os.Getppid()
}
