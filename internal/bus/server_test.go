package bus

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/protocol"
)

const testToken = "test-bearer-xyz"

// --- harness ----------------------------------------------------------

type harness struct {
	t      *testing.T
	srv    *Server
	url    string
	clock  *fakeClock
	cancel context.CancelFunc
}

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("TEAM_DATA_DIR", t.TempDir())

	clock := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	var uuidCounter int
	srv, err := New(Options{
		Host:  "127.0.0.1",
		Port:  0,
		Token: testToken,
		Now:   clock.now,
		UUIDFunc: func() string {
			// Counter in the leading chars so [:8] is unique across
			// sessions (matches what real UUIDs offer).
			uuidCounter++
			return zeroPad(uuidCounter, 8) + "-test"
		},
		MsgIDFunc: func() string {
			return "mid00000"
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()

	t.Cleanup(func() {
		srv.Stop()
	})

	return &harness{
		t:     t,
		srv:   srv,
		url:   "ws://" + l.Addr().String(),
		clock: clock,
	}
}

func zeroPad(i, width int) string {
	s := itoa(i)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// --- test client ------------------------------------------------------

type tclient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *harness) dial() *tclient {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, h.url, nil)
	if err != nil {
		h.t.Fatalf("Dial: %v", err)
	}
	conn.SetReadLimit(protocol.WSFrameCap)
	tc := &tclient{t: h.t, conn: conn}
	h.t.Cleanup(func() { tc.close() })
	return tc
}

func (tc *tclient) send(v any) {
	tc.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		tc.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tc.conn.Write(ctx, websocket.MessageText, data); err != nil {
		tc.t.Fatal(err)
	}
}

func (tc *tclient) recvRaw() []byte {
	tc.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := tc.conn.Read(ctx)
	if err != nil {
		tc.t.Fatalf("recv: %v", err)
	}
	return data
}

func (tc *tclient) recvOp() (protocol.OpName, []byte) {
	tc.t.Helper()
	data := tc.recvRaw()
	op, err := protocol.PeekOp(data)
	if err != nil {
		tc.t.Fatalf("peek: %v (raw=%s)", err, data)
	}
	return op, data
}

func (tc *tclient) close() {
	_ = tc.conn.Close(websocket.StatusNormalClosure, "bye")
}

// helloAgent runs a successful agent hello and returns the welcome.
func (tc *tclient) helloAgent(name, nonce string) protocol.Welcome {
	tc.send(protocol.Hello{
		Op: protocol.OpHello, Name: name, Token: testToken,
		Nonce: nonce, Role: protocol.RoleAgent,
	})
	op, raw := tc.recvOp()
	if op != protocol.OpWelcome {
		tc.t.Fatalf("expected welcome, got %s raw=%s", op, raw)
	}
	var w protocol.Welcome
	_ = json.Unmarshal(raw, &w)
	return w
}

func (tc *tclient) helloControl(forSession, nonce string) protocol.Welcome {
	tc.send(protocol.Hello{
		Op: protocol.OpHello, Token: testToken, Nonce: nonce,
		Role: protocol.RoleControl, ForSession: forSession,
	})
	op, raw := tc.recvOp()
	if op != protocol.OpWelcome {
		tc.t.Fatalf("expected welcome (control), got %s raw=%s", op, raw)
	}
	var w protocol.Welcome
	_ = json.Unmarshal(raw, &w)
	return w
}

// drainPeerEvents reads and discards any peer_joined/peer_left/renamed
// notifications until something else arrives.
func (tc *tclient) drainPeerEvents() (protocol.OpName, []byte) {
	tc.t.Helper()
	for {
		op, raw := tc.recvOp()
		switch op {
		case protocol.OpPeerJoined, protocol.OpPeerLeft, protocol.OpRenamed:
			continue
		default:
			return op, raw
		}
	}
}

// --- tests ------------------------------------------------------------

func TestHello_OK(t *testing.T) {
	h := newHarness(t)
	tc := h.dial()
	w := tc.helloAgent("alice", "n1")
	if w.AssignedName != "alice" {
		t.Fatalf("got %q", w.AssignedName)
	}
	if w.SessionID == "" {
		t.Fatal("empty session id")
	}
}

func TestHello_BadToken(t *testing.T) {
	h := newHarness(t)
	tc := h.dial()
	tc.send(protocol.Hello{
		Op: protocol.OpHello, Name: "alice", Token: "wrong",
		Nonce: "n", Role: protocol.RoleAgent,
	})
	op, raw := tc.recvOp()
	if op != protocol.OpError {
		t.Fatalf("got %s", op)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrUnauthorized {
		t.Fatalf("got %s", e.Code)
	}
}

func TestHello_InvalidName(t *testing.T) {
	h := newHarness(t)
	tc := h.dial()
	tc.send(protocol.Hello{
		Op: protocol.OpHello, Name: "BadName", Token: testToken,
		Nonce: "n", Role: protocol.RoleAgent,
	})
	op, raw := tc.recvOp()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrInvalidName {
		t.Fatalf("got %s", e.Code)
	}
}

func TestHello_NameTakenWithCandidates(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "n1")

	b := h.dial()
	b.send(protocol.Hello{
		Op: protocol.OpHello, Name: "alice", Token: testToken,
		Nonce: "n2", Role: protocol.RoleAgent,
	})
	op, raw := b.recvOp()
	if op != protocol.OpError {
		t.Fatalf("got %s", op)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrNameTaken {
		t.Fatalf("got %s", e.Code)
	}
	if len(e.Candidates) != 3 || e.Candidates[0] != "alice-2" {
		t.Fatalf("bad candidates: %v", e.Candidates)
	}
}

func TestHello_SessionReplaceRequiresMatchingNonce(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	w := a.helloAgent("alice", "n1")

	bad := h.dial()
	bad.send(protocol.Hello{
		Op: protocol.OpHello, SessionID: w.SessionID, Name: "alice",
		Token: testToken, Nonce: "wrong-nonce", Role: protocol.RoleAgent,
	})
	op, raw := bad.recvOp()
	if op != protocol.OpError {
		t.Fatalf("got %s", op)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrUnauthorized {
		t.Fatalf("got %s", e.Code)
	}
}

func TestHello_SessionReplaceSucceedsWithSameNonce(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	w := a.helloAgent("alice", "n1")

	repl := h.dial()
	repl.send(protocol.Hello{
		Op: protocol.OpHello, SessionID: w.SessionID, Name: "alice",
		Token: testToken, Nonce: "n1", Role: protocol.RoleAgent,
	})
	op, _ := repl.recvOp()
	if op != protocol.OpWelcome {
		t.Fatalf("expected welcome, got %s", op)
	}
}

func TestList_Empty(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "n1")
	a.send(protocol.ListReq{Op: protocol.OpList})
	op, raw := a.recvOp()
	if op != protocol.OpListOk {
		t.Fatalf("got %s", op)
	}
	var l protocol.ListOk
	_ = json.Unmarshal(raw, &l)
	// alice is in the registry; she sees herself.
	if len(l.Sessions) != 1 || l.Sessions[0].Name != "alice" {
		t.Fatalf("got %+v", l.Sessions)
	}
}

func TestList_WithPeers(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob", "nb")

	// alice may have a queued peer_joined for bob; drain.
	a.send(protocol.ListReq{Op: protocol.OpList})
	op, raw := a.drainPeerEvents()
	if op != protocol.OpListOk {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var l protocol.ListOk
	_ = json.Unmarshal(raw, &l)
	names := map[string]bool{}
	for _, s := range l.Sessions {
		names[s.Name] = true
	}
	if !names["alice"] || !names["bob"] {
		t.Fatalf("missing peer: %v", names)
	}
}

func TestSend_ByName(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob", "nb")

	a.send(protocol.Send{Op: protocol.OpSend, To: "bob", Text: "hi bob"})

	op, raw := b.drainPeerEvents()
	if op != protocol.OpMsg {
		t.Fatalf("expected msg, got %s raw=%s", op, raw)
	}
	var m protocol.Msg
	_ = json.Unmarshal(raw, &m)
	if m.Text != "hi bob" || m.FromName != "alice" || m.To != "bob" {
		t.Fatalf("got %+v", m)
	}
	if h.srv.counters.MsgsSent.Load() != 1 {
		t.Fatalf("MsgsSent = %d", h.srv.counters.MsgsSent.Load())
	}
}

func TestSend_ByNamePrefix(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob-build", "nb")

	a.send(protocol.Send{Op: protocol.OpSend, To: "bob", Text: "hi"})
	op, raw := b.drainPeerEvents()
	if op != protocol.OpMsg {
		t.Fatalf("got %s raw=%s", op, raw)
	}
}

func TestSend_AmbiguousPrefix(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	h.dial().helloAgent("bob-build", "nb")
	h.dial().helloAgent("bob-test", "nc")

	a.send(protocol.Send{Op: protocol.OpSend, To: "bob", Text: "ambiguous"})
	op, raw := a.drainPeerEvents()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrAmbiguous {
		t.Fatalf("got %s", e.Code)
	}
	if len(e.Matches) != 2 {
		t.Fatalf("matches=%v", e.Matches)
	}
}

func TestSend_BySessionIDPrefix(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	wb := b.helloAgent("bob", "nb")

	a.send(protocol.Send{
		Op: protocol.OpSend, To: wb.SessionID[:8], Text: "by sid",
	})
	op, _ := b.drainPeerEvents()
	if op != protocol.OpMsg {
		t.Fatalf("got %s", op)
	}
}

func TestSend_ToSelf_Rejected(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	a.send(protocol.Send{Op: protocol.OpSend, To: "alice", Text: "self"})
	op, raw := a.drainPeerEvents()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrUnknownPeer {
		t.Fatalf("got %s", e.Code)
	}
}

func TestBroadcast_FanOut(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob", "nb")
	c := h.dial()
	c.helloAgent("carol", "nc")

	a.send(protocol.Broadcast{Op: protocol.OpBroadcast, Text: "hey all"})
	for _, peer := range []*tclient{b, c} {
		op, raw := peer.drainPeerEvents()
		if op != protocol.OpMsg {
			t.Fatalf("peer got %s raw=%s", op, raw)
		}
		var m protocol.Msg
		_ = json.Unmarshal(raw, &m)
		if m.Text != "hey all" {
			t.Fatalf("got %q", m.Text)
		}
		if m.To != "" {
			t.Fatalf("broadcast should omit To, got %q", m.To)
		}
	}
	if got := h.srv.counters.MsgsBroadcast.Load(); got != 1 {
		t.Fatalf("MsgsBroadcast=%d", got)
	}
}

func TestBroadcast_RateLimit(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	// One peer to receive — broadcast resolution doesn't care, but we
	// want at least one fan-out target.
	h.dial().helloAgent("bob", "nb")

	for i := 0; i < protocol.BroadcastRateLimitPerMin; i++ {
		a.send(protocol.Broadcast{Op: protocol.OpBroadcast, Text: "x"})
	}
	// 61st should be rate-limited.
	a.send(protocol.Broadcast{Op: protocol.OpBroadcast, Text: "over"})
	op, raw := a.drainPeerEvents()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrRateLimited {
		t.Fatalf("got %s", e.Code)
	}
}

func TestBroadcast_ControlChargesListener(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	w := a.helloAgent("alice", "na")
	h.dial().helloAgent("bob", "nb") // receiver

	// Exhaust the quota via control connections — each in a fresh
	// dial — to prove the rate limit keys on the listener, not the
	// per-connection identity.
	for i := 0; i < protocol.BroadcastRateLimitPerMin; i++ {
		ctrl := h.dial()
		ctrl.helloControl(w.SessionID, "na")
		ctrl.send(protocol.Broadcast{Op: protocol.OpBroadcast, Text: "x"})
		ctrl.close()
	}

	// One more from a fresh control connection should be rate-limited.
	ctrl := h.dial()
	ctrl.helloControl(w.SessionID, "na")
	ctrl.send(protocol.Broadcast{Op: protocol.OpBroadcast, Text: "over"})
	op, raw := ctrl.recvOp()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrRateLimited {
		t.Fatalf("got %s", e.Code)
	}
}

func TestRename_OK(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	a.send(protocol.Rename{Op: protocol.OpRename, Name: "alicia"})
	op, raw := a.recvOp()
	if op != protocol.OpRenamed {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var r protocol.Renamed
	_ = json.Unmarshal(raw, &r)
	if r.Name != "alicia" {
		t.Fatalf("got %q", r.Name)
	}
}

func TestRename_Collision(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob", "nb")
	a.send(protocol.Rename{Op: protocol.OpRename, Name: "bob"})
	op, raw := a.drainPeerEvents()
	if op != protocol.OpError {
		t.Fatalf("got %s raw=%s", op, raw)
	}
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code != protocol.ErrNameTaken {
		t.Fatalf("got %s", e.Code)
	}
}

func TestPing_Pong(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	a.send(protocol.Ping{Op: protocol.OpPing})
	op, _ := a.recvOp()
	if op != protocol.OpPong {
		t.Fatalf("got %s", op)
	}
}

func TestUnregister_BroadcastsPeerLeft(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	a.helloAgent("alice", "na")
	b := h.dial()
	b.helloAgent("bob", "nb")

	// b leaves
	b.close()

	// alice should see a peer_left for bob.
	for {
		op, _ := a.recvOp()
		if op == protocol.OpPeerLeft {
			break
		}
		if op == protocol.OpPeerJoined {
			continue
		}
		t.Fatalf("unexpected op %s while waiting for peer_left", op)
	}
	if got := h.srv.counters.PeerLeftTotal.Load(); got < 1 {
		t.Fatalf("PeerLeftTotal=%d", got)
	}
}

func TestRegistrySnapshot_ExcludesControls(t *testing.T) {
	h := newHarness(t)
	a := h.dial()
	w := a.helloAgent("alice", "na")
	ctrl := h.dial()
	ctrl.helloControl(w.SessionID, "na")

	snap := h.srv.RegistrySnapshot()
	if len(snap) != 1 || snap[0].Name != "alice" {
		t.Fatalf("got %+v", snap)
	}
}
