// Package bus implements the team WebSocket bus server: registry,
// hello/dispatch handlers, broadcast rate limit, message logging.
//
// Lifecycle is owned by the caller (a foreground `team server` process
// managed by launchd; see TEAM-005). The server has no idle-shutdown
// timer.
package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

// Options configures a Server. Zero values are filled with sensible
// defaults by New(); tests override clock/UUID/msg-id for determinism
// and Token to skip disk I/O.
type Options struct {
	Host  string
	Port  int
	Token string

	Now       func() time.Time
	UUIDFunc  func() string
	MsgIDFunc func() string
}

// Counters are exposed for TEAM-008's /api/stats endpoint.
type Counters struct {
	MsgsSent        atomic.Int64
	MsgsBroadcast   atomic.Int64
	MsgsRejected    atomic.Int64
	PeerJoinedTotal atomic.Int64
	PeerLeftTotal   atomic.Int64
}

// Server owns the registry of connected clients.
type Server struct {
	host  string
	port  int
	token string

	now   func() time.Time
	uuid  func() string
	msgID func() string

	registryMu sync.Mutex
	registry   map[string]*clientState // keyed by session_id

	broadcastMu      sync.Mutex
	broadcastWindows map[string][]time.Time // keyed by listener session_id

	counters  Counters
	startedAt time.Time

	httpServer *http.Server
}

// clientState is the server's view of one connected client.
type clientState struct {
	sessionID string
	role      protocol.Role
	name      string
	label     string
	cwd       string
	pid       int
	nonce     string
	// forSession is the listener's session_id for control connections;
	// empty for agents. Used by `_from_id_for` semantics.
	forSession string

	conn    *websocket.Conn
	writeMu sync.Mutex
	since   time.Time
}

// fromID matches shared.py::_from_id_for: a control acts on behalf of
// its listener, so its message metadata reports the listener's sid.
func (c *clientState) fromID() string {
	if c.role == protocol.RoleControl && c.forSession != "" {
		return c.forSession
	}
	return c.sessionID
}

// New builds a Server. If opts.Token is empty, EnsureToken is called
// against the configured data dir.
func New(opts Options) (*Server, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = protocol.DefaultPort
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.UUIDFunc == nil {
		opts.UUIDFunc = newUUID
	}
	if opts.MsgIDFunc == nil {
		opts.MsgIDFunc = newMsgID
	}
	if opts.Token == "" {
		if err := paths.SecureDir(paths.DataDir()); err != nil {
			return nil, err
		}
		tok, err := auth.EnsureToken(paths.TokenPath())
		if err != nil {
			return nil, err
		}
		opts.Token = tok
	}
	return &Server{
		host:             opts.Host,
		port:             opts.Port,
		token:            opts.Token,
		now:              opts.Now,
		uuid:             opts.UUIDFunc,
		msgID:            opts.MsgIDFunc,
		registry:         make(map[string]*clientState),
		broadcastWindows: make(map[string][]time.Time),
		startedAt:        opts.Now(),
	}, nil
}

// Token returns the bearer token (for the inspect API to authenticate
// against in TEAM-008). Callers should treat it as opaque.
func (s *Server) Token() string { return s.token }

// StartedAt is exposed for /api/stats uptime calculation.
func (s *Server) StartedAt() time.Time { return s.startedAt }

// Stats returns a snapshot of current counters plus connected count.
type Stats struct {
	UptimeSeconds            float64 `json:"uptime_s"`
	Connected                int     `json:"connected"`
	MsgsSent                 int64   `json:"msgs_sent"`
	MsgsBroadcast            int64   `json:"msgs_broadcast"`
	MsgsRejected             int64   `json:"msgs_rejected"`
	PeerJoinedTotal          int64   `json:"peer_joined_total"`
	PeerLeftTotal            int64   `json:"peer_left_total"`
	BroadcastRateLimitPerMin int     `json:"broadcast_rate_limit_per_min"`
}

func (s *Server) Stats() Stats {
	s.registryMu.Lock()
	connected := 0
	for _, c := range s.registry {
		if c.role == protocol.RoleAgent {
			connected++
		}
	}
	s.registryMu.Unlock()
	return Stats{
		UptimeSeconds:            s.now().Sub(s.startedAt).Seconds(),
		Connected:                connected,
		MsgsSent:                 s.counters.MsgsSent.Load(),
		MsgsBroadcast:            s.counters.MsgsBroadcast.Load(),
		MsgsRejected:             s.counters.MsgsRejected.Load(),
		PeerJoinedTotal:          s.counters.PeerJoinedTotal.Load(),
		PeerLeftTotal:            s.counters.PeerLeftTotal.Load(),
		BroadcastRateLimitPerMin: protocol.BroadcastRateLimitPerMin,
	}
}

// RegistrySnapshot returns the current list of registered agents.
// Controls are excluded (per Python role-split: only agents appear in
// list output).
func (s *Server) RegistrySnapshot() []protocol.SessionInfo {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	out := make([]protocol.SessionInfo, 0, len(s.registry))
	for _, c := range s.registry {
		if c.role != protocol.RoleAgent {
			continue
		}
		out = append(out, protocol.SessionInfo{
			SessionID: c.sessionID,
			Name:      c.name,
			Label:     c.label,
			Cwd:       c.cwd,
			Since:     c.since.UTC().Format(protocol.TimestampFormat),
		})
	}
	return out
}

// newUUID generates a RFC 4122 v4 UUID without pulling in a third-party
// dep. Format is the canonical 8-4-4-4-12 hex.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// newMsgID returns 8 lowercase hex chars. Matches uuid4().hex[:8] in
// the Python reference.
func newMsgID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// nowTimestamp formats the current time as RFC3339Nano UTC.
func (s *Server) nowTimestamp() string {
	return s.now().UTC().Format(protocol.TimestampFormat)
}

// listenerAliveLocked reports whether a client whose listener has
// dropped should be allowed to act. Agents are always "alive" while
// their handler runs. Controls are alive iff their listener is still
// in the registry. Caller must hold registryMu.
func (s *Server) listenerAliveLocked(c *clientState) bool {
	if c.role != protocol.RoleControl {
		return true
	}
	if c.forSession == "" {
		return false
	}
	_, ok := s.registry[c.forSession]
	return ok
}

// consumeBroadcastQuota reports whether the listener whose id is
// listenerSID has room in its 60-per-minute window. Charges one slot
// on success.
func (s *Server) consumeBroadcastQuota(listenerSID string) bool {
	now := s.now()
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
	w := s.broadcastWindows[listenerSID]
	// Prune expired entries.
	cutoff := now.Add(-time.Minute)
	keep := w[:0]
	for _, t := range w {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= protocol.BroadcastRateLimitPerMin {
		s.broadcastWindows[listenerSID] = keep
		return false
	}
	keep = append(keep, now)
	s.broadcastWindows[listenerSID] = keep
	return true
}

// Stop shuts the HTTP server down gracefully.
func (s *Server) Stop() {
	if s.httpServer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.httpServer.Shutdown(ctx)
}
