package procutil

import (
	"errors"
	"testing"
)

func TestIsCCProcess(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
		want bool
	}{
		{"empty", []string{}, false},
		{"plain claude", []string{"/usr/local/bin/claude"}, true},
		{"claude-versioned-dash", []string{"/usr/local/bin/claude-2"}, true},
		{"versioned path", []string{"/Users/x/.local/share/claude/versions/2.1.146"}, true},
		{"local share path", []string{"/Users/x/.local/share/claude/bin"}, true},
		{"node + claude-code arg", []string{"/usr/bin/node", "/path/claude-code/index.js"}, true},
		{"node + /claude arg", []string{"/usr/bin/node", "/Users/x/something/claude/index.js"}, true},
		{"plain node", []string{"/usr/bin/node", "server.js"}, false},
		{"bash", []string{"/bin/bash"}, false},
		{"sh", []string{"sh", "-c", "/bin/foo"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCCProcess(tc.cmd); got != tc.want {
				t.Errorf("got %v, want %v for %v", got, tc.want, tc.cmd)
			}
		})
	}
}

func TestFindCCAncestorPid_WalksUp(t *testing.T) {
	// Tree: 5 (claude) → 4 (bash) → 3 (terminal) → 2 (login) → 1
	tree := map[int]ProcInfo{
		3: {Pid: 3, Cmdline: []string{"/usr/bin/Terminal"}, Ppid: 2},
		4: {Pid: 4, Cmdline: []string{"/bin/bash"}, Ppid: 3},
		5: {Pid: 5, Cmdline: []string{"/usr/local/bin/claude"}, Ppid: 4},
		6: {Pid: 6, Cmdline: []string{"team", "connect"}, Ppid: 5},
	}
	fetch := func(pid int) (ProcInfo, error) {
		if info, ok := tree[pid]; ok {
			return info, nil
		}
		return ProcInfo{}, errors.New("not found")
	}
	got := findCCAncestorPid(fetch, 6)
	if got != 5 {
		t.Fatalf("got pid %d, want 5", got)
	}
}

func TestFindCCAncestorPid_NoneFound(t *testing.T) {
	tree := map[int]ProcInfo{
		3: {Pid: 3, Cmdline: []string{"/bin/sh"}, Ppid: 2},
		4: {Pid: 4, Cmdline: []string{"/bin/bash"}, Ppid: 3},
		5: {Pid: 5, Cmdline: []string{"team", "connect"}, Ppid: 4},
	}
	fetch := func(pid int) (ProcInfo, error) {
		if info, ok := tree[pid]; ok {
			return info, nil
		}
		return ProcInfo{}, errors.New("not found")
	}
	if got := findCCAncestorPid(fetch, 5); got != -1 {
		t.Fatalf("got pid %d, want -1", got)
	}
}

func TestFindCCAncestorPid_BailsOnLoop(t *testing.T) {
	// Pathological ppid loop should not hang.
	tree := map[int]ProcInfo{
		3: {Pid: 3, Cmdline: []string{"/bin/sh"}, Ppid: 4},
		4: {Pid: 4, Cmdline: []string{"/bin/sh"}, Ppid: 3},
	}
	fetch := func(pid int) (ProcInfo, error) { return tree[pid], nil }
	if got := findCCAncestorPid(fetch, 3); got != -1 {
		t.Fatalf("got pid %d, want -1", got)
	}
}

func TestResolveListenerKey_RespectsEnv(t *testing.T) {
	t.Setenv("TEAM_PPID_OVERRIDE", "9999")
	if got := ResolveListenerKey(); got != 9999 {
		t.Fatalf("got %d, want 9999", got)
	}
}
