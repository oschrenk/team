package bus

import (
	"context"
	"fmt"
	"strings"

	"github.com/oschrenk/team/internal/logfile"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
	"github.com/oschrenk/team/internal/validate"
)

func (s *Server) handleList(ctx context.Context, state *clientState) {
	// Same liveness invariant as send/broadcast/rename: a control
	// whose listener has dropped shouldn't keep enumerating peers.
	var reject struct {
		code protocol.ErrorCode
		msg  string
	}
	var sessions []protocol.SessionInfo

	s.registryMu.Lock()
	if !s.listenerAliveLocked(state) {
		reject.code = protocol.ErrUnauthorized
		reject.msg = "listener no longer connected"
	} else {
		sessions = make([]protocol.SessionInfo, 0, len(s.registry))
		for _, c := range s.registry {
			if c.role != protocol.RoleAgent {
				continue
			}
			sessions = append(sessions, protocol.SessionInfo{
				SessionID: c.sessionID,
				Name:      c.name,
				Label:     c.label,
				Cwd:       c.cwd,
				Since:     c.since.UTC().Format(protocol.TimestampFormat),
			})
		}
	}
	s.registryMu.Unlock()

	if reject.code != "" {
		_ = s.sendError(ctx, state, reject.code, reject.msg)
		return
	}
	_ = s.writeFrame(ctx, state, protocol.ListOk{
		Op: protocol.OpListOk, Sessions: sessions,
	})
}

// resolveSendResult is the outcome of a target lookup. Either target
// is non-nil OR (code, msg, …) describes the rejection.
type resolveSendResult struct {
	target     *clientState
	code       protocol.ErrorCode
	msg        string
	matches    []string
	candidates []string
}

// resolveSendTarget mirrors server.py::_resolve_send_target precedence:
// exact session_id → exact name → unique name prefix → ≥4-char
// session_id prefix. Liveness check folded in so a stale control can't
// slip a message between the liveness check and the actual send.
func (s *Server) resolveSendTarget(state *clientState, target string) resolveSendResult {
	fromID := state.fromID()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if !s.listenerAliveLocked(state) {
		return resolveSendResult{
			code: protocol.ErrUnauthorized, msg: "listener no longer connected",
		}
	}
	// Collect agents into a stable slice once.
	agents := make([]*clientState, 0, len(s.registry))
	for _, c := range s.registry {
		if c.role == protocol.RoleAgent {
			agents = append(agents, c)
		}
	}
	// 1) exact session_id
	for _, c := range agents {
		if c.sessionID == target {
			if c.sessionID == fromID {
				return resolveSendResult{
					code: protocol.ErrUnknownPeer, msg: "cannot send to self",
				}
			}
			return resolveSendResult{target: c}
		}
	}
	// 2) exact name
	for _, c := range agents {
		if c.name != "" && c.name == target {
			if c.sessionID == fromID {
				return resolveSendResult{
					code: protocol.ErrUnknownPeer, msg: "cannot send to self",
				}
			}
			return resolveSendResult{target: c}
		}
	}
	// 3) name prefix (unique)
	if target != "" {
		var prefix []*clientState
		for _, c := range agents {
			if c.name != "" && strings.HasPrefix(c.name, target) {
				prefix = append(prefix, c)
			}
		}
		if len(prefix) == 1 {
			if prefix[0].sessionID == fromID {
				return resolveSendResult{
					code: protocol.ErrUnknownPeer, msg: "cannot send to self",
				}
			}
			return resolveSendResult{target: prefix[0]}
		}
		if len(prefix) > 1 {
			names := make([]string, len(prefix))
			for i, c := range prefix {
				names[i] = c.name
			}
			return resolveSendResult{
				code:    protocol.ErrAmbiguous,
				msg:     fmt.Sprintf("ambiguous prefix %q", target),
				matches: names,
			}
		}
	}
	// 4) session_id prefix, ≥4 chars (unique)
	if len(target) >= 4 {
		var sidPrefix []*clientState
		for _, c := range agents {
			if strings.HasPrefix(c.sessionID, target) {
				sidPrefix = append(sidPrefix, c)
			}
		}
		if len(sidPrefix) == 1 {
			if sidPrefix[0].sessionID == fromID {
				return resolveSendResult{
					code: protocol.ErrUnknownPeer, msg: "cannot send to self",
				}
			}
			return resolveSendResult{target: sidPrefix[0]}
		}
		if len(sidPrefix) > 1 {
			short := make([]string, len(sidPrefix))
			for i, c := range sidPrefix {
				short[i] = c.sessionID[:8]
			}
			return resolveSendResult{
				code:    protocol.ErrAmbiguous,
				msg:     fmt.Sprintf("ambiguous session_id prefix %q", target),
				matches: short,
			}
		}
	}
	return resolveSendResult{
		code: protocol.ErrUnknownPeer, msg: fmt.Sprintf("no agent matches %q", target),
	}
}

func (s *Server) handleSend(ctx context.Context, state *clientState, p protocol.Send) {
	if len(p.Text) > protocol.TextCap {
		s.counters.MsgsRejected.Add(1)
		_ = s.sendError(ctx, state, protocol.ErrTextTooLong, "text exceeds direct send cap")
		return
	}
	res := s.resolveSendTarget(state, p.To)
	if res.target == nil {
		s.counters.MsgsRejected.Add(1)
		_ = s.sendErrorWith(ctx, state, res.code, res.msg, res.candidates, res.matches)
		return
	}
	msg := protocol.Msg{
		Op:          protocol.OpMsg,
		MsgID:       s.msgID(),
		From:        state.fromID(),
		FromName:    state.name,
		FromLabel:   state.label,
		To:          res.target.name,
		ToSessionID: res.target.sessionID,
		Text:        p.Text,
		Ts:          s.nowTimestamp(),
	}
	s.logMessage(msg, "direct")
	s.safeWrite(ctx, res.target, msg)
	s.counters.MsgsSent.Add(1)
}

func (s *Server) handleBroadcast(ctx context.Context, state *clientState, p protocol.Broadcast) {
	if len(p.Text) > protocol.BroadcastTextCap {
		s.counters.MsgsRejected.Add(1)
		_ = s.sendError(ctx, state, protocol.ErrTextTooLong, "text exceeds broadcast cap")
		return
	}
	fromID := state.fromID()

	// Liveness + targets snapshot (under lock).
	var (
		liveOK  bool
		targets []*clientState
	)
	s.registryMu.Lock()
	liveOK = s.listenerAliveLocked(state)
	if liveOK {
		for _, c := range s.registry {
			if c.role == protocol.RoleAgent && c.sessionID != fromID {
				targets = append(targets, c)
			}
		}
	}
	s.registryMu.Unlock()
	if !liveOK {
		s.counters.MsgsRejected.Add(1)
		_ = s.sendError(ctx, state, protocol.ErrUnauthorized, "listener no longer connected")
		return
	}
	// Charge rate-limit AFTER liveness so dead-listener attempts don't
	// consume quota.
	if !s.consumeBroadcastQuota(fromID) {
		s.counters.MsgsRejected.Add(1)
		_ = s.sendError(ctx, state, protocol.ErrRateLimited, "broadcast rate limit exceeded")
		return
	}
	msg := protocol.Msg{
		Op:        protocol.OpMsg,
		MsgID:     s.msgID(),
		From:      fromID,
		FromName:  state.name,
		FromLabel: state.label,
		Text:      p.Text,
		Ts:        s.nowTimestamp(),
	}
	s.logMessage(msg, "broadcast")
	for _, t := range targets {
		s.safeWrite(ctx, t, msg)
	}
	s.counters.MsgsBroadcast.Add(1)
}

func (s *Server) handleRename(ctx context.Context, state *clientState, p protocol.Rename) {
	if p.Name != "" && !validate.Name(p.Name) {
		_ = s.sendError(ctx, state, protocol.ErrInvalidName, "invalid name")
		return
	}
	targetSID := state.fromID()
	var reject struct {
		code protocol.ErrorCode
		msg  string
	}
	s.registryMu.Lock()
	target, ok := s.registry[targetSID]
	if !ok || target.role != protocol.RoleAgent {
		reject.code = protocol.ErrUnknownPeer
		reject.msg = "no listener to rename"
	} else if p.Name != "" {
		for _, c := range s.registry {
			if c.sessionID != targetSID && c.role == protocol.RoleAgent && c.name == p.Name {
				reject.code = protocol.ErrNameTaken
				reject.msg = fmt.Sprintf("name %q is taken", p.Name)
				break
			}
		}
	}
	if reject.code == "" {
		target.name = p.Name
		if state != target {
			state.name = p.Name
		}
	}
	s.registryMu.Unlock()
	if reject.code != "" {
		_ = s.sendError(ctx, state, reject.code, reject.msg)
		return
	}
	_ = s.writeFrame(ctx, state, protocol.Renamed{Op: protocol.OpRenamed, Name: p.Name})
	s.broadcastEvent(protocol.Renamed{
		Op: protocol.OpRenamed, SessionID: targetSID, Name: p.Name,
	}, targetSID)
}

// logMessage appends a JSONL record for every direct or broadcast msg.
// Best-effort: log failures are intentionally swallowed.
func (s *Server) logMessage(msg protocol.Msg, kind string) {
	_ = paths.SecureDir(paths.DataDir())
	path := paths.MessagesLogPath()
	_ = logfile.Rotate(path, protocol.MessagesLogMaxBytes, protocol.MessagesLogBackups)

	rec := struct {
		Ts          string `json:"ts"`
		MsgID       string `json:"msg_id"`
		Kind        string `json:"kind"`
		From        string `json:"from"`
		FromName    string `json:"from_name"`
		FromLabel   string `json:"from_label,omitempty"`
		To          string `json:"to,omitempty"`
		ToSessionID string `json:"to_session_id,omitempty"`
		Text        string `json:"text"`
	}{
		Ts: msg.Ts, MsgID: msg.MsgID, Kind: kind,
		From: msg.From, FromName: msg.FromName, FromLabel: msg.FromLabel,
		To: msg.To, ToSessionID: msg.ToSessionID, Text: msg.Text,
	}
	_ = logfile.AppendJSONL(path, rec, 0o600)
}
