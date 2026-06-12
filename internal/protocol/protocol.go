// Package protocol carries the wire-protocol constants and op structs
// for the team WebSocket bus. JSON field names mirror the Python
// reference (lowercase-with-underscores) for cross-implementation
// compatibility.
package protocol

import (
	"encoding/json"
	"time"
)

// Endpoint defaults.
const DefaultPort = 9473

// Size limits (mirrors shared.py).
const (
	WSFrameCap       = 16 * 1024 * 1024
	TextCap          = 10 * 1024 * 1024
	BroadcastTextCap = 256 * 1024
	StdoutCap        = 400
)

// Liveness / reconnect.
const (
	PingInterval        = 15 * time.Second
	PongTimeout         = 30 * time.Second
	ReconnectBackoffMin = 250 * time.Millisecond
	ReconnectBackoffMax = 4 * time.Second
	ReconnectJitterFrac = 0.2
)

// Server-side throttles.
const BroadcastRateLimitPerMin = 60

// Message-log rotation.
const (
	MessagesLogMaxBytes = 50 * 1024 * 1024
	MessagesLogBackups  = 5
)

// TimestampFormat is the on-wire timestamp encoding. RFC3339Nano in UTC.
const TimestampFormat = time.RFC3339Nano

// Role distinguishes long-lived listeners (agents) from short-lived
// helpers acting on a listener's behalf (controls).
type Role string

const (
	RoleAgent   Role = "agent"
	RoleControl Role = "control"
)

// OpName is the discriminator field on every frame.
type OpName string

const (
	OpHello      OpName = "hello"
	OpWelcome    OpName = "welcome"
	OpError      OpName = "error"
	OpPing       OpName = "ping"
	OpPong       OpName = "pong"
	OpList       OpName = "list"
	OpListOk     OpName = "list_ok"
	OpSend       OpName = "send"
	OpBroadcast  OpName = "broadcast"
	OpRename     OpName = "rename"
	OpRenamed    OpName = "renamed"
	OpBye        OpName = "bye"
	OpMsg        OpName = "msg"
	OpPeerJoined OpName = "peer_joined"
	OpPeerLeft   OpName = "peer_left"
)

// ErrorCode is the discriminator in error frames.
type ErrorCode string

const (
	ErrInvalidName    ErrorCode = "invalid_name"
	ErrInvalidLabel   ErrorCode = "invalid_label"
	ErrInvalidPayload ErrorCode = "invalid_payload"
	ErrNameTaken      ErrorCode = "name_taken"
	ErrUnknownPeer    ErrorCode = "unknown_peer"
	ErrAmbiguous      ErrorCode = "ambiguous"
	ErrTextTooLong    ErrorCode = "text_too_long"
	ErrUnauthorized   ErrorCode = "unauthorized"
	ErrRateLimited    ErrorCode = "rate_limited"
	ErrHopLimit       ErrorCode = "hop_limit"
	ErrUnknownOp      ErrorCode = "unknown_op"
)

// PeekOp reads only the "op" field of a JSON frame. Useful as a
// discriminator before unmarshaling into a typed op struct.
func PeekOp(raw []byte) (OpName, error) {
	var disc struct {
		Op OpName `json:"op"`
	}
	if err := json.Unmarshal(raw, &disc); err != nil {
		return "", err
	}
	return disc.Op, nil
}

// --- Op structs --------------------------------------------------------

// Hello — client → server. First frame on a new connection.
type Hello struct {
	Op         OpName `json:"op"`
	SessionID  string `json:"session_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Label      string `json:"label,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Pid        int    `json:"pid,omitempty"`
	Role       Role   `json:"role,omitempty"`
	Token      string `json:"token,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	ForSession string `json:"for_session,omitempty"`
}

// Welcome — server → client on successful hello.
type Welcome struct {
	Op           OpName `json:"op"`
	SessionID    string `json:"session_id"`
	AssignedName string `json:"assigned_name"`
	ForSession   string `json:"for_session,omitempty"`
}

// Error — server → client. Candidates and Matches are present for
// name_taken / ambiguous respectively.
type Error struct {
	Op         OpName    `json:"op"`
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Candidates []string  `json:"candidates,omitempty"`
	Matches    []string  `json:"matches,omitempty"`
}

// Ping / Pong — bidirectional keepalive.
type Ping struct {
	Op OpName `json:"op"`
}

type Pong struct {
	Op OpName `json:"op"`
}

// ListReq — client → server. Just the discriminator.
type ListReq struct {
	Op OpName `json:"op"`
}

// SessionInfo — one entry in a ListOk response.
type SessionInfo struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	Cwd       string `json:"cwd"`
	Since     string `json:"since"`
}

// ListOk — server → client.
type ListOk struct {
	Op       OpName        `json:"op"`
	Sessions []SessionInfo `json:"sessions"`
}

// Send — client → server (direct).
type Send struct {
	Op   OpName `json:"op"`
	To   string `json:"to"`
	Text string `json:"text"`
}

// Broadcast — client → server.
type Broadcast struct {
	Op   OpName `json:"op"`
	Text string `json:"text"`
}

// Rename — client → server.
type Rename struct {
	Op   OpName `json:"op"`
	Name string `json:"name"`
}

// Renamed — server → broadcast event when a peer renames itself.
// Also returned to the renaming client as an ack.
type Renamed struct {
	Op        OpName `json:"op"`
	SessionID string `json:"session_id,omitempty"`
	Name      string `json:"name"`
}

// Bye — client → server. Voluntary disconnect.
type Bye struct {
	Op OpName `json:"op"`
}

// Msg — server → client. Delivered message.
//
// `To` / `ToSessionID` are populated only on direct sends; broadcasts
// omit them.
type Msg struct {
	Op          OpName `json:"op"`
	MsgID       string `json:"msg_id"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
	FromLabel   string `json:"from_label,omitempty"`
	To          string `json:"to,omitempty"`
	ToSessionID string `json:"to_session_id,omitempty"`
	Text        string `json:"text"`
	Ts          string `json:"ts"`
}

// PeerJoined / PeerLeft — server → broadcast lifecycle events.
type PeerJoined struct {
	Op        OpName `json:"op"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

type PeerLeft struct {
	Op        OpName `json:"op"`
	SessionID string `json:"session_id"`
}
