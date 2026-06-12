package client

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/procutil"
)

// SessionState is the on-disk record helper CLIs (`team send`, `team
// list`, etc.) read to find their listener.
type SessionState struct {
	SessionID   string `json:"session_id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Token       string `json:"token"`
	Nonce       string `json:"nonce"`
	ListenerPID int    `json:"listener_pid"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	CreatedAt   string `json:"created_at"`
}

// WriteSessionState atomically writes state for ppid. Helpers reading
// the file are guaranteed to see either the previous state or the new
// one, never a partial write. Mirrors client.py::_write_session_state.
func WriteSessionState(ppid int, st SessionState) error {
	if err := paths.SecureDir(paths.ClientsDir()); err != nil {
		return err
	}
	finalPath := paths.ClientSessionPath(ppid)
	dir := filepath.Dir(finalPath)
	tmp, err := os.CreateTemp(dir, filepath.Base(finalPath)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

// ReadSessionState returns the state file for ppid, or (nil, nil) if
// it doesn't exist. Used by both the client (to surface "already
// running" diagnostics) and the helper CLIs (TEAM-007).
func ReadSessionState(ppid int) (*SessionState, error) {
	data, err := os.ReadFile(paths.ClientSessionPath(ppid))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var st SessionState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// DeleteSessionState removes the file. Best-effort: missing → no error.
func DeleteSessionState(ppid int) error {
	err := os.Remove(paths.ClientSessionPath(ppid))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// FindListenerState returns the local listener's session state for
// helper CLIs. Resolves the ppid key the same way the listener writer
// does (see procutil.ResolveListenerKey), then reads
// <clients-dir>/<ppid>.session.
//
// Returns (nil, path, nil) if the file is missing — caller should
// treat that as "not connected".
func FindListenerState() (*SessionState, string, error) {
	ppid := procutil.ResolveListenerKey()
	path := paths.ClientSessionPath(ppid)
	st, err := ReadSessionState(ppid)
	return st, path, err
}

// UnlinkIfMatches removes the per-session state file iff it still
// matches `want` (same session_id + nonce). TOCTOU-safe: re-reads
// before unlinking so a fresh listener's state isn't stomped on.
//
// Used by helpers when the server returns unknown_peer / unauthorized,
// indicating the listener referenced in the state file is no longer
// alive.
func UnlinkIfMatches(path string, want SessionState) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var current SessionState
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.SessionID == want.SessionID && current.Nonce == want.Nonce {
		return os.Remove(path)
	}
	return nil
}
