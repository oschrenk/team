package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/bus"
	"github.com/oschrenk/team/internal/inspect"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

var (
	serverHost string
	serverPort int
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the team bus (foreground; intended for launchd)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := bus.New(bus.Options{
			Host: serverHost,
			Port: serverPort,
		})
		if err != nil {
			return err
		}

		// Publish identity BEFORE binding the listener so the pidfile
		// exists by the time the kernel accepts a client connection.
		// (Without this, there's a window where Dial succeeds but
		// VerifyServerIdentity sees no pidfile and refuses to send the
		// bearer token.)
		if err := paths.SecureDir(paths.DataDir()); err != nil {
			return err
		}
		if err := auth.WriteServerIdentity(os.Getpid(), serverHost, serverPort); err != nil {
			return err
		}
		defer unlinkOwnIdentity(serverHost, serverPort)

		addr := serverHost + ":" + strconv.Itoa(serverPort)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-sigs
			s.Stop()
		}()

		fmt.Fprintf(os.Stderr, "team server listening on %s (pid=%d)\n",
			l.Addr().String(), os.Getpid())
		return s.Serve(l, func(mux *http.ServeMux) {
			inspect.Mount(mux, s, version, serverPort)
		})
	},
}

// unlinkOwnIdentity removes pidfile/.meta only if the pidfile still
// records this pid — protects against a fresh server having already
// taken over the endpoint.
func unlinkOwnIdentity(host string, port int) {
	pidPath := paths.PidfilePath(port, host)
	metaPath := paths.PidfileMetaPath(port, host)
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if pid == os.Getpid() {
				_ = os.Remove(pidPath)
				_ = os.Remove(metaPath)
			}
		}
	}
}

// debugStatsCmd hits the bus's in-process Stats accessor — only useful
// when run from the same process. Real out-of-process stats land in
// TEAM-008's HTTP /api/stats.
var debugStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Print bus stats JSON (placeholder — real version lands in TEAM-008)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := bus.New(bus.Options{})
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(s.Stats(), "", "  ")
		fmt.Println(string(data))
		return nil
	},
}

func init() {
	serverCmd.Flags().StringVar(&serverHost, "host", "127.0.0.1", "bind host")
	serverCmd.Flags().IntVar(&serverPort, "port", protocol.DefaultPort, "bind port")
	rootCmd.AddCommand(serverCmd)
	debugCmd.AddCommand(debugStatsCmd)
}
