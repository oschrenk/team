package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
)

var sendCmd = &cobra.Command{
	Use:   "send <to> <text>",
	Short: "Send a direct message to one peer",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWithControlConn(cmd.Context(), func(ctx context.Context, ctrl *client.ControlConn) error {
			return ctrl.SendDirect(ctx, args[0], args[1])
		})
	},
}

var broadcastCmd = &cobra.Command{
	Use:   "broadcast <text>",
	Short: "Send a message to all other peers (≤256 KB)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWithControlConn(cmd.Context(), func(ctx context.Context, ctrl *client.ControlConn) error {
			return ctrl.SendBroadcast(ctx, args[0])
		})
	},
}

// runWithControlConn handles the common discover → dial → action →
// close flow, plus exit-code mapping:
//   - 0 on success
//   - 1 on any server error frame
//   - 2 on "not connected"
//
// cobra error-wrapping is bypassed via SilenceUsage/SilenceErrors so we
// own the diagnostic output.
func runWithControlConn(ctx context.Context, action func(context.Context, *client.ControlConn) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctrl, err := client.ControlDial(ctx)
	if errors.Is(err, client.ErrNotConnected) {
		fmt.Fprintln(os.Stderr, "not connected; run /team:team connect in this Claude Code session first")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ctrl.Close()
	if err := action(ctx, ctrl); err != nil {
		printActionError(err)
		os.Exit(1)
	}
	return nil
}

func printActionError(err error) {
	var ce *client.ControlError
	if errors.As(err, &ce) {
		msg := fmt.Sprintf("%s: %s", ce.Code, ce.Message)
		if len(ce.Matches) > 0 {
			msg += fmt.Sprintf(" (matches: %s)", strings.Join(ce.Matches, ", "))
		}
		if len(ce.Candidates) > 0 {
			msg += fmt.Sprintf(" (try: %s)", strings.Join(ce.Candidates, ", "))
		}
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

func init() {
	sendCmd.SilenceUsage = true
	sendCmd.SilenceErrors = true
	broadcastCmd.SilenceUsage = true
	broadcastCmd.SilenceErrors = true
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(broadcastCmd)
}
