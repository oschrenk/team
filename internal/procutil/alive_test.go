package procutil

import (
	"os"
	"testing"
)

func TestSafePidAlive_Self(t *testing.T) {
	if !SafePidAlive(os.Getpid()) {
		t.Fatal("expected current process to be alive")
	}
}

func TestSafePidAlive_NonPositive(t *testing.T) {
	for _, pid := range []int{0, -1, -999} {
		if SafePidAlive(pid) {
			t.Fatalf("pid %d should not be reported alive", pid)
		}
	}
}

func TestSafePidAlive_Dead(t *testing.T) {
	// Pid 1 always exists. Pick a deliberately impossible pid instead.
	// Max user pid on macOS/Linux is well under 2^31. 2^30 is safely unused.
	if SafePidAlive(1 << 30) {
		t.Fatal("expected reserved pid range to be dead")
	}
}
