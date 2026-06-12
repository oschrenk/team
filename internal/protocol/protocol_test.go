package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPeekOp(t *testing.T) {
	cases := []struct {
		in   string
		want OpName
	}{
		{`{"op":"hello"}`, OpHello},
		{`{"op":"msg","text":"x"}`, OpMsg},
		{`{"op":"unknown"}`, OpName("unknown")},
	}
	for _, tc := range cases {
		got, err := PeekOp([]byte(tc.in))
		if err != nil {
			t.Fatalf("PeekOp(%q) err: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("PeekOp(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPeekOp_Malformed(t *testing.T) {
	if _, err := PeekOp([]byte(`not json`)); err == nil {
		t.Fatal("expected error on malformed input")
	}
}

// roundTrip[T] marshals v, unmarshals back into U, and asserts
// they're deeply equal.
func roundTrip[T any](t *testing.T, name string, v T) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s marshal: %v", name, err)
	}
	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%s unmarshal: %v (raw=%s)", name, err, data)
	}
	if !reflect.DeepEqual(v, got) {
		t.Fatalf("%s round-trip mismatch:\n  want %#v\n  got  %#v\n  raw  %s", name, v, got, data)
	}
}

func TestRoundTrip_Hello_Agent(t *testing.T) {
	roundTrip(t, "Hello-agent", Hello{
		Op: OpHello, SessionID: "sid-1", Name: "alice", Label: "Alice",
		Cwd: "/proj/foo", Pid: 1234, Role: RoleAgent,
		Token: "tok", Nonce: "n",
	})
}

func TestRoundTrip_Hello_Control(t *testing.T) {
	roundTrip(t, "Hello-control", Hello{
		Op: OpHello, Role: RoleControl,
		Token: "tok", Nonce: "n", ForSession: "sid-listener",
	})
}

func TestRoundTrip_Welcome(t *testing.T) {
	roundTrip(t, "Welcome", Welcome{
		Op: OpWelcome, SessionID: "sid", AssignedName: "alice",
	})
}

func TestRoundTrip_Welcome_Control(t *testing.T) {
	roundTrip(t, "Welcome-control", Welcome{
		Op: OpWelcome, SessionID: "sid", AssignedName: "alice",
		ForSession: "sid-listener",
	})
}

func TestRoundTrip_Error(t *testing.T) {
	roundTrip(t, "Error", Error{
		Op: OpError, Code: ErrUnknownPeer, Message: "no agent matches 'x'",
	})
	roundTrip(t, "Error-with-candidates", Error{
		Op: OpError, Code: ErrNameTaken, Message: "taken",
		Candidates: []string{"alice-2", "alice-3"},
	})
	roundTrip(t, "Error-with-matches", Error{
		Op: OpError, Code: ErrAmbiguous, Message: "ambiguous",
		Matches: []string{"alice", "alice-build"},
	})
}

func TestRoundTrip_Ping_Pong_Bye(t *testing.T) {
	roundTrip(t, "Ping", Ping{Op: OpPing})
	roundTrip(t, "Pong", Pong{Op: OpPong})
	roundTrip(t, "Bye", Bye{Op: OpBye})
}

func TestRoundTrip_List(t *testing.T) {
	roundTrip(t, "ListReq", ListReq{Op: OpList})
	roundTrip(t, "ListOk-empty", ListOk{Op: OpListOk, Sessions: []SessionInfo{}})
	roundTrip(t, "ListOk-populated", ListOk{
		Op: OpListOk,
		Sessions: []SessionInfo{
			{SessionID: "s1", Name: "alice", Label: "", Cwd: "/p", Since: "2024-01-01T00:00:00Z"},
			{SessionID: "s2", Name: "bob", Label: "Bob", Cwd: "/q", Since: "2024-01-01T00:00:01Z"},
		},
	})
}

func TestRoundTrip_Send_Broadcast(t *testing.T) {
	roundTrip(t, "Send", Send{Op: OpSend, To: "alice", Text: "hi"})
	roundTrip(t, "Broadcast", Broadcast{Op: OpBroadcast, Text: "hey all"})
}

func TestRoundTrip_Rename_Renamed(t *testing.T) {
	roundTrip(t, "Rename", Rename{Op: OpRename, Name: "newname"})
	roundTrip(t, "Renamed-ack", Renamed{Op: OpRenamed, Name: "newname"})
	roundTrip(t, "Renamed-event", Renamed{
		Op: OpRenamed, SessionID: "sid-x", Name: "newname",
	})
}

func TestRoundTrip_Msg_Direct(t *testing.T) {
	roundTrip(t, "Msg-direct", Msg{
		Op:          OpMsg,
		MsgID:       "abc123",
		From:        "sid-1",
		FromName:    "alice",
		FromLabel:   "Alice",
		To:          "bob",
		ToSessionID: "sid-2",
		Text:        "hi there",
		Ts:          "2024-01-01T12:34:56.789Z",
	})
}

func TestRoundTrip_Msg_Broadcast(t *testing.T) {
	// Broadcasts omit To / ToSessionID.
	roundTrip(t, "Msg-broadcast", Msg{
		Op: OpMsg, MsgID: "id", From: "sid-1", FromName: "alice",
		Text: "all hands", Ts: "2024-01-01T12:34:56Z",
	})
}

func TestRoundTrip_PeerJoined_PeerLeft(t *testing.T) {
	roundTrip(t, "PeerJoined", PeerJoined{Op: OpPeerJoined, SessionID: "sid", Name: "alice"})
	roundTrip(t, "PeerLeft", PeerLeft{Op: OpPeerLeft, SessionID: "sid"})
}

func TestJSONFieldNames_Snake(t *testing.T) {
	// Sentinel: regression-protect against accidental renames to
	// camelCase or similar (would break wire compat with the Python
	// upstream).
	data, _ := json.Marshal(Hello{
		Op: OpHello, SessionID: "x", ForSession: "y",
	})
	want := `"session_id"`
	if !contains(string(data), want) {
		t.Fatalf("expected %s in %s", want, data)
	}
	want2 := `"for_session"`
	if !contains(string(data), want2) {
		t.Fatalf("expected %s in %s", want2, data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
