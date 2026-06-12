package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
)

var disconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Show how to stop the local listener (we can't kill it for you)",
	RunE: func(cmd *cobra.Command, args []string) error {
		state, _, err := client.FindListenerState()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if state == nil {
			fmt.Println("not connected — nothing to disconnect")
			return nil
		}
		fmt.Printf("listener is running as %s (pid %d, session %s)\n",
			emptyOr(state.Name, "—"), state.ListenerPID, state.SessionID)
		fmt.Println()
		fmt.Println("to disconnect:")
		fmt.Println("  in Claude Code:  use the TaskStop tool on the monitor task")
		fmt.Printf("  manually:        kill %d\n", state.ListenerPID)
		return nil
	},
}

func init() {
	disconnectCmd.SilenceUsage = true
	disconnectCmd.SilenceErrors = true
	rootCmd.AddCommand(disconnectCmd)
}
