package inspect

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschrenk/team/internal/bus"
	"github.com/oschrenk/team/internal/logfile"
	"github.com/oschrenk/team/internal/protocol"
)

const testToken = "inspect-token-xyz"

// fakeBus implements BusAccessor for handler unit tests.
type fakeBus struct {
	token     string
	startedAt time.Time
	stats     bus.Stats
	sessions  []protocol.SessionInfo
}

func (f *fakeBus) Token() string                              { return f.token }
func (f *fakeBus) StartedAt() time.Time                       { return f.startedAt }
func (f *fakeBus) Stats() bus.Stats                           { return f.stats }
func (f *fakeBus) RegistrySnapshot() []protocol.SessionInfo   { return f.sessions }

func newTestServer(t *testing.T, b BusAccessor) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, b, "0.1.0", 9473)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, body
}

func TestHealth_NoAuth(t *testing.T) {
	b := &fakeBus{token: testToken, startedAt: time.Now().Add(-2 * time.Minute)}
	srv := newTestServer(t, b)
	resp, body := get(t, srv, "/api/health", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var h Health
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Port != 9473 || h.Version != "0.1.0" {
		t.Fatalf("got %+v", h)
	}
	if h.UptimeSeconds < 100 {
		t.Fatalf("uptime_s = %v, want ≥ 100", h.UptimeSeconds)
	}
}

func TestSessions_RequiresAuth(t *testing.T) {
	b := &fakeBus{token: testToken}
	srv := newTestServer(t, b)

	resp, body := get(t, srv, "/api/sessions", "")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
	}
	resp, body = get(t, srv, "/api/sessions", "wrong-token")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 on bad token, got %d: %s", resp.StatusCode, body)
	}
}

func TestSessions_OK(t *testing.T) {
	b := &fakeBus{
		token: testToken,
		sessions: []protocol.SessionInfo{
			{SessionID: "s1", Name: "alice", Cwd: "/proj/foo", Since: "2026-01-01T00:00:00Z"},
			{SessionID: "s2", Name: "bob", Label: "Bob", Cwd: "/proj/bar", Since: "2026-01-01T00:00:01Z"},
		},
	}
	srv := newTestServer(t, b)
	resp, body := get(t, srv, "/api/sessions", testToken)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got []protocol.SessionInfo
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alice" || got[1].Name != "bob" {
		t.Fatalf("got %+v", got)
	}
}

func TestStats_OK(t *testing.T) {
	b := &fakeBus{
		token: testToken,
		stats: bus.Stats{
			UptimeSeconds: 12.5, Connected: 2, MsgsSent: 47, MsgsBroadcast: 12,
			MsgsRejected: 1, PeerJoinedTotal: 8, PeerLeftTotal: 5,
			BroadcastRateLimitPerMin: 60,
		},
	}
	srv := newTestServer(t, b)
	resp, body := get(t, srv, "/api/stats", testToken)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"msgs_sent":47`) {
		t.Fatalf("expected msgs_sent=47, got %s", body)
	}
	if !strings.Contains(string(body), `"broadcast_rate_limit_per_min":60`) {
		t.Fatalf("missing rate limit field: %s", body)
	}
}

func TestMessages_TailDefaultsAndCap(t *testing.T) {
	// Override messages.log path to a temp file.
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	// Write 30 lines to messages.log.
	tmpPath := filepath.Join(t.TempDir(), "messages.log")
	for i := 0; i < 30; i++ {
		rec := LoggedMessage{
			Ts: "2026-01-01T00:00:00Z", MsgID: "id" + itoa(i),
			Kind: "direct", From: "sid", FromName: "alice",
			Text: "msg " + itoa(i),
		}
		if err := logfile.AppendJSONL(tmpPath, rec, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := TailJSONL(tmpPath, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d, want 5", len(got))
	}
	if got[0].MsgID != "id25" || got[4].MsgID != "id29" {
		t.Fatalf("got %v..%v, want id25..id29", got[0].MsgID, got[4].MsgID)
	}
}

func TestMessages_MissingFileReturnsEmpty(t *testing.T) {
	got, err := TailJSONL("/nonexistent/path/messages.log", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestMessages_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	body := `{"msg_id":"a","text":"good"}
not json at all
{"msg_id":"b","text":"good2"}
`
	if err := writeFileSimple(path, body); err != nil {
		t.Fatal(err)
	}
	got, err := TailJSONL(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MsgID != "a" || got[1].MsgID != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestMessages_EndpointUsesPath(t *testing.T) {
	t.Setenv("TEAM_DATA_DIR", t.TempDir())
	// Write one log entry to the real default messages.log path.
	rec := LoggedMessage{Ts: "x", MsgID: "abc", Kind: "direct", From: "s", FromName: "a", Text: "y"}
	if err := logfile.AppendJSONL(messagesLogPath(t), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	b := &fakeBus{token: testToken}
	srv := newTestServer(t, b)

	resp, body := get(t, srv, "/api/messages?n=3", testToken)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got []LoggedMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MsgID != "abc" {
		t.Fatalf("got %+v", got)
	}
}

// --- helpers ---------------------------------------------------------

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

// writeFileSimple writes body to path, ensuring the parent dir exists.
func writeFileSimple(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// messagesLogPath returns paths.MessagesLogPath() but routed through
// the test data dir.
func messagesLogPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("TEAM_DATA_DIR"), "messages.log")
}
