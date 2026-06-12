package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
	"github.com/oschrenk/team/internal/validate"
)

var renameCmd = &cobra.Command{
	Use:   "rename <new-name>",
	Short: "Rename this listener",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newName := args[0]
		if newName != "" && !validate.Name(newName) {
			return fmt.Errorf("invalid name %q", newName)
		}
		return runWithControlConn(cmd.Context(), func(ctx context.Context, ctrl *client.ControlConn) error {
			if err := ctrl.Rename(ctx, newName); err != nil {
				return err
			}
			fmt.Printf("renamed → %s\n", newName)
			return nil
		})
	},
}

func init() {
	renameCmd.SilenceUsage = true
	renameCmd.SilenceErrors = true
	rootCmd.AddCommand(renameCmd)
}
