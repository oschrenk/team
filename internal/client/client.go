// Package client implements the long-lived `team connect` monitor:
// holds a ppid flock so only one runs per CC session, dials the bus
// with backoff, writes incoming messages to stdout in the Claude Code
// monitor format, and maintains the per-session state file that helper
// CLIs read.
package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	mrand "math/rand/v2"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/flock"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/procutil"
	"github.com/oschrenk/team/internal/protocol"
)

// Options configures a new Client.
type Options struct {
	Host                string
	Port                int
	Name                string
	Label               string
	PPID                int
	Verbose             bool
	MaxCollisionRetries int

	// ParentCheckInterval is how often Run polls AliveCheck against
	// the resolved Claude ancestor pid. Zero → 5 seconds. Detects the
	// orphan case where the parent CC process dies and the monitor
	// gets reparented to init.
	ParentCheckInterval time.Duration

	// Test seams.
	Now        func() time.Time
	Out        io.Writer
	NewSID     func() string
	Token      string             // override for tests; production reads from disk
	AliveCheck func(pid int) bool // override for tests; defaults to procutil.SafePidAlive
}

// Client is the long-lived monitor.
type Client struct {
	host    string
	port    int
	name    string
	label   string
	ppid    int
	verbose bool

	sessionID string
	nonce     string

	maxCollisions int

	parentCheckInterval time.Duration
	aliveCheck          func(pid int) bool

	now func() time.Time
	out io.Writer
	tok string // empty → ensure on each connect attempt
}

// New builds a Client with defaults filled in.
func New(opts Options) *Client {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = protocol.DefaultPort
	}
	if opts.PPID == 0 {
		opts.PPID = procutil.ResolveListenerKey()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.MaxCollisionRetries == 0 {
		opts.MaxCollisionRetries = 3
	}
	if opts.ParentCheckInterval == 0 {
		opts.ParentCheckInterval = 5 * time.Second
	}
	if opts.AliveCheck == nil {
		opts.AliveCheck = procutil.SafePidAlive
	}
	sid := opts.NewSID
	if sid == nil {
		sid = newUUID
	}
	return &Client{
		host:                opts.Host,
		port:                opts.Port,
		name:                opts.Name,
		label:               opts.Label,
		ppid:                opts.PPID,
		verbose:             opts.Verbose,
		sessionID:           sid(),
		nonce:               randomNonce(),
		maxCollisions:       opts.MaxCollisionRetries,
		parentCheckInterval: opts.ParentCheckInterval,
		aliveCheck:          opts.AliveCheck,
		now:                 opts.Now,
		out:                 opts.Out,
		tok:                 opts.Token,
	}
}

// Run blocks until ctx is cancelled or the client gives up (e.g. name
// collision retries exhausted).
//
// Behavior:
//   - Tries to acquire the ppid flock; if held by another monitor,
//     prints diagnostic and returns nil.
//   - Otherwise loops: connect → run read loop → on disconnect, back
//     off (exponential + jitter) and retry. Honors ctx for shutdown.
func (c *Client) Run(ctx context.Context) error {
	if err := paths.SecureDir(paths.ClientsDir()); err != nil {
		return fmt.Errorf("create clients dir: %w", err)
	}
	lock, ok, err := flock.Acquire(paths.ClientLockPath(c.ppid))
	if err != nil {
		return fmt.Errorf("acquire ppid lock: %w", err)
	}
	if !ok {
		c.printAlreadyRunning()
		return nil
	}
	defer lock.Close()
	defer func() { _ = DeleteSessionState(c.ppid) }()

	// Parent-liveness watch: when the resolved CC ancestor dies (we get
	// reparented to init), cancel and exit. Without this, the monitor
	// outlives its parent and shows up as a ghost in `team list`.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go c.parentWatch(runCtx, cancelRun)

	backoff := protocol.ReconnectBackoffMin
	collisions := 0
	for {
		err := c.connectAndServe(runCtx)
		if errors.Is(err, errStopClient) {
			return nil
		}
		if errors.Is(err, errNameTaken) {
			collisions++
			if collisions >= c.maxCollisions {
				c.printf("[team] name %q taken after %d retries; run `team connect <other-name>`\n",
					c.name, collisions)
				return nil
			}
			// Reconnect immediately with the suggested name (already
			// set on c.name by handleHelloResponse).
			continue
		}
		if err != nil && c.verbose {
			c.printf("[team] connect failed: %v\n", err)
		}
		select {
		case <-runCtx.Done():
			return nil
		default:
		}
		// Backoff with jitter.
		jitter := protocol.ReconnectJitterFrac * float64(backoff)
		delay := time.Duration(float64(backoff) + (mrand.Float64()*2-1)*jitter)
		select {
		case <-runCtx.Done():
			return nil
		case <-time.After(delay):
		}
		backoff *= 2
		if backoff > protocol.ReconnectBackoffMax {
			backoff = protocol.ReconnectBackoffMax
		}
	}
}

// Sentinel errors used by Run to decide whether to reconnect.
var (
	errStopClient     = errors.New("client stop requested")
	errNameTaken      = errors.New("name taken; retry with candidate")
	errIdentityFailed = errors.New("server identity check failed; will retry")
)

func (c *Client) connectAndServe(ctx context.Context) error {
	// Resolve token. Verify identity first so we don't hand the token
	// to a port squatter. Failure is non-fatal: during launchd-managed
	// startup the pidfile may briefly lag, so retry via the outer loop's
	// backoff rather than giving up.
	if !auth.VerifyServerIdentity(c.host, c.port) {
		if c.verbose {
			c.printf("[team] server identity check failed (%s:%d); retrying\n",
				c.host, c.port)
		}
		return errIdentityFailed
	}
	tok := c.tok
	if tok == "" {
		t, err := auth.EnsureToken(paths.TokenPath())
		if err != nil {
			return fmt.Errorf("ensure token: %w", err)
		}
		tok = t
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, _, err := websocket.Dial(dialCtx, c.url(), nil)
	cancel()
	if err != nil {
		if isConnRefused(err) {
			c.printf("[team] server not running — run `team service install` (or `team server &` for manual start)\n")
		}
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.WSFrameCap)

	cwd, _ := os.Getwd()
	hello := protocol.Hello{
		Op:        protocol.OpHello,
		SessionID: c.sessionID,
		Name:      c.name,
		Label:     c.label,
		Cwd:       cwd,
		Pid:       os.Getpid(),
		Role:      protocol.RoleAgent,
		Token:     tok,
		Nonce:     c.nonce,
	}
	if err := writeJSON(ctx, conn, hello); err != nil {
		return err
	}

	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()

	_, raw, err := conn.Read(readCtx)
	if err != nil {
		return err
	}
	op, err := protocol.PeekOp(raw)
	if err != nil {
		return err
	}
	switch op {
	case protocol.OpError:
		return c.handleHelloError(raw)
	case protocol.OpWelcome:
		// continue
	default:
		return fmt.Errorf("unexpected response to hello: %s", op)
	}

	state := SessionState{
		SessionID:   c.sessionID,
		Name:        c.name,
		Label:       c.label,
		Token:       tok,
		Nonce:       c.nonce,
		ListenerPID: os.Getpid(),
		Host:        c.host,
		Port:        c.port,
		CreatedAt:   c.now().UTC().Format(protocol.TimestampFormat),
	}
	if err := WriteSessionState(c.ppid, state); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}

	go c.pingLoop(readCtx, conn)

	// Read loop.
	for {
		_, raw, err := conn.Read(readCtx)
		if err != nil {
			return err
		}
		op, err := protocol.PeekOp(raw)
		if err != nil {
			continue
		}
		switch op {
		case protocol.OpMsg:
			var m protocol.Msg
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			line, cont, was := FormatMsg(m)
			c.println(line)
			if was {
				c.println(cont)
			}
		case protocol.OpPeerJoined, protocol.OpPeerLeft, protocol.OpRenamed:
			if c.verbose {
				c.printf("[team] %s: %s\n", op, raw)
			}
		case protocol.OpPong:
			// keepalive ack
		default:
			if c.verbose {
				c.printf("[team] %s: %s\n", op, raw)
			}
		}
	}
}

func (c *Client) handleHelloError(raw []byte) error {
	var e protocol.Error
	_ = json.Unmarshal(raw, &e)
	if e.Code == protocol.ErrNameTaken && len(e.Candidates) > 0 {
		old := c.name
		c.name = e.Candidates[0]
		c.printf("[team] name %q taken; using %q\n", old, c.name)
		return errNameTaken
	}
	c.printf("[team] hello rejected: %s %s\n", e.Code, e.Message)
	return errStopClient
}

// parentWatch polls aliveCheck against the resolved CC ancestor pid on
// a ticker. When the parent dies, it cancels the Run context so the
// outer loop exits cleanly. A no-op if ppid is non-positive (the
// resolver fell through to an invalid value).
func (c *Client) parentWatch(ctx context.Context, cancel context.CancelFunc) {
	if c.ppid <= 0 {
		return
	}
	ticker := time.NewTicker(c.parentCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !c.aliveCheck(c.ppid) {
				if c.verbose {
					c.printf("[team] parent pid %d gone; exiting\n", c.ppid)
				}
				cancel()
				return
			}
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(protocol.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := writeJSON(ctx, conn, protocol.Ping{Op: protocol.OpPing}); err != nil {
				return
			}
		}
	}
}

// --- helpers ----------------------------------------------------------

func (c *Client) url() string {
	u := url.URL{
		Scheme: "ws",
		Host:   net.JoinHostPort(c.host, strconv.Itoa(c.port)),
		Path:   "/",
	}
	return u.String()
}

func (c *Client) printAlreadyRunning() {
	st, _ := ReadSessionState(c.ppid)
	if st != nil {
		c.printf("[team] another monitor for this session is already running — name=%q, listener_pid=%d, session_id=%q; exiting\n",
			st.Name, st.ListenerPID, st.SessionID)
		return
	}
	c.printf("[team] another monitor for this session is already running — exiting\n")
}

func (c *Client) printf(format string, args ...any) {
	fmt.Fprintf(c.out, format, args...)
}

func (c *Client) println(s string) {
	fmt.Fprintln(c.out, s)
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	// websocket.Dial wraps the underlying net error; check the chain.
	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		return errors.Is(sysErr.Err, syscall.ECONNREFUSED)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// Fallback: string match. coder/websocket sometimes returns an
	// opaque error in handshake failure modes.
	return strings.Contains(err.Error(), "connection refused")
}

// fsNotExistOK lets us drop os.IsNotExist branches inline.
func fsNotExistOK(err error) bool {
	return err == nil || errors.Is(err, fs.ErrNotExist)
}

// --- crypto helpers ---------------------------------------------------

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// silence unused-import noise if logic changes shape later.
var _ = fsNotExistOK
