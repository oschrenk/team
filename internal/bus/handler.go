package bus

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/protocol"
	"github.com/oschrenk/team/internal/validate"
)

const (
	helloTimeout = 10 * time.Second
	writeTimeout = 5 * time.Second
)

// Serve runs the HTTP server on l. Blocks until Stop is called or the
// listener errors.
//
// extraMounts lets callers (e.g. cmd/server.go) add additional handlers
// to the same mux before serving — used by TEAM-008's inspect API. This
// avoids a circular import from internal/bus → internal/inspect; the
// composition happens in package cmd instead.
func (s *Server) Serve(l net.Listener, extraMounts ...func(*http.ServeMux)) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)
	for _, m := range extraMounts {
		m(mux)
	}

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  0, // WS connections may be long-lived
		WriteTimeout: 0,
	}
	err := s.httpServer.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Bus is localhost-only; bearer-token is the real auth gate.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.WSFrameCap)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	state, ok := s.doHello(ctx, conn)
	if !ok {
		return
	}
	defer s.unregister(state)

	s.dispatchLoop(ctx, state)
}

// doHello reads the first frame, validates it, and registers the
// client. Returns (state, true) on success; (nil, false) on any error
// (the client receives an error frame before false is returned).
func (s *Server) doHello(ctx context.Context, conn *websocket.Conn) (*clientState, bool) {
	helloCtx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()

	_, raw, err := conn.Read(helloCtx)
	if err != nil {
		return nil, false
	}
	op, err := protocol.PeekOp(raw)
	if err != nil {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrUnknownOp, "malformed JSON")
		return nil, false
	}
	if op != protocol.OpHello {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrUnknownOp, "first frame must be hello")
		return nil, false
	}
	var hello protocol.Hello
	if err := json.Unmarshal(raw, &hello); err != nil {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrInvalidPayload, "frame must be a JSON object")
		return nil, false
	}
	// Constant-time token compare.
	if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(s.token)) != 1 {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrUnauthorized, "bad token")
		return nil, false
	}
	if hello.Name != "" && !validate.Name(hello.Name) {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrInvalidName, "invalid name")
		return nil, false
	}
	if !validate.Label(hello.Label) {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrInvalidLabel, "invalid label")
		return nil, false
	}

	switch hello.Role {
	case "", protocol.RoleAgent:
		return s.helloAgent(ctx, conn, hello)
	case protocol.RoleControl:
		return s.helloControl(ctx, conn, hello)
	default:
		_ = sendErrorOnConn(ctx, conn, protocol.ErrInvalidPayload, "bad role")
		return nil, false
	}
}

func (s *Server) helloAgent(ctx context.Context, conn *websocket.Conn, hello protocol.Hello) (*clientState, bool) {
	sid := hello.SessionID
	if sid == "" {
		sid = s.uuid()
	}
	cwd := validate.SanitizeForStdout(hello.Cwd)
	if len(cwd) > 256 {
		cwd = cwd[:256]
	}

	var oldConn *websocket.Conn
	var rejectCode protocol.ErrorCode
	var rejectMsg string
	var rejectCandidates []string

	state := &clientState{
		sessionID:  sid,
		role:       protocol.RoleAgent,
		name:       hello.Name,
		label:      validate.NormalizeLabel(hello.Label),
		cwd:        cwd,
		pid:        hello.Pid,
		nonce:      hello.Nonce,
		conn:       conn,
		since:      s.now(),
	}

	s.registryMu.Lock()
	if existing, ok := s.registry[sid]; ok {
		// Reconnect-replace requires proving continuity via nonce —
		// otherwise session_id (which leaks through list_ok) becomes
		// an impersonation vector.
		if hello.Nonce == "" || hello.Nonce != existing.nonce {
			rejectCode = protocol.ErrUnauthorized
			rejectMsg = "session_id is in use by another connection"
		} else {
			oldConn = existing.conn
			delete(s.registry, sid)
		}
	}
	if rejectCode == "" && hello.Name != "" {
		for _, c := range s.registry {
			if c.role == protocol.RoleAgent && c.name == hello.Name {
				rejectCode = protocol.ErrNameTaken
				rejectMsg = fmt.Sprintf("name %q is taken", hello.Name)
				rejectCandidates = []string{
					hello.Name + "-2", hello.Name + "-3", hello.Name + "-4",
				}
				break
			}
		}
	}
	if rejectCode == "" {
		s.registry[sid] = state
	}
	s.registryMu.Unlock()

	if rejectCode != "" {
		s.counters.MsgsRejected.Add(1)
		_ = sendErrorOnConnWith(ctx, conn, rejectCode, rejectMsg, rejectCandidates, nil)
		return nil, false
	}
	if oldConn != nil {
		// Close blocks waiting for the peer's close ack, but the
		// replaced peer is no longer being read from — close in a
		// goroutine so the new conn's welcome isn't stalled.
		go func(c *websocket.Conn) {
			_ = c.Close(websocket.StatusCode(4000), "replaced")
		}(oldConn)
	}

	welcome := protocol.Welcome{
		Op:           protocol.OpWelcome,
		SessionID:    sid,
		AssignedName: state.name,
	}
	if !s.writeFrame(ctx, state, welcome) {
		return nil, false
	}
	s.counters.PeerJoinedTotal.Add(1)
	s.broadcastEvent(protocol.PeerJoined{
		Op: protocol.OpPeerJoined, SessionID: sid, Name: state.name,
	}, sid)
	return state, true
}

func (s *Server) helloControl(ctx context.Context, conn *websocket.Conn, hello protocol.Hello) (*clientState, bool) {
	if hello.ForSession == "" {
		_ = sendErrorOnConn(ctx, conn, protocol.ErrInvalidPayload, "for_session required for control role")
		return nil, false
	}
	var state *clientState

	s.registryMu.Lock()
	listener, ok := s.registry[hello.ForSession]
	if !ok || listener.role != protocol.RoleAgent {
		s.registryMu.Unlock()
		_ = sendErrorOnConn(ctx, conn, protocol.ErrUnknownPeer, fmt.Sprintf("no listener for %q", hello.ForSession))
		return nil, false
	}
	if hello.Nonce == "" || hello.Nonce != listener.nonce {
		s.registryMu.Unlock()
		_ = sendErrorOnConn(ctx, conn, protocol.ErrUnauthorized, "stale listener state; reconnect")
		return nil, false
	}
	state = &clientState{
		sessionID:  s.uuid(),
		role:       protocol.RoleControl,
		name:       listener.name,
		label:      listener.label,
		cwd:        listener.cwd,
		pid:        hello.Pid,
		nonce:      hello.Nonce,
		forSession: hello.ForSession,
		conn:       conn,
		since:      s.now(),
	}
	s.registry[state.sessionID] = state
	s.registryMu.Unlock()

	welcome := protocol.Welcome{
		Op:           protocol.OpWelcome,
		SessionID:    state.sessionID,
		AssignedName: state.name,
		ForSession:   hello.ForSession,
	}
	if !s.writeFrame(ctx, state, welcome) {
		return nil, false
	}
	return state, true
}

func (s *Server) dispatchLoop(ctx context.Context, state *clientState) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, s.readTimeout)
		_, raw, err := state.conn.Read(readCtx)
		cancel()
		if err != nil {
			return
		}
		op, err := protocol.PeekOp(raw)
		if err != nil {
			_ = s.sendError(ctx, state, protocol.ErrUnknownOp, "malformed JSON")
			continue
		}
		switch op {
		case protocol.OpPing:
			_ = s.writeFrame(ctx, state, protocol.Pong{Op: protocol.OpPong})
		case protocol.OpList:
			s.handleList(ctx, state)
		case protocol.OpSend:
			var p protocol.Send
			if err := json.Unmarshal(raw, &p); err != nil {
				_ = s.sendError(ctx, state, protocol.ErrInvalidPayload, "frame must be a JSON object")
				continue
			}
			s.handleSend(ctx, state, p)
		case protocol.OpBroadcast:
			var p protocol.Broadcast
			if err := json.Unmarshal(raw, &p); err != nil {
				_ = s.sendError(ctx, state, protocol.ErrInvalidPayload, "frame must be a JSON object")
				continue
			}
			s.handleBroadcast(ctx, state, p)
		case protocol.OpRename:
			var p protocol.Rename
			if err := json.Unmarshal(raw, &p); err != nil {
				_ = s.sendError(ctx, state, protocol.ErrInvalidPayload, "frame must be a JSON object")
				continue
			}
			s.handleRename(ctx, state, p)
		case protocol.OpBye:
			return
		case protocol.OpHello:
			_ = s.sendError(ctx, state, protocol.ErrUnknownOp, "duplicate hello")
		default:
			_ = s.sendError(ctx, state, protocol.ErrUnknownOp, fmt.Sprintf("unknown op %q", op))
		}
	}
}

func (s *Server) unregister(state *clientState) {
	var shouldAnnounce bool
	s.registryMu.Lock()
	current, ok := s.registry[state.sessionID]
	if ok && current == state {
		delete(s.registry, state.sessionID)
		shouldAnnounce = state.role == protocol.RoleAgent
	}
	s.registryMu.Unlock()
	if shouldAnnounce {
		s.counters.PeerLeftTotal.Add(1)
		s.broadcastEvent(protocol.PeerLeft{
			Op: protocol.OpPeerLeft, SessionID: state.sessionID,
		}, state.sessionID)
	}
}

// --- write helpers -----------------------------------------------------

// writeFrame serializes v to JSON and sends it to state with timeout.
// Returns false on any error (caller should bail out).
func (s *Server) writeFrame(ctx context.Context, state *clientState, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return state.conn.Write(wctx, websocket.MessageText, data) == nil
}

// safeWrite is the fan-out variant: same as writeFrame but never blocks
// the caller's flow.
func (s *Server) safeWrite(ctx context.Context, state *clientState, v any) {
	_ = s.writeFrame(ctx, state, v)
}

// sendError sends an Error frame to state.
func (s *Server) sendError(ctx context.Context, state *clientState, code protocol.ErrorCode, message string) error {
	if !s.writeFrame(ctx, state, protocol.Error{
		Op: protocol.OpError, Code: code, Message: message,
	}) {
		return errors.New("write failed")
	}
	return nil
}

// sendErrorWith adds optional candidates/matches.
func (s *Server) sendErrorWith(ctx context.Context, state *clientState, code protocol.ErrorCode, message string, candidates, matches []string) error {
	if !s.writeFrame(ctx, state, protocol.Error{
		Op: protocol.OpError, Code: code, Message: message,
		Candidates: candidates, Matches: matches,
	}) {
		return errors.New("write failed")
	}
	return nil
}

// sendErrorOnConn is the variant used before a clientState exists
// (during the hello handshake).
func sendErrorOnConn(ctx context.Context, conn *websocket.Conn, code protocol.ErrorCode, message string) error {
	return sendErrorOnConnWith(ctx, conn, code, message, nil, nil)
}

func sendErrorOnConnWith(ctx context.Context, conn *websocket.Conn, code protocol.ErrorCode, message string, candidates, matches []string) error {
	data, err := json.Marshal(protocol.Error{
		Op: protocol.OpError, Code: code, Message: message,
		Candidates: candidates, Matches: matches,
	})
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}

// broadcastEvent fans v out to every connected agent except excludeSID.
func (s *Server) broadcastEvent(v any, excludeSID string) {
	s.registryMu.Lock()
	targets := make([]*clientState, 0, len(s.registry))
	for _, c := range s.registry {
		if c.role == protocol.RoleAgent && c.sessionID != excludeSID {
			targets = append(targets, c)
		}
	}
	s.registryMu.Unlock()
	if len(targets) == 0 {
		return
	}
	// Use a fresh context: broadcasts shouldn't be cancelled by the
	// originating handler returning.
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	for _, t := range targets {
		s.safeWrite(ctx, t, v)
	}
}
