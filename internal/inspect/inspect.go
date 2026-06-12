// Package inspect exposes a small JSON inspection API on the same
// listener as the bus's WebSocket handler. The headline feature over
// the Python upstream: live observability without disturbing the bus.
//
// Routes (all under /api/):
//
//	GET /api/health         no auth → liveness + version
//	GET /api/sessions       bearer  → current registry snapshot
//	GET /api/stats          bearer  → counters + uptime
//	GET /api/messages?n=N   bearer  → tail of last N messages.log records
//
// Auth uses the same bearer token the WebSocket bus uses, supplied as
// `Authorization: Bearer <token>`. Compared with subtle.ConstantTimeCompare.
package inspect

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oschrenk/team/internal/bus"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

// BusAccessor is the read-only seam inspect uses to query bus state.
// *bus.Server satisfies it; tests can supply a fake.
type BusAccessor interface {
	Token() string
	StartedAt() time.Time
	Stats() bus.Stats
	RegistrySnapshot() []protocol.SessionInfo
}

// Mount adds /api/* routes to mux. Call once during server start-up.
func Mount(mux *http.ServeMux, b BusAccessor, version string, port int) {
	h := &handler{b: b, version: version, port: port}
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/sessions", h.withAuth(h.sessions))
	mux.HandleFunc("GET /api/stats", h.withAuth(h.stats))
	mux.HandleFunc("GET /api/messages", h.withAuth(h.messages))
}

type handler struct {
	b       BusAccessor
	version string
	port    int
}

// Health is the unauthenticated liveness payload.
type Health struct {
	OK            bool    `json:"ok"`
	Port          int     `json:"port"`
	UptimeSeconds float64 `json:"uptime_s"`
	Version       string  `json:"version"`
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Health{
		OK:            true,
		Port:          h.port,
		UptimeSeconds: time.Since(h.b.StartedAt()).Seconds(),
		Version:       h.version,
	})
}

func (h *handler) sessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.b.RegistrySnapshot())
}

func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.b.Stats())
}

func (h *handler) messages(w http.ResponseWriter, r *http.Request) {
	n := 20
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 500 {
		n = 500
	}
	msgs, err := TailJSONL(paths.MessagesLogPath(), n)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// withAuth wraps an HTTP handler with bearer-token enforcement.
func (h *handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
			return
		}
		supplied := auth[len(prefix):]
		want := h.b.Token()
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(want)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
