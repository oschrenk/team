// Package paths centralizes every on-disk location the team binary uses.
//
// All paths are derived from a single DataDir() root so tests can swap it
// via the TEAM_DATA_DIR env var.
package paths

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// DataDir returns the root data directory for team state. TEAM_DATA_DIR
// overrides; default is ~/.claude/data/team.
func DataDir() string {
	if env := os.Getenv("TEAM_DATA_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".claude", "data", "team")
}

func TokenPath() string       { return filepath.Join(DataDir(), "token") }
func ServerLogPath() string   { return filepath.Join(DataDir(), "server.log") }
func MessagesLogPath() string { return filepath.Join(DataDir(), "messages.log") }
func ClientsDir() string      { return filepath.Join(DataDir(), "clients") }

func ClientLockPath(ppid int) string {
	return filepath.Join(ClientsDir(), strconv.Itoa(ppid)+".lock")
}

func ClientSessionPath(ppid int) string {
	return filepath.Join(ClientsDir(), strconv.Itoa(ppid)+".session")
}

// identityStem mirrors shared.py::_identity_stem. Default host
// (127.0.0.1) keeps the legacy "server.<port>" stem for compatibility.
func identityStem(port int, host string) string {
	if host == "" || host == "127.0.0.1" {
		return "server." + strconv.Itoa(port)
	}
	return "server." + url.PathEscape(host) + "." + strconv.Itoa(port)
}

func PidfilePath(port int, host string) string {
	return filepath.Join(DataDir(), identityStem(port, host)+".pid")
}

func PidfileMetaPath(port int, host string) string {
	return filepath.Join(DataDir(), identityStem(port, host)+".pid.meta")
}

// SecureDir creates path (if needed) and forces mode 0700.
func SecureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// SecureFile forces mode 0600 on an existing file.
func SecureFile(path string) error {
	return os.Chmod(path, 0o600)
}
