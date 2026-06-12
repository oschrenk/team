package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
	"github.com/oschrenk/team/internal/protocol"
	"github.com/oschrenk/team/internal/validate"
)

var (
	connectHost    string
	connectPort    int
	connectLabel   string
	connectVerbose bool
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Long-lived monitor: connect this session to the team bus",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host := envOverride(cmd, "host", connectHost, "TEAM_HOST")
		port := portFromFlagsAndEnv(cmd, connectPort)
		label := connectLabel
		if label == "" {
			label = os.Getenv("TEAM_LABEL")
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			name = os.Getenv("TEAM_NAME")
		}

		if name != "" && !validate.Name(name) {
			return fmt.Errorf("invalid name %q", name)
		}
		if !validate.Label(label) {
			return fmt.Errorf("invalid label %q", label)
		}

		autoNamed := false
		if name == "" {
			cwd, _ := os.Getwd()
			name = validate.AutoNameFromCwd(cwd)
			autoNamed = name != ""
		}

		c := client.New(client.Options{
			Host:    host,
			Port:    port,
			Name:    name,
			Label:   label,
			Verbose: connectVerbose,
		})
		if autoNamed {
			fmt.Printf("[team] no name given; auto-named %q from cwd (rename with `team rename`)\n", name)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		return c.Run(ctx)
	},
}

// portFromFlagsAndEnv applies the spec's precedence:
//
//	CLI flag → CLAUDE_PLUGIN_OPTION_PORT → TEAM_PORT → default
func portFromFlagsAndEnv(cmd *cobra.Command, flagValue int) int {
	if cmd.Flags().Changed("port") {
		return flagValue
	}
	for _, k := range []string{"CLAUDE_PLUGIN_OPTION_PORT", "TEAM_PORT"} {
		if v := os.Getenv(k); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				return p
			}
		}
	}
	return flagValue
}

func envOverride(cmd *cobra.Command, flagName, flagValue, envKey string) string {
	if cmd.Flags().Changed(flagName) {
		return flagValue
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return flagValue
}

func init() {
	connectCmd.Flags().StringVar(&connectHost, "host", "127.0.0.1", "server host")
	connectCmd.Flags().IntVar(&connectPort, "port", protocol.DefaultPort, "server port")
	connectCmd.Flags().StringVar(&connectLabel, "label", "", "display label")
	connectCmd.Flags().BoolVar(&connectVerbose, "verbose", false, "verbose output")
	rootCmd.AddCommand(connectCmd)
}
