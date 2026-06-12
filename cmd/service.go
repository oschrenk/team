package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/oschrenk/team/internal/service"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the launchd user agent for the team server",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the launchd plist and bootstrap the service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		binPath, err := resolveBinaryPath()
		if err != nil {
			return err
		}
		msg, err := ctl.Install(binPath)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", msg)
		fmt.Printf("  plist:  %s\n", ctl.PlistPath())
		fmt.Printf("  log:    %s\n", ctl.LogPath())
		fmt.Printf("  binary: %s\n", binPath)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Bootout the service and remove the plist",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		msg, err := ctl.Uninstall()
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print service state (loaded / running / pid)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		st, err := ctl.Status()
		if err != nil {
			return err
		}
		fmt.Printf("plist_installed: %v\n", st.PlistInstalled)
		fmt.Printf("loaded:          %v\n", st.Loaded)
		fmt.Printf("state:           %s\n", emptyDash(st.State))
		fmt.Printf("pid:             %s\n", pidString(st.Pid))
		fmt.Printf("plist:           %s\n", ctl.PlistPath())
		fmt.Printf("log:             %s\n", ctl.LogPath())
		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Kickstart the service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		return ctl.Start()
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Send SIGTERM to the service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		return ctl.Stop()
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Kickstart -k (kill and restart)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		return ctl.Restart()
	},
}

var serviceLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail the service log (Ctrl-C to exit)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctl, err := service.New()
		if err != nil {
			return err
		}
		return tailLog(ctl.LogPath())
	},
}

// tailLog shells out to `tail -F` so the user gets the well-understood
// kernel-event-driven behavior, including follow-across-rotate. We
// intentionally don't reimplement that here.
func tailLog(path string) error {
	ctx, cancel := signalContext()
	defer cancel()
	c := exec.CommandContext(ctx, "tail", "-F", path)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	if err := c.Run(); err != nil {
		// tail exits non-zero on signal — treat that as expected.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()
	return ctx, cancel
}

func resolveBinaryPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Resolve symlinks so launchd's plist always points at the
	// underlying binary (brew installs through symlinks).
	resolved, err := os.Readlink(p)
	if err != nil || resolved == "" {
		return p, nil
	}
	return resolved, nil
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func pidString(p int) string {
	if p <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", p)
}

// silence unused-import linter for io in case tailLog gets rewritten.
var _ = io.Discard

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	serviceCmd.AddCommand(serviceStartCmd)
	serviceCmd.AddCommand(serviceStopCmd)
	serviceCmd.AddCommand(serviceRestartCmd)
	serviceCmd.AddCommand(serviceLogsCmd)
	rootCmd.AddCommand(serviceCmd)
}
