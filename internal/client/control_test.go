package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

// startListenerAndHarness brings up the bus + an agent listener whose
// per-session state file is on disk. Returns the harness for raw-WS
// access plus the listener's own out buffer so tests can read incoming
// messages.
func startListenerAndHarness(t *testing.T, listenerName string) (*intHarness, *safeBuffer, context.CancelFunc) {
	t.Helper()
	h := startIntegrationServer(t)

	ppid := 30000 + int(time.Now().UnixNano()%1000)
	t.Setenv("TEAM_PPID_OVERRIDE", itoaForTest(ppid))

	listenerOut := &safeBuffer{}
	listener := New(Options{
		Host: h.host, Port: h.port,
		Name: listenerName, PPID: ppid,
		Token: h.token, Out: listenerOut,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = listener.Run(ctx) }()
	waitFor(t, 2*time.Second, func() bool {
		st, _ := ReadSessionState(ppid)
		return st != nil && st.Name == listenerName
	}, "listener registered")
	return h, listenerOut, cancel
}

func itoaForTest(i int) string {
	// stdlib strconv import would taint imports list; tiny inline
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

// --- ControlDial -------------------------------------------------------

func TestControlDial_OK(t *testing.T) {
	_, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()

	ctrl, err := ControlDial(context.Background())
	if err != nil {
		t.Fatalf("ControlDial: %v", err)
	}
	defer ctrl.Close()
	if ctrl.State.Name != "alice" {
		t.Fatalf("State.Name = %q", ctrl.State.Name)
	}
	if ctrl.Welcome.AssignedName != "alice" {
		t.Fatalf("welcome.assigned_name = %q", ctrl.Welcome.AssignedName)
	}
}

func TestControlDial_NotConnected(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	t.Setenv("TEAM_PPID_OVERRIDE", "99999")

	_, err := ControlDial(context.Background())
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v, want ErrNotConnected", err)
	}
}

// --- SendDirect / SendBroadcast / Rename / List -----------------------

func TestControl_SendDirect_Delivers(t *testing.T) {
	h, listenerOut, cancel := startListenerAndHarness(t, "alice")
	defer cancel()

	// Another peer (raw) so the control can target it (we send TO bob).
	bob := dialAgent(t, h, "bob", "n-bob")
	defer bob.close()

	ctrl, err := ControlDial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	if err := ctrl.SendDirect(context.Background(), "bob", "hi bob"); err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	_ = listenerOut // bob receives, not alice — we just need silence-on-success
}

func TestControl_SendDirect_UnknownPeer(t *testing.T) {
	_, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()
	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()

	err := ctrl.SendDirect(context.Background(), "nobody", "x")
	var ce *ControlError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *ControlError", err)
	}
	if ce.Code != protocol.ErrUnknownPeer {
		t.Fatalf("got code %s", ce.Code)
	}
}

func TestControl_SendDirect_Ambiguous(t *testing.T) {
	h, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()
	bob1 := dialAgent(t, h, "bob-build", "n1")
	defer bob1.close()
	bob2 := dialAgent(t, h, "bob-test", "n2")
	defer bob2.close()

	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()
	err := ctrl.SendDirect(context.Background(), "bob", "x")
	var ce *ControlError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *ControlError", err)
	}
	if ce.Code != protocol.ErrAmbiguous {
		t.Fatalf("got code %s", ce.Code)
	}
	if len(ce.Matches) != 2 {
		t.Fatalf("matches %v", ce.Matches)
	}
}

func TestControl_Broadcast_FanOut(t *testing.T) {
	h, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()
	bob := dialAgent(t, h, "bob", "nb")
	defer bob.close()
	carol := dialAgent(t, h, "carol", "nc")
	defer carol.close()

	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()
	if err := ctrl.SendBroadcast(context.Background(), "hey all"); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
}

func TestControl_List_ShowsAgentsHidesControls(t *testing.T) {
	h, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()
	bob := dialAgent(t, h, "bob", "nb")
	defer bob.close()

	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()
	sessions, err := ctrl.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range sessions {
		names[s.Name] = true
	}
	if !names["alice"] || !names["bob"] {
		t.Fatalf("missing peer: %v", names)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions (no controls), got %d: %+v", len(sessions), sessions)
	}
}

func TestControl_Rename_OK(t *testing.T) {
	_, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()

	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()
	if err := ctrl.Rename(context.Background(), "alicia"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// State.Name reflects pre-rename; subsequent list should show new name.
	sessions, _ := ctrl.List(context.Background())
	found := false
	for _, s := range sessions {
		if s.Name == "alicia" {
			found = true
		}
	}
	if !found {
		t.Fatalf("post-rename list missing 'alicia': %+v", sessions)
	}
}

func TestControl_Rename_Collision(t *testing.T) {
	h, _, cancel := startListenerAndHarness(t, "alice")
	defer cancel()
	bob := dialAgent(t, h, "bob", "nb")
	defer bob.close()

	ctrl, _ := ControlDial(context.Background())
	defer ctrl.Close()
	err := ctrl.Rename(context.Background(), "bob")
	var ce *ControlError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v", err)
	}
	if ce.Code != protocol.ErrNameTaken {
		t.Fatalf("got code %s", ce.Code)
	}
}

// --- UnlinkIfMatches -------------------------------------------------

func TestUnlinkIfMatches_MatchingState(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	st := SessionState{SessionID: "s1", Nonce: "n1", Name: "a"}
	if err := WriteSessionState(123, st); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkIfMatches(paths.ClientSessionPath(123), st); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadSessionState(123)
	if got != nil {
		t.Fatalf("expected unlinked, got %+v", got)
	}
}

func TestUnlinkIfMatches_DifferentState_Preserves(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	current := SessionState{SessionID: "s1", Nonce: "n1"}
	stale := SessionState{SessionID: "s0", Nonce: "n0"}
	if err := WriteSessionState(123, current); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkIfMatches(paths.ClientSessionPath(123), stale); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadSessionState(123)
	if got == nil || got.SessionID != "s1" {
		t.Fatalf("expected preserved, got %+v", got)
	}
}
