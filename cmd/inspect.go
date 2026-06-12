package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/inspect"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

var (
	inspectHost     string
	inspectPort     int
	inspectJSON     bool
	inspectWatch    bool
	inspectMessages int
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the running bus (registry, stats, recent messages)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if inspectWatch {
			return runInspectWatch()
		}
		return runInspectOnce()
	},
}

func runInspectOnce() error {
	snap, err := fetchSnapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if inspectJSON {
		data, _ := json.MarshalIndent(snap, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	renderSnapshot(snap)
	return nil
}

func runInspectWatch() error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		snap, err := fetchSnapshot()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else {
			clearScreen()
			renderSnapshot(snap)
		}
		<-tick.C
	}
}

// snapshot is the bundle of all four endpoints, returned by --json.
type snapshot struct {
	Health   inspect.Health           `json:"health"`
	Sessions []protocol.SessionInfo   `json:"sessions"`
	Stats    map[string]any           `json:"stats"`
	Messages []inspect.LoggedMessage  `json:"messages"`
}

func fetchSnapshot() (*snapshot, error) {
	s := &snapshot{}
	if err := getJSON("/api/health", false, &s.Health); err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	if err := getJSON("/api/sessions", true, &s.Sessions); err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}
	if err := getJSON("/api/stats", true, &s.Stats); err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	url := fmt.Sprintf("/api/messages?n=%d", inspectMessages)
	if err := getJSON(url, true, &s.Messages); err != nil {
		return nil, fmt.Errorf("messages: %w", err)
	}
	return s, nil
}

func getJSON(path string, withAuth bool, out any) error {
	url := fmt.Sprintf("http://%s:%d%s", inspectHost, inspectPort, path)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if withAuth {
		tok, err := auth.EnsureToken(paths.TokenPath())
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func renderSnapshot(s *snapshot) {
	// Header
	uptime := time.Duration(s.Health.UptimeSeconds * float64(time.Second)).Round(time.Second)
	state := "alive"
	if !s.Health.OK {
		state = "DOWN"
	}
	fmt.Printf("server: %s @ %s:%d (uptime %s, version %s)\n",
		state, inspectHost, inspectPort, uptime, s.Health.Version)
	fmt.Println()

	// Sessions
	fmt.Printf("sessions (%d connected):\n", len(s.Sessions))
	if len(s.Sessions) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tSESSION\tCWD\tSINCE")
		for _, sess := range s.Sessions {
			short := sess.SessionID
			if len(short) > 8 {
				short = short[:8]
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
				emptyOr(sess.Name, "—"), short,
				emptyOr(sess.Cwd, "—"), shortenTimestamp(sess.Since))
		}
		w.Flush()
	}
	fmt.Println()

	// Stats line
	fmt.Printf("stats: msgs sent=%v (broadcast=%v, rejected=%v)  peers joined/left: %v/%v\n",
		jsonNum(s.Stats["msgs_sent"]),
		jsonNum(s.Stats["msgs_broadcast"]),
		jsonNum(s.Stats["msgs_rejected"]),
		jsonNum(s.Stats["peer_joined_total"]),
		jsonNum(s.Stats["peer_left_total"]))
	fmt.Println()

	// Recent messages
	fmt.Printf("recent messages (%d):\n", len(s.Messages))
	for _, m := range s.Messages {
		dir := "→ " + emptyOr(m.To, "(all)")
		fmt.Printf("  [%s] %s %s: %s\n",
			shortenTimestamp(m.Ts), m.FromName, dir, truncateOneLine(m.Text, 80))
	}
}

func shortenTimestamp(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19] // HH:MM:SS
	}
	return ts
}

func truncateOneLine(s string, n int) string {
	// Strip newlines so the message stays on one line.
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			out = append(out, '↵')
		} else {
			out = append(out, r)
		}
	}
	if len(out) > n {
		return string(out[:n]) + "…"
	}
	return string(out)
}

func jsonNum(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case nil:
		return "0"
	default:
		return fmt.Sprint(v)
	}
}

func clearScreen() {
	// ANSI clear-screen + move-cursor-home. Degrades to a few junk chars
	// if stdout isn't a TTY — acceptable for a watch loop.
	fmt.Print("\x1b[2J\x1b[H")
}

func init() {
	inspectCmd.Flags().StringVar(&inspectHost, "host", "127.0.0.1", "server host")
	inspectCmd.Flags().IntVar(&inspectPort, "port", protocol.DefaultPort, "server port")
	inspectCmd.Flags().BoolVar(&inspectJSON, "json", false, "raw JSON aggregation")
	inspectCmd.Flags().BoolVar(&inspectWatch, "watch", false, "repaint every 2s")
	inspectCmd.Flags().IntVarP(&inspectMessages, "messages", "n", 5, "number of recent messages to fetch")
	inspectCmd.SilenceUsage = true
	inspectCmd.SilenceErrors = true
	rootCmd.AddCommand(inspectCmd)
}
