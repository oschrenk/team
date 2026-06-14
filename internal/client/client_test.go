package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/bus"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

// --- format unit tests -----------------------------------------------

func TestFormatMsg_Short(t *testing.T) {
	m := protocol.Msg{
		Op: protocol.OpMsg, MsgID: "abcd1234",
		From: "sid1", FromName: "alice", FromLabel: "Alice",
		Text: "hi there", Ts: "2026-01-01T12:00:00Z",
	}
	line, cont, was := FormatMsg(m)
	if was {
		t.Errorf("did not expect truncation")
	}
	if !strings.Contains(line, "msg=abcd1234") || !strings.Contains(line, `from="alice"`) ||
		!strings.Contains(line, `"Alice"`) || !strings.HasSuffix(line, " hi there") {
		t.Errorf("unexpected line: %q", line)
	}
	if cont != "" {
		t.Errorf("expected empty cont, got %q", cont)
	}
}

func TestFormatMsg_NoLabel(t *testing.T) {
	m := protocol.Msg{
		Op: protocol.OpMsg, MsgID: "id",
		From: "sid", FromName: "alice", Text: "hello",
	}
	line, _, _ := FormatMsg(m)
	if strings.Contains(line, `""`) {
		t.Errorf("empty label slot should be omitted: %q", line)
	}
}

func TestFormatMsg_Truncated(t *testing.T) {
	long := strings.Repeat("a", protocol.StdoutCap*2)
	m := protocol.Msg{
		Op: protocol.OpMsg, MsgID: "id",
		From: "sid", FromName: "alice", Text: long,
	}
	line, cont, was := FormatMsg(m)
	if !was {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(line, "truncated=") {
		t.Errorf("missing truncated tag: %q", line)
	}
	if !strings.Contains(cont, "msg=id cont") {
		t.Errorf("cont line: %q", cont)
	}
}

func TestFormatMsg_FallbackToFromShortID(t *testing.T) {
	m := protocol.Msg{
		Op: protocol.OpMsg, MsgID: "id",
		From: "12345678abcd", Text: "x",
	}
	line, _, _ := FormatMsg(m)
	if !strings.Contains(line, `from="12345678"`) {
		t.Errorf("expected truncated From in prefix, got %q", line)
	}
}

// --- session state unit tests ----------------------------------------

func TestSessionState_RoundTrip(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	st := SessionState{
		SessionID: "s", Name: "alice", Label: "Alice",
		Token: "tok", Nonce: "n", ListenerPID: 123,
		Host: "127.0.0.1", Port: 9473, CreatedAt: "2026-01-01T00:00:00Z",
	}
	if err := WriteSessionState(42, st); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSessionState(42)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.SessionID != "s" || got.Port != 9473 {
		t.Fatalf("got %+v", got)
	}
	// File mode is 0600.
	info, _ := os.Stat(paths.ClientSessionPath(42))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm got %o", info.Mode().Perm())
	}
	// Delete is idempotent.
	if err := DeleteSessionState(42); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSessionState(42); err != nil {
		t.Fatalf("second delete should be no-op, got %v", err)
	}
}

func TestReadSessionState_MissingFile(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	got, err := ReadSessionState(42)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing file, got %+v", got)
	}
}

// --- integration ------------------------------------------------------

type intHarness struct {
	t          *testing.T
	srv        *bus.Server
	listenAddr string
	host       string
	port       int
	token      string
	dataDir    string
}

func startIntegrationServer(t *testing.T) *intHarness {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("TEAM_DATA_DIR", dataDir)

	srv, err := bus.New(bus.Options{
		Host:  "127.0.0.1",
		Token: "test-token-xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(l.Addr().String())
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	// Identity check during connectAndServe: write pidfile + meta
	// pointing at this test process and override the cmdline check
	// to pass.
	if err := auth.WriteServerIdentity(os.Getpid(), host, port); err != nil {
		t.Fatal(err)
	}
	prev := auth.DefaultCmdlineFetcher
	auth.DefaultCmdlineFetcher = func(int) ([]string, error) {
		return []string{"team", "server"}, nil
	}
	t.Cleanup(func() { auth.DefaultCmdlineFetcher = prev })

	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Stop() })

	return &intHarness{
		t: t, srv: srv,
		listenAddr: l.Addr().String(),
		host:       host, port: port,
		token:   "test-token-xyz",
		dataDir: dataDir,
	}
}

// safeBuffer is a goroutine-safe bytes.Buffer (the client's read
// goroutine prints concurrently with our test assertions).
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestIntegration_ConnectAndReceiveDirectMsg(t *testing.T) {
	h := startIntegrationServer(t)
	out := &safeBuffer{}

	c := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice", Label: "Alice",
		PPID:    10001,
		Token:   h.token,
		Out:     out,
		Verbose: true,
	})
	t.Cleanup(func() { t.Logf("client out: %s", out.String()) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientDone := make(chan struct{})
	go func() {
		_ = c.Run(ctx)
		close(clientDone)
	}()

	// Wait for session state file → that means the welcome arrived
	// and the read loop is running.
	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(10001)
		return st != nil && st.Name == "alice"
	}, "session state file")

	// Now send a direct message to alice by opening a raw WS as
	// another agent.
	other := dialAgent(t, h, "bob", "n-bob")
	defer other.close()
	other.send(protocol.Send{Op: protocol.OpSend, To: "alice", Text: "hi alice"})

	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "hi alice") &&
			strings.Contains(out.String(), `from="bob"`)
	}, "msg on stdout")

	cancel()
	<-clientDone

	// Session state should be cleaned up on graceful exit.
	if st, _ := ReadSessionState(10001); st != nil {
		t.Errorf("expected session state cleaned up after exit, got %+v", st)
	}
}

func TestIntegration_PPIDFlock_SecondMonitorBailsOut(t *testing.T) {
	h := startIntegrationServer(t)
	outA := &safeBuffer{}
	outB := &safeBuffer{}

	cA := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice", PPID: 10002,
		Token: h.token, Out: outA,
	})
	cB := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice2", PPID: 10002, // SAME ppid
		Token: h.token, Out: outB,
	})

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go func() { _ = cA.Run(ctxA) }()
	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(10002)
		return st != nil
	}, "A registered")

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	doneB := make(chan struct{})
	go func() {
		_ = cB.Run(ctxB)
		close(doneB)
	}()

	// B should bail out immediately because A holds the flock.
	select {
	case <-doneB:
	case <-time.After(2 * time.Second):
		t.Fatal("B did not exit despite flock conflict")
	}
	if !strings.Contains(outB.String(), "another monitor for this session is already running") {
		t.Fatalf("expected diagnostic from B, got %q", outB.String())
	}
}

func TestIntegration_NameTakenAutoRetry(t *testing.T) {
	h := startIntegrationServer(t)
	outA := &safeBuffer{}

	// Pre-register "alice" via a raw agent.
	first := dialAgent(t, h, "alice", "first-nonce")
	defer first.close()

	c := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice", PPID: 10003,
		Token: h.token, Out: outA,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// Should auto-retry to "alice-2".
	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(10003)
		return st != nil && st.Name == "alice-2"
	}, "auto-renamed alice-2")
	if !strings.Contains(outA.String(), "alice-2") {
		t.Errorf("expected rename diagnostic, got %q", outA.String())
	}
}

func TestIntegration_ExitsWhenParentDies(t *testing.T) {
	h := startIntegrationServer(t)
	out := &safeBuffer{}

	var parentAlive atomic.Bool
	parentAlive.Store(true)

	c := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice", PPID: 10010,
		Token: h.token, Out: out, Verbose: true,
		ParentCheckInterval: 25 * time.Millisecond,
		AliveCheck:          func(int) bool { return parentAlive.Load() },
	})
	t.Cleanup(func() { t.Logf("client out: %s", out.String()) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = c.Run(ctx)
		close(done)
	}()

	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(10010)
		return st != nil && st.Name == "alice"
	}, "session state file")

	// Simulate parent CC death.
	parentAlive.Store(false)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not exit after parent death")
	}
	if !strings.Contains(out.String(), "parent pid") {
		t.Errorf("expected parent-death diagnostic, got %q", out.String())
	}
	if st, _ := ReadSessionState(10010); st != nil {
		t.Errorf("expected session state cleaned up, got %+v", st)
	}
}

func TestIntegration_PersistsWhileParentAlive(t *testing.T) {
	h := startIntegrationServer(t)
	out := &safeBuffer{}

	c := New(Options{
		Host: h.host, Port: h.port,
		Name: "alice", PPID: 10011,
		Token: h.token, Out: out,
		ParentCheckInterval: 25 * time.Millisecond,
		AliveCheck:          func(int) bool { return true },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = c.Run(ctx)
		close(done)
	}()

	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(10011)
		return st != nil && st.Name == "alice"
	}, "session state file")

	// Verify the client stays running across many check-intervals.
	select {
	case <-done:
		t.Fatal("client exited despite alive parent")
	case <-time.After(250 * time.Millisecond):
	}

	cancel()
	<-done
}

// --- raw WS agent helper ---------------------------------------------

type rawAgent struct {
	t    *testing.T
	conn *websocket.Conn
}

func dialAgent(t *testing.T, h *intHarness, name, nonce string) *rawAgent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	url := "ws://" + h.listenAddr + "/"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(protocol.WSFrameCap)
	ag := &rawAgent{t: t, conn: conn}
	ag.send(protocol.Hello{
		Op: protocol.OpHello, Name: name, Token: h.token,
		Nonce: nonce, Role: protocol.RoleAgent,
	})
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := protocol.PeekOp(raw)
	if op != protocol.OpWelcome {
		t.Fatalf("raw agent %q hello failed: %s", name, raw)
	}
	return ag
}

func (a *rawAgent) send(v any) {
	a.t.Helper()
	data, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.conn.Write(ctx, websocket.MessageText, data); err != nil {
		a.t.Fatal(err)
	}
}

func (a *rawAgent) close() {
	_ = a.conn.Close(websocket.StatusNormalClosure, "bye")
}

// waitFor polls predicate at 25ms intervals until it returns true or
// timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, predicate func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", label)
}
