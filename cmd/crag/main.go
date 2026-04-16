package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/donovan-yohan/crag/internal/config"
	"github.com/donovan-yohan/crag/internal/dispatch"
	"github.com/donovan-yohan/crag/internal/session"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "crag",
		Short: "Crag — outer control plane for Nightshift local dev",
		Long: "Crag dispatches belayer runs to a Lima VM worker, polls status, and " +
			"streams logs back to the macOS terminal.",
		SilenceUsage: true,
	}
	root.AddCommand(newRunCmd(), newStatusCmd(), newLogsCmd(), newVMCmd())
	return root
}

func newRunCmd() *cobra.Command {
	var (
		task   string
		detach bool
	)
	cmd := &cobra.Command{
		Use:   "run <repo-or-path>",
		Short: "Submit a belayer run inside the Lima VM",
		Args:  cobra.ExactArgs(1),
		RunE: withDispatcher(func(ctx context.Context, d *dispatch.Dispatcher, args []string) error {
			if task == "" {
				return fmt.Errorf("--task is required")
			}
			id, err := d.Submit(ctx, dispatch.Request{Source: args[0], Task: task})
			if err != nil {
				return err
			}
			fmt.Printf("submitted session %s\n", id)
			if detach {
				return nil
			}
			return d.Wait(ctx, id)
		}),
	}
	cmd.Flags().StringVar(&task, "task", "", "task description handed to belayer")
	cmd.Flags().BoolVar(&detach, "detach", false, "submit and exit; do not wait for completion")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [session-id]",
		Short: "Poll status for a session (defaults to the most recent)",
		Args:  cobra.MaximumNArgs(1),
		RunE: withDispatcher(func(ctx context.Context, d *dispatch.Dispatcher, args []string) error {
			id, err := resolveSessionID(args)
			if err != nil {
				return err
			}
			out, err := d.Status(ctx, id)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		}),
	}
}

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs [session-id]",
		Short: "Stream session logs from the VM (defaults to the most recent)",
		Args:  cobra.MaximumNArgs(1),
		RunE: withDispatcher(func(ctx context.Context, d *dispatch.Dispatcher, args []string) error {
			id, err := resolveSessionID(args)
			if err != nil {
				return err
			}
			return d.Logs(ctx, id)
		}),
	}
}

func newVMCmd() *cobra.Command {
	vm := &cobra.Command{Use: "vm", Short: "Manage the Lima VM worker"}

	vm.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Ensure the Lima VM is running",
		RunE: withDispatcher(func(ctx context.Context, d *dispatch.Dispatcher, args []string) error {
			return d.VM().Start(ctx)
		}),
	})

	vm.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Shut down the Lima VM",
		RunE: withDispatcher(func(ctx context.Context, d *dispatch.Dispatcher, args []string) error {
			return d.VM().Stop(ctx)
		}),
	})

	return vm
}

// withDispatcher wraps a handler with config loading, dispatcher construction,
// and a SIGINT/SIGTERM-cancellable context. Every cobra RunE in this CLI needs
// the same setup; this collapses the per-command boilerplate.
func withDispatcher(fn func(ctx context.Context, d *dispatch.Dispatcher, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return fn(ctx, dispatch.New(cfg), args)
	}
}

// resolveSessionID returns the explicit id if provided, otherwise the most
// recent submitted session.
func resolveSessionID(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	id, err := session.Latest()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no session id given and no recent session recorded; submit one with `crag run`")
	}
	return id, nil
}
