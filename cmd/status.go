package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this session's connection state",
	RunE: func(cmd *cobra.Command, args []string) error {
		state, _, err := client.FindListenerState()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if state == nil {
			fmt.Println("not connected")
			os.Exit(2)
		}
		fmt.Printf("connected: %s @ %s:%d\n", emptyOr(state.Name, "—"), state.Host, state.Port)
		return nil
	},
}

func init() {
	statusCmd.SilenceUsage = true
	statusCmd.SilenceErrors = true
	rootCmd.AddCommand(statusCmd)
}
