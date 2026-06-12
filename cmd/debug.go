package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/auth"
	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
)

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Inspect internal state (paths, token) — dev aid",
}

var debugPathsCmd = &cobra.Command{
	Use:   "paths",
	Short: "Print resolved on-disk paths",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("data_dir:       %s\n", paths.DataDir())
		fmt.Printf("token:          %s\n", paths.TokenPath())
		fmt.Printf("server_log:     %s\n", paths.ServerLogPath())
		fmt.Printf("messages_log:   %s\n", paths.MessagesLogPath())
		fmt.Printf("clients_dir:    %s\n", paths.ClientsDir())
		fmt.Printf("pidfile(9473):  %s\n", paths.PidfilePath(9473, "127.0.0.1"))
		fmt.Printf("meta(9473):     %s\n", paths.PidfileMetaPath(9473, "127.0.0.1"))
	},
}

var debugTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Mint (if missing) and print the bearer token",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := paths.SecureDir(paths.DataDir()); err != nil {
			return err
		}
		tok, err := auth.EnsureToken(paths.TokenPath())
		if err != nil {
			return err
		}
		fmt.Printf("path:  %s\n", paths.TokenPath())
		fmt.Printf("token: %s\n", tok)
		return nil
	},
}

var debugConstantsCmd = &cobra.Command{
	Use:   "constants",
	Short: "Print protocol-level constants (size / rate / timing limits)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("default_port:                %d\n", protocol.DefaultPort)
		fmt.Printf("ws_frame_cap:                %d  (%.0f MB)\n", protocol.WSFrameCap, float64(protocol.WSFrameCap)/1024/1024)
		fmt.Printf("text_cap:                    %d  (%.0f MB)\n", protocol.TextCap, float64(protocol.TextCap)/1024/1024)
		fmt.Printf("broadcast_text_cap:          %d  (%.0f KB)\n", protocol.BroadcastTextCap, float64(protocol.BroadcastTextCap)/1024)
		fmt.Printf("stdout_cap:                  %d  (codepoints)\n", protocol.StdoutCap)
		fmt.Printf("ping_interval:               %s\n", protocol.PingInterval)
		fmt.Printf("pong_timeout:                %s\n", protocol.PongTimeout)
		fmt.Printf("reconnect_backoff_min:       %s\n", protocol.ReconnectBackoffMin)
		fmt.Printf("reconnect_backoff_max:       %s\n", protocol.ReconnectBackoffMax)
		fmt.Printf("reconnect_jitter_frac:       %g\n", protocol.ReconnectJitterFrac)
		fmt.Printf("broadcast_rate_limit_per_min:%d\n", protocol.BroadcastRateLimitPerMin)
		fmt.Printf("messages_log_max_bytes:      %d  (%.0f MB)\n", protocol.MessagesLogMaxBytes, float64(protocol.MessagesLogMaxBytes)/1024/1024)
		fmt.Printf("messages_log_backups:        %d\n", protocol.MessagesLogBackups)
	},
}

var debugVerifyCmd = &cobra.Command{
	Use:   "verify [host] [port]",
	Short: "Run VerifyServerIdentity with verbose output",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		host := args[0]
		port := 0
		fmt.Sscanf(args[1], "%d", &port)
		fmt.Printf("verifying %s:%d\n", host, port)
		fmt.Printf("pidfile path: %s\n", paths.PidfilePath(port, host))
		fmt.Printf("meta path:    %s\n", paths.PidfileMetaPath(port, host))
		ok := auth.VerifyServerIdentity(host, port)
		fmt.Printf("result: %v\n", ok)
		return nil
	},
}

func init() {
	debugCmd.AddCommand(debugPathsCmd)
	debugCmd.AddCommand(debugTokenCmd)
	debugCmd.AddCommand(debugConstantsCmd)
	debugCmd.AddCommand(debugVerifyCmd)
	rootCmd.AddCommand(debugCmd)
}
