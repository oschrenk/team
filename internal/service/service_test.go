package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- harness ----------------------------------------------------------

type recordedCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []recordedCall
	// behavior is a per-test override that returns stdout/code/err
	// based on the command. Default: success with empty stdout.
	behavior func(name string, args ...string) ([]byte, int, error)
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{name: name, args: append([]string{}, args...)})
	f.mu.Unlock()
	if f.behavior != nil {
		return f.behavior(name, args...)
	}
	return nil, 0, nil
}

func newCtl(t *testing.T) (*Controller, *fakeRunner) {
	t.Helper()
	home := t.TempDir()
	r := &fakeRunner{}
	c := &Controller{
		Label:   "com.test.team",
		UID:     501,
		HomeDir: home,
		Run:     r.run,
	}
	return c, r
}

// --- plist rendering --------------------------------------------------

func TestRenderPlist_ContainsResolvedPaths(t *testing.T) {
	c, _ := newCtl(t)
	body := c.RenderPlist("/usr/local/bin/team")
	for _, want := range []string{
		"<string>com.test.team</string>",
		"<string>/usr/local/bin/team</string>",
		"<string>server</string>",
		"<string>" + c.LogPath() + "</string>",
		"<string>" + c.HomeDir + "</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist missing %q\n%s", want, body)
		}
	}
}

func TestRenderPlist_EscapesXMLChars(t *testing.T) {
	c, _ := newCtl(t)
	// A binary path with & < > (contrived; just to verify escape).
	body := c.RenderPlist("/weird&path/<bin>/team")
	if strings.Contains(body, "&path") {
		t.Errorf("raw & not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&amp;path") {
		t.Errorf("& not escaped to &amp;:\n%s", body)
	}
	if !strings.Contains(body, "&lt;bin&gt;") {
		t.Errorf("< > not escaped:\n%s", body)
	}
}

func TestRenderPlist_StableForSameInput(t *testing.T) {
	c, _ := newCtl(t)
	a := c.RenderPlist("/bin/team")
	b := c.RenderPlist("/bin/team")
	if a != b {
		t.Fatal("render not deterministic")
	}
}

// --- paths -----------------------------------------------------------

func TestPaths(t *testing.T) {
	c, _ := newCtl(t)
	if got := c.PlistPath(); !strings.HasSuffix(got, "/Library/LaunchAgents/com.test.team.plist") {
		t.Fatalf("PlistPath = %q", got)
	}
	if got := c.LogPath(); !strings.HasSuffix(got, "/Library/Logs/team/server.log") {
		t.Fatalf("LogPath = %q", got)
	}
	if got := c.Target(); got != "gui/501/com.test.team" {
		t.Fatalf("Target = %q", got)
	}
	if got := c.Domain(); got != "gui/501" {
		t.Fatalf("Domain = %q", got)
	}
}

// --- launchctl wrappers -----------------------------------------------

func TestInstall_WritesPlistAndBootstraps(t *testing.T) {
	c, r := newCtl(t)
	// Status fails (not loaded) → triggers bootstrap.
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		switch args[0] {
		case "print":
			return []byte(""), 113, nil
		case "bootstrap":
			return nil, 0, nil
		}
		return nil, 0, nil
	}
	msg, err := c.Install("/usr/local/bin/team")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "installed" {
		t.Fatalf("got %q", msg)
	}
	// Plist must exist on disk.
	body, err := os.ReadFile(c.PlistPath())
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !strings.Contains(string(body), "/usr/local/bin/team") {
		t.Errorf("plist missing binary path")
	}
	// Log dir created.
	if _, err := os.Stat(filepath.Dir(c.LogPath())); err != nil {
		t.Errorf("log dir not created: %v", err)
	}
	// launchctl bootstrap was called with right args.
	gotBootstrap := false
	for _, call := range r.calls {
		if call.name == "launchctl" && call.args[0] == "bootstrap" {
			if call.args[1] != c.Domain() || call.args[2] != c.PlistPath() {
				t.Errorf("bootstrap args wrong: %v", call.args)
			}
			gotBootstrap = true
		}
	}
	if !gotBootstrap {
		t.Errorf("launchctl bootstrap not called; calls=%v", r.calls)
	}
}

func TestInstall_IdempotentWhenAlreadyLoaded(t *testing.T) {
	c, r := newCtl(t)
	// First install
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		if args[0] == "print" {
			return []byte(""), 113, nil
		}
		return nil, 0, nil
	}
	if _, err := c.Install("/usr/local/bin/team"); err != nil {
		t.Fatal(err)
	}

	// Second install: status says loaded, plist unchanged → no-op.
	r.calls = nil
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		if args[0] == "print" {
			// Pretend loaded.
			return []byte("state = running\npid = 999\n"), 0, nil
		}
		return nil, 0, nil
	}
	msg, err := c.Install("/usr/local/bin/team")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "already installed" {
		t.Fatalf("got %q", msg)
	}
	// Should NOT have called bootstrap again.
	for _, call := range r.calls {
		if len(call.args) > 0 && call.args[0] == "bootstrap" {
			t.Errorf("unexpected bootstrap on second install")
		}
	}
}

func TestInstall_ReloadsWhenPlistChanges(t *testing.T) {
	c, r := newCtl(t)
	// Initial install.
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		if args[0] == "print" {
			return []byte(""), 113, nil
		}
		return nil, 0, nil
	}
	if _, err := c.Install("/old/path/team"); err != nil {
		t.Fatal(err)
	}

	// Second install with different binary path AND service is "loaded"
	// → expect bootout-then-bootstrap.
	r.calls = nil
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		if args[0] == "print" {
			return []byte("state = running\npid = 999\n"), 0, nil
		}
		return nil, 0, nil
	}
	msg, err := c.Install("/new/path/team")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "reinstalled (plist updated)" {
		t.Fatalf("got %q", msg)
	}
	var sawBootout, sawBootstrap bool
	for _, call := range r.calls {
		if len(call.args) == 0 {
			continue
		}
		switch call.args[0] {
		case "bootout":
			sawBootout = true
		case "bootstrap":
			sawBootstrap = true
		}
	}
	if !sawBootout || !sawBootstrap {
		t.Errorf("expected bootout+bootstrap; calls=%v", r.calls)
	}
	// New plist on disk.
	body, _ := os.ReadFile(c.PlistPath())
	if !strings.Contains(string(body), "/new/path/team") {
		t.Errorf("plist not updated")
	}
}

func TestUninstall_RemovesPlist(t *testing.T) {
	c, r := newCtl(t)
	// Prime plist on disk.
	_ = os.MkdirAll(filepath.Dir(c.PlistPath()), 0o755)
	_ = os.WriteFile(c.PlistPath(), []byte("xxx"), 0o644)

	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		return nil, 0, nil
	}
	msg, err := c.Uninstall()
	if err != nil {
		t.Fatal(err)
	}
	if msg != "uninstalled" {
		t.Fatalf("got %q", msg)
	}
	if _, err := os.Stat(c.PlistPath()); !os.IsNotExist(err) {
		t.Fatal("plist should be gone")
	}
}

func TestUninstall_NotInstalled(t *testing.T) {
	c, r := newCtl(t)
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		// bootout: not loaded
		return []byte("Could not find service"), 113, nil
	}
	msg, err := c.Uninstall()
	if err != nil {
		t.Fatal(err)
	}
	if msg != "not installed" {
		t.Fatalf("got %q", msg)
	}
}

func TestStatus_NotLoaded(t *testing.T) {
	c, r := newCtl(t)
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		return []byte(""), 113, nil
	}
	st, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Loaded {
		t.Fatal("expected not loaded")
	}
	if st.State != "not loaded" {
		t.Fatalf("got state %q", st.State)
	}
}

func TestStatus_Loaded_ParsesPidAndState(t *testing.T) {
	c, r := newCtl(t)
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		return []byte(`gui/501/com.test.team = {
	state = running
	pid = 4321
	program = /usr/local/bin/team
}`), 0, nil
	}
	st, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Loaded {
		t.Fatal("expected loaded")
	}
	if st.State != "running" {
		t.Fatalf("got state %q", st.State)
	}
	if st.Pid != 4321 {
		t.Fatalf("got pid %d", st.Pid)
	}
}

func TestStart_Stop_Restart_BuildArgs(t *testing.T) {
	c, r := newCtl(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"kickstart", c.Target()},
		{"kill", "SIGTERM", c.Target()},
		{"kickstart", "-k", c.Target()},
	}
	if len(r.calls) != len(want) {
		t.Fatalf("got %d calls, want %d", len(r.calls), len(want))
	}
	for i, w := range want {
		got := r.calls[i].args
		if len(got) != len(w) {
			t.Errorf("call %d args: got %v, want %v", i, got, w)
			continue
		}
		for j := range w {
			if got[j] != w[j] {
				t.Errorf("call %d arg %d: got %q want %q", i, j, got[j], w[j])
			}
		}
	}
}

func TestStop_PropagatesError(t *testing.T) {
	c, r := newCtl(t)
	r.behavior = func(name string, args ...string) ([]byte, int, error) {
		return nil, 0, errors.New("launchctl missing")
	}
	if err := c.Stop(); err == nil {
		t.Fatal("expected error")
	}
}
