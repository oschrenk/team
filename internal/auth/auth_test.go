package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/oschrenk/team/internal/paths"
)

func TestEnsureToken_CreatesOncePerPath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "token")

	tok, err := EnsureToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 20 {
		t.Fatalf("token too short: %q", tok)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm got %o, want 0600", got)
	}

	// Second call returns the same token (read path).
	tok2, err := EnsureToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != tok2 {
		t.Fatalf("expected stable token, got %q vs %q", tok, tok2)
	}
}

func TestEnsureToken_ConcurrentCallersAgree(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "token")

	const N = 16
	tokens := make([]string, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = EnsureToken(path)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	for i := 1; i < N; i++ {
		if tokens[i] != tokens[0] {
			t.Fatalf("goroutine %d disagrees: %q vs %q", i, tokens[i], tokens[0])
		}
	}
}

func TestEnsureToken_RefusesSymlink(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.WriteFile(real, []byte("xxx"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureToken(link); err == nil {
		t.Fatal("expected error for symlink token path")
	}
}

func TestVerifyServerIdentity_MissingPidfile(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if VerifyServerIdentity("127.0.0.1", 9473) {
		t.Fatal("expected false with no pidfile")
	}
}

func TestVerifyServerIdentity_DeadPid(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(paths.DataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteServerIdentity(1<<30, "127.0.0.1", 9473); err != nil {
		t.Fatal(err)
	}
	if VerifyServerIdentity("127.0.0.1", 9473) {
		t.Fatal("expected false for dead pid")
	}
}

func TestVerifyServerIdentity_CmdlineMatch(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(paths.DataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := WriteServerIdentity(pid, "127.0.0.1", 9473); err != nil {
		t.Fatal(err)
	}

	// Inject a fetcher that pretends our PID is `team server`.
	ok := verifyServerIdentity("127.0.0.1", 9473, func(p int) ([]string, error) {
		if p != pid {
			t.Fatalf("fetcher called with %d, want %d", p, pid)
		}
		return []string{"team", "server", "--port", "9473"}, nil
	})
	if !ok {
		t.Fatal("expected match")
	}
}

func TestVerifyServerIdentity_CmdlineMismatch(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(paths.DataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteServerIdentity(os.Getpid(), "127.0.0.1", 9473); err != nil {
		t.Fatal(err)
	}
	ok := verifyServerIdentity("127.0.0.1", 9473, func(int) ([]string, error) {
		return []string{"nginx", "-g", "daemon off;"}, nil
	})
	if ok {
		t.Fatal("expected mismatch to fail")
	}
}

func TestVerifyServerIdentity_SoftTrustOnFetchError(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(paths.DataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteServerIdentity(os.Getpid(), "127.0.0.1", 9473); err != nil {
		t.Fatal(err)
	}
	ok := verifyServerIdentity("127.0.0.1", 9473, func(int) ([]string, error) {
		return nil, errors.New("access denied")
	})
	if !ok {
		t.Fatal("expected soft-trust when fetcher errors")
	}
}

func TestVerifyServerIdentity_TamperedMeta(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(paths.DataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := WriteServerIdentity(pid, "127.0.0.1", 9473); err != nil {
		t.Fatal(err)
	}
	// Tamper with the meta file post-write to claim a different port.
	tampered := []byte(`{"pid":` + strconv.Itoa(pid) + `,"host":"127.0.0.1","port":9999}`)
	if err := os.WriteFile(paths.PidfileMetaPath(9473, "127.0.0.1"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := verifyServerIdentity("127.0.0.1", 9473, func(int) ([]string, error) {
		return []string{"team", "server"}, nil
	})
	if ok {
		t.Fatal("expected tampered meta to fail")
	}
}
