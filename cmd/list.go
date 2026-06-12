package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/client"
	"github.com/oschrenk/team/internal/protocol"
)

var (
	listJSON bool
	listSelf bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
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

		if listSelf {
			printSelf(ctrl)
			return nil
		}

		sessions, err := ctrl.List(ctx)
		if err != nil {
			printActionError(err)
			os.Exit(1)
		}
		if listJSON {
			data, _ := json.MarshalIndent(sessions, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		printSessions(sessions)
		return nil
	},
}

func printSessions(sessions []protocol.SessionInfo) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSESSION\tCWD\tSINCE")
	for _, s := range sessions {
		short := s.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", emptyOr(s.Name, "—"), short, emptyOr(s.Cwd, "—"), s.Since)
	}
	w.Flush()
}

func printSelf(ctrl *client.ControlConn) {
	if listJSON {
		data, _ := json.MarshalIndent(map[string]any{
			"name":       ctrl.State.Name,
			"label":      ctrl.State.Label,
			"session_id": ctrl.State.SessionID,
			"host":       ctrl.State.Host,
			"port":       ctrl.State.Port,
		}, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Printf("name:       %s\n", emptyOr(ctrl.State.Name, "—"))
	fmt.Printf("label:      %s\n", emptyOr(ctrl.State.Label, "—"))
	fmt.Printf("session_id: %s\n", ctrl.State.SessionID)
	fmt.Printf("host:       %s\n", ctrl.State.Host)
	fmt.Printf("port:       %d\n", ctrl.State.Port)
}

func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "raw JSON output")
	listCmd.Flags().BoolVar(&listSelf, "self", false, "only show this listener's own line")
	listCmd.SilenceUsage = true
	listCmd.SilenceErrors = true
	rootCmd.AddCommand(listCmd)
}
