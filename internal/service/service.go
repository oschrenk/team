// Package service owns the launchd user-agent that runs the team
// server. On macOS personal-use systems, launchd is the lifecycle owner
// (not self-spawn, like the Python upstream).
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Label is the launchd job label. Also the basename of the plist.
const Label = "com.oschrenk.team"

// Controller is the test seam: real builds run launchctl; tests inject
// a fake.
type Controller struct {
	Label string
	UID   int

	// Run executes a command and returns stdout, exit code, error.
	// Default uses os/exec; tests override.
	Run func(name string, args ...string) (stdout []byte, exitCode int, err error)

	// HomeDir is the user's home directory. Tests override.
	HomeDir string
}

// New builds a Controller with production defaults.
func New() (*Controller, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Controller{
		Label:   Label,
		UID:     os.Getuid(),
		HomeDir: home,
		Run:     defaultRun,
	}, nil
}

func defaultRun(name string, args ...string) ([]byte, int, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, exitErr.ExitCode(), nil
	}
	return out, -1, err
}

// PlistPath is where `service install` writes the agent plist.
func (c *Controller) PlistPath() string {
	return filepath.Join(c.HomeDir, "Library", "LaunchAgents", c.Label+".plist")
}

// LogPath is where launchd writes the server's stdout/stderr.
func (c *Controller) LogPath() string {
	return filepath.Join(c.HomeDir, "Library", "Logs", "team", "server.log")
}

// Target is the launchd service identifier (gui/<uid>/<label>).
func (c *Controller) Target() string {
	return fmt.Sprintf("gui/%d/%s", c.UID, c.Label)
}

// Domain is the user GUI domain (gui/<uid>).
func (c *Controller) Domain() string {
	return fmt.Sprintf("gui/%d", c.UID)
}

// --- plist rendering --------------------------------------------------

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>server</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>ProcessType</key>
	<string>Background</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>%s</string>
	</dict>
</dict>
</plist>
`

// RenderPlist builds the plist body for this Controller given a binary
// path. XML-special chars in inputs are escaped.
func (c *Controller) RenderPlist(binaryPath string) string {
	return fmt.Sprintf(plistTemplate,
		escapeXML(c.Label),
		escapeXML(binaryPath),
		escapeXML(c.LogPath()),
		escapeXML(c.LogPath()),
		escapeXML(c.HomeDir),
	)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// --- status -----------------------------------------------------------

// Status is the public service state.
type Status struct {
	PlistInstalled bool
	Loaded         bool
	State          string // "running", "spawning", "not loaded", "" if unknown
	Pid            int
}

// Status queries launchd for service state.
func (c *Controller) Status() (Status, error) {
	st := Status{
		PlistInstalled: fileExists(c.PlistPath()),
	}
	out, code, err := c.Run("launchctl", "print", c.Target())
	if err != nil {
		return st, err
	}
	if code != 0 {
		// Not loaded — `print` returns non-zero (typically 113 on macOS).
		st.State = "not loaded"
		return st, nil
	}
	st.Loaded = true
	st.State, st.Pid = parseLaunchctlPrint(string(out))
	return st, nil
}

// parseLaunchctlPrint scrapes `state = X` and `pid = N` lines out of
// `launchctl print` output. Best-effort.
func parseLaunchctlPrint(body string) (state string, pid int) {
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if k, v, ok := splitKV(l); ok {
			switch k {
			case "state":
				state = v
			case "pid":
				if p, err := strconv.Atoi(v); err == nil {
					pid = p
				}
			}
		}
	}
	return
}

func splitKV(line string) (key, val string, ok bool) {
	// Lines look like:  "state = running"
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])
	return key, val, key != "" && val != ""
}

// --- install / uninstall / lifecycle ----------------------------------

// Install writes the plist (if needed) and `launchctl bootstrap`s it.
// Idempotent: a no-op if the service is already loaded with the
// expected plist content.
//
// Returns a human-friendly status string for the CLI to surface.
func (c *Controller) Install(binaryPath string) (string, error) {
	wantBody := c.RenderPlist(binaryPath)

	// Make sure log dir exists.
	if err := os.MkdirAll(filepath.Dir(c.LogPath()), 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	// Plist write (idempotent).
	plistPath := c.PlistPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	existing, _ := os.ReadFile(plistPath)
	plistChanged := string(existing) != wantBody
	if plistChanged {
		if err := os.WriteFile(plistPath, []byte(wantBody), 0o644); err != nil {
			return "", fmt.Errorf("write plist: %w", err)
		}
	}

	// Bootstrap. If already loaded, bootout-bootstrap to pick up changes.
	st, _ := c.Status()
	if st.Loaded {
		if !plistChanged {
			return "already installed", nil
		}
		// Reload to pick up plist changes.
		if _, _, err := c.Run("launchctl", "bootout", c.Target()); err != nil {
			return "", fmt.Errorf("bootout for reload: %w", err)
		}
	}
	out, code, err := c.Run("launchctl", "bootstrap", c.Domain(), plistPath)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("bootstrap exited %d: %s", code, strings.TrimSpace(string(out)))
	}
	if plistChanged && st.Loaded {
		return "reinstalled (plist updated)", nil
	}
	return "installed", nil
}

// Uninstall boots the service out and removes the plist. Best-effort.
func (c *Controller) Uninstall() (string, error) {
	out, code, err := c.Run("launchctl", "bootout", c.Target())
	if err != nil {
		return "", err
	}
	// code 113 = not loaded; that's fine, we still want to remove plist.
	plistPath := c.PlistPath()
	rmErr := os.Remove(plistPath)
	switch {
	case code == 0 && rmErr == nil:
		return "uninstalled", nil
	case code != 0 && os.IsNotExist(rmErr):
		return "not installed", nil
	case code == 0 && os.IsNotExist(rmErr):
		return "uninstalled (plist was missing)", nil
	case code != 0 && rmErr == nil:
		return "uninstalled (was not loaded; plist removed)", nil
	default:
		return "", fmt.Errorf("bootout exited %d (%s); rm: %v", code, strings.TrimSpace(string(out)), rmErr)
	}
}

// Start asks launchd to kickstart the service.
func (c *Controller) Start() error {
	out, code, err := c.Run("launchctl", "kickstart", c.Target())
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("kickstart exited %d: %s", code, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop sends SIGTERM via launchd. The service comes back up if
// KeepAlive=true, which is our default.
func (c *Controller) Stop() error {
	out, code, err := c.Run("launchctl", "kill", "SIGTERM", c.Target())
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("kill exited %d: %s", code, strings.TrimSpace(string(out)))
	}
	return nil
}

// Restart = kickstart -k (kill then restart).
func (c *Controller) Restart() error {
	out, code, err := c.Run("launchctl", "kickstart", "-k", c.Target())
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("kickstart -k exited %d: %s", code, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
