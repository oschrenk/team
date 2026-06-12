#!/usr/bin/env bash
# End-to-end smoke for `team`. Spins up `team server` on a free port,
# starts two `team connect` listeners as agents alice and bob, exchanges
# direct + broadcast messages, exercises `team inspect`. Designed to run
# without touching launchd or the user's real ~/.claude data directory.
#
# Exit 0 on success, non-zero on any failed expectation. Intended for
# `task smoke` and CI dry-runs before tagging a release.

set -uo pipefail

readonly PORT=${TEAM_SMOKE_PORT:-19473}
readonly DATA_DIR=$(mktemp -d -t team-smoke-XXXXXX)
readonly BIN=${TEAM_BIN:-./team}
readonly PPID_KEY=$$

cleanup() {
	if [[ -n ${BOB:-} ]]; then kill "$BOB" 2>/dev/null || true; fi
	if [[ -n ${ALICE:-} ]]; then kill "$ALICE" 2>/dev/null || true; fi
	if [[ -n ${SVR:-} ]]; then kill "$SVR" 2>/dev/null || true; fi
	wait 2>/dev/null || true
	rm -rf "$DATA_DIR"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
ok()   { echo "ok:   $*"; }

# --- preflight --------------------------------------------------------

[[ -x "$BIN" ]] || fail "team binary not found at $BIN (run \`task build\` first)"

# --- server -----------------------------------------------------------

TEAM_DATA_DIR="$DATA_DIR" "$BIN" server --port "$PORT" >"$DATA_DIR/server.log" 2>&1 &
SVR=$!
sleep 0.3
kill -0 "$SVR" 2>/dev/null || fail "server failed to start; log: $(cat "$DATA_DIR/server.log")"
[[ -f "$DATA_DIR/server.$PORT.pid.meta" ]] || fail "pidfile meta missing"
ok "server started (pid=$SVR, port=$PORT)"

# --- alice listener ---------------------------------------------------

TEAM_DATA_DIR="$DATA_DIR" TEAM_PORT="$PORT" TEAM_PPID_OVERRIDE="$PPID_KEY" \
  "$BIN" connect alice --label "Alice" >"$DATA_DIR/alice.out" 2>&1 &
ALICE=$!
sleep 0.7
[[ -f "$DATA_DIR/clients/$PPID_KEY.session" ]] || \
  fail "alice never registered; alice.out: $(cat "$DATA_DIR/alice.out")"
ok "alice registered"

# --- bob raw WS receiver ----------------------------------------------

cat >"$DATA_DIR/bob.go" <<EOF
package main
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
	"github.com/coder/websocket"
)
func main() {
	tok, _ := os.ReadFile(os.Args[1] + "/token")
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:" + os.Args[2] + "/", nil)
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	defer conn.CloseNow()
	d, _ := json.Marshal(map[string]any{"op":"hello","name":"bob","token":string(tok),"nonce":"nb","role":"agent"})
	conn.Write(ctx, websocket.MessageText, d)
	conn.Read(ctx)
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil { return }
		os.Stdout.Write(raw); os.Stdout.WriteString("\n")
		time.Sleep(10 * time.Millisecond)
	}
}
EOF
go build -o "$DATA_DIR/bob" "$DATA_DIR/bob.go" 2>&1 || fail "bob build failed"
"$DATA_DIR/bob" "$DATA_DIR" "$PORT" >"$DATA_DIR/bob.recv" 2>&1 &
BOB=$!
sleep 0.5
ok "bob receiver running"

# --- exercise helpers -------------------------------------------------

T="TEAM_DATA_DIR=$DATA_DIR TEAM_PPID_OVERRIDE=$PPID_KEY"

eval "$T $BIN status" | grep -q "connected: alice" || fail "status didn't show alice"
ok "team status connected"

eval "$T $BIN list" | grep -q "alice" || fail "team list missing alice"
ok "team list shows alice"

eval "$T $BIN send bob \"hi bob\"" || fail "team send exited non-zero"
sleep 0.3
grep -q "hi bob" "$DATA_DIR/bob.recv" || fail "bob never received the direct msg"
ok "team send delivered"

if eval "$T $BIN send nobody x" 2>/dev/null; then
	fail "team send to nobody should have exit 1"
fi
ok "team send unknown-peer exits non-zero"

eval "$T $BIN broadcast \"hey all\"" || fail "team broadcast exited non-zero"
sleep 0.3
grep -q "hey all" "$DATA_DIR/bob.recv" || fail "bob never received broadcast"
ok "team broadcast delivered"

eval "$T $BIN rename alicia" >/dev/null || fail "team rename exited non-zero"
sleep 0.3
eval "$T $BIN list" | grep -q "alicia" || fail "post-rename list missing alicia"
ok "team rename applied"

# --- inspect ----------------------------------------------------------

TEAM_DATA_DIR="$DATA_DIR" "$BIN" inspect --port "$PORT" -n 5 >"$DATA_DIR/inspect.out" 2>&1 || \
  fail "team inspect exited non-zero"
grep -q "alive @ 127.0.0.1:$PORT" "$DATA_DIR/inspect.out" || fail "inspect missing alive header"
grep -q "alicia" "$DATA_DIR/inspect.out" || fail "inspect missing alicia"
grep -q "bob" "$DATA_DIR/inspect.out" || fail "inspect missing bob"
ok "team inspect renders"

TEAM_DATA_DIR="$DATA_DIR" "$BIN" inspect --port "$PORT" --json -n 5 | jq -e '.health.ok == true' >/dev/null || \
  fail "team inspect --json shape unexpected"
ok "team inspect --json valid"

echo
echo "all good ✓"
