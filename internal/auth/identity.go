package auth

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/process"

	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/procutil"
)

// CmdlineFetcher returns the command-line of a running process.
// Injected as a seam so tests can verify identity without spawning
// a real `team server`.
type CmdlineFetcher func(pid int) ([]string, error)

// DefaultCmdlineFetcher uses gopsutil. Test code may swap this var or
// call verifyServerIdentity directly.
var DefaultCmdlineFetcher CmdlineFetcher = gopsutilCmdline

func gopsutilCmdline(pid int) ([]string, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, err
	}
	return p.CmdlineSlice()
}

type identityMeta struct {
	Pid  int    `json:"pid"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// WriteServerIdentity publishes <pid, host, port> to the endpoint-scoped
// pidfile + .meta. Both files are written with mode 0600.
//
// Server's responsibility is to call this BEFORE accepting connections,
// so clients always see a valid identity by the time they can dial.
func WriteServerIdentity(pid int, host string, port int) error {
	pidPath := paths.PidfilePath(port, host)
	metaPath := paths.PidfileMetaPath(port, host)

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	if err := paths.SecureFile(pidPath); err != nil {
		return err
	}

	hostNorm := host
	if hostNorm == "" {
		hostNorm = "127.0.0.1"
	}
	data, err := json.Marshal(identityMeta{Pid: pid, Host: hostNorm, Port: port})
	if err != nil {
		return err
	}
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		return err
	}
	return paths.SecureFile(metaPath)
}

// VerifyServerIdentity returns true iff a live process whose cmdline
// contains "team server" owns (host, port) according to the pidfile +
// .meta files. Defense-in-depth against localhost port-squatters.
//
// Failure modes (all return false):
//   - missing pidfile
//   - unreadable / malformed pid
//   - dead pid
//   - missing/mismatched .meta
//   - cmdline doesn't include "team server"
//
// Soft-trust: if the cmdline fetcher errors (e.g. process briefly
// inaccessible), we trust the endpoint metadata we already verified.
func VerifyServerIdentity(host string, port int) bool {
	return verifyServerIdentity(host, port, DefaultCmdlineFetcher)
}

func verifyServerIdentity(host string, port int, fetch CmdlineFetcher) bool {
	pidPath := paths.PidfilePath(port, host)
	metaPath := paths.PidfileMetaPath(port, host)

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pid <= 0 {
		return false
	}
	if !procutil.SafePidAlive(pid) {
		return false
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	var meta identityMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return false
	}
	if meta.Pid != pid || meta.Port != port {
		return false
	}
	wantHost := host
	if wantHost == "" {
		wantHost = "127.0.0.1"
	}
	if meta.Host != wantHost {
		return false
	}

	cmdline, err := fetch(pid)
	if err != nil {
		// Soft-trust: endpoint metadata already verified above.
		return true
	}
	return strings.Contains(strings.Join(cmdline, " "), "team server")
}
