package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataDir_RespectsEnv(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", "/tmp/team-xyz")
	if got := DataDir(); got != "/tmp/team-xyz" {
		t.Fatalf("got %q, want %q", got, "/tmp/team-xyz")
	}
}

func TestDataDir_DefaultsToClaudeData(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", "")
	got := DataDir()
	wantSuffix := filepath.Join(".claude", "data", "team")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("got %q, want suffix %q", got, wantSuffix)
	}
}

func TestPidfile_DefaultHost_LegacyStem(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", "/tmp/x")
	for _, host := range []string{"", "127.0.0.1"} {
		got := PidfilePath(9473, host)
		if !strings.HasSuffix(got, "/server.9473.pid") {
			t.Fatalf("host=%q got %q, want suffix /server.9473.pid", host, got)
		}
	}
}

func TestPidfile_CustomHost_IncludesEncodedHost(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", "/tmp/x")
	got := PidfilePath(9473, "0.0.0.0")
	if !strings.Contains(got, "0.0.0.0") || !strings.HasSuffix(got, ".9473.pid") {
		t.Fatalf("got %q, want host and port in stem", got)
	}
}

func TestClientLockAndSessionPaths(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", "/tmp/x")
	if got := ClientLockPath(1234); !strings.HasSuffix(got, "/clients/1234.lock") {
		t.Fatalf("got %q", got)
	}
	if got := ClientSessionPath(1234); !strings.HasSuffix(got, "/clients/1234.session") {
		t.Fatalf("got %q", got)
	}
}

func TestSecureDir_AppliesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secure")
	if err := SecureDir(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("got perm %o, want 0700", got)
	}
}

func TestSecureFile_AppliesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SecureFile(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("got perm %o, want 0600", got)
	}
}
