package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/protocol"
)

// Sentinel errors helper CLIs can branch on for exit-code selection.
var (
	ErrNotConnected = errors.New("not connected")
	// ErrHelloRejected carries the server-side rejection reason.
)

// ControlError is a server-side error frame surfaced to a helper CLI.
type ControlError struct {
	Code       protocol.ErrorCode
	Message    string
	Matches    []string
	Candidates []string
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ControlConn wraps a connected role=control WebSocket.
type ControlConn struct {
	conn    *websocket.Conn
	State   SessionState
	Welcome protocol.Welcome
}

// ControlDial discovers the local listener via the per-session state
// file, verifies the server identity, and completes the role=control
// handshake. On stale state (server returns unknown_peer / unauthorized),
// removes the state file (TOCTOU-safe) and returns ErrNotConnected.
func ControlDial(ctx context.Context) (*ControlConn, error) {
	state, statePath, err := FindListenerState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotConnected
	}
	if !auth.VerifyServerIdentity(state.Host, state.Port) {
		return nil, fmt.Errorf("server identity check failed (%s:%d)", state.Host, state.Port)
	}
	u := url.URL{
		Scheme: "ws",
		Host:   net.JoinHostPort(state.Host, strconv.Itoa(state.Port)),
		Path:   "/",
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, _, err := websocket.Dial(dialCtx, u.String(), nil)
	cancel()
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(protocol.WSFrameCap)

	hello := protocol.Hello{
		Op:         protocol.OpHello,
		Pid:        os.Getpid(),
		Role:       protocol.RoleControl,
		Token:      state.Token,
		Nonce:      state.Nonce,
		ForSession: state.SessionID,
	}
	if err := writeJSON(ctx, conn, hello); err != nil {
		conn.CloseNow()
		return nil, err
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	op, _ := protocol.PeekOp(raw)
	if op == protocol.OpError {
		var e protocol.Error
		_ = json.Unmarshal(raw, &e)
		conn.CloseNow()
		if e.Code == protocol.ErrUnknownPeer || e.Code == protocol.ErrUnauthorized {
			// Stale state file — clean up so future helpers don't
			// re-attempt.
			_ = UnlinkIfMatches(statePath, *state)
			return nil, ErrNotConnected
		}
		return nil, &ControlError{Code: e.Code, Message: e.Message}
	}
	if op != protocol.OpWelcome {
		conn.CloseNow()
		return nil, fmt.Errorf("unexpected response: %s", op)
	}
	var w protocol.Welcome
	_ = json.Unmarshal(raw, &w)
	return &ControlConn{conn: conn, State: *state, Welcome: w}, nil
}

// Close shuts down the WebSocket.
func (c *ControlConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "bye")
}

// SendDirect sends one direct message and waits briefly for an error
// frame. nil result = success; *ControlError = server rejection.
func (c *ControlConn) SendDirect(ctx context.Context, to, text string) error {
	if len(text) > protocol.TextCap {
		return fmt.Errorf("text exceeds direct send cap (%d bytes)", protocol.TextCap)
	}
	if err := writeJSON(ctx, c.conn, protocol.Send{
		Op: protocol.OpSend, To: to, Text: text,
	}); err != nil {
		return err
	}
	return c.expectErrorOrTimeout(ctx, time.Second)
}

// SendBroadcast sends one broadcast and waits briefly for an error.
func (c *ControlConn) SendBroadcast(ctx context.Context, text string) error {
	if len(text) > protocol.BroadcastTextCap {
		return fmt.Errorf("text exceeds broadcast cap (%d bytes)", protocol.BroadcastTextCap)
	}
	if err := writeJSON(ctx, c.conn, protocol.Broadcast{
		Op: protocol.OpBroadcast, Text: text,
	}); err != nil {
		return err
	}
	return c.expectErrorOrTimeout(ctx, time.Second)
}

// Rename requests a rename of the listener. Blocks for the server's
// response.
func (c *ControlConn) Rename(ctx context.Context, name string) error {
	if err := writeJSON(ctx, c.conn, protocol.Rename{
		Op: protocol.OpRename, Name: name,
	}); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, raw, err := c.conn.Read(rctx)
	if err != nil {
		return err
	}
	op, _ := protocol.PeekOp(raw)
	switch op {
	case protocol.OpRenamed:
		return nil
	case protocol.OpError:
		var e protocol.Error
		_ = json.Unmarshal(raw, &e)
		return &ControlError{Code: e.Code, Message: e.Message}
	default:
		return fmt.Errorf("unexpected response: %s", op)
	}
}

// List returns the current agent registry.
func (c *ControlConn) List(ctx context.Context) ([]protocol.SessionInfo, error) {
	if err := writeJSON(ctx, c.conn, protocol.ListReq{Op: protocol.OpList}); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, raw, err := c.conn.Read(rctx)
	if err != nil {
		return nil, err
	}
	op, _ := protocol.PeekOp(raw)
	switch op {
	case protocol.OpListOk:
		var lo protocol.ListOk
		if err := json.Unmarshal(raw, &lo); err != nil {
			return nil, err
		}
		return lo.Sessions, nil
	case protocol.OpError:
		var e protocol.Error
		_ = json.Unmarshal(raw, &e)
		return nil, &ControlError{Code: e.Code, Message: e.Message}
	default:
		return nil, fmt.Errorf("unexpected response: %s", op)
	}
}

// expectErrorOrTimeout reads one frame; nil if it's something other than
// an error frame or the read times out (success-by-silence pattern).
func (c *ControlConn) expectErrorOrTimeout(ctx context.Context, d time.Duration) error {
	rctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	_, raw, err := c.conn.Read(rctx)
	if err != nil {
		// Timeout / closed → treat as success.
		return nil
	}
	op, _ := protocol.PeekOp(raw)
	if op == protocol.OpError {
		var e protocol.Error
		_ = json.Unmarshal(raw, &e)
		return &ControlError{
			Code: e.Code, Message: e.Message,
			Matches: e.Matches, Candidates: e.Candidates,
		}
	}
	return nil
}
