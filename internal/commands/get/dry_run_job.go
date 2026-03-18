package get

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
)

// NewDryRunJobCommand creates the 'get dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run-job <id>",
		Short: "Get a dry-run job",
		Long: `Get a dry-run job by ID.

Use --watch to stream state updates via WebSocket until the job reaches a terminal state.

Examples:
  # Get a dry-run job
  qctl get dry-run-job 123

  # Get with JSON output
  qctl get dry-run-job 123 -o json

  # Watch for state changes
  qctl get dry-run-job 123 --watch`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid dry-run job ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			watch, _ := cmd.Flags().GetBool("watch")
			if watch {
				return watchDryRunJob(cmd, ctx, client, jobID)
			}

			return getDryRunJob(cmd, ctx, client, jobID)
		},
	}

	cmd.Flags().Bool("watch", false, "Watch for state changes via WebSocket")

	return cmd
}

// getDryRunJob fetches and prints a single dry-run job
func getDryRunJob(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, jobID int) error {
	job, err := client.GetDryRunJob(ctx.Credential.AccessToken, jobID)
	if err != nil {
		return fmt.Errorf("failed to get dry-run job: %w", err)
	}

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printer.Print(job)
}

// watchDryRunJob streams state updates via WebSocket
func watchDryRunJob(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, client *dry_runs.Client, jobID int) error {
	wsPath := fmt.Sprintf("/api/orgs/%s/ws/dry-runs/%d", cmdCtx.OrganizationID, jobID)

	c := ws.NewClient(ws.Config{
		BaseURL:     cmdCtx.ServerURL,
		AccessToken: cmdCtx.Credential.AccessToken,
		Path:        wsPath,
		Verbosity:   cmdCtx.Verbosity,
	})

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := c.Connect(sigCtx); err != nil {
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}
	defer c.Close()

	msgs, errCh := c.ReadMessages(sigCtx)
	formatStr, _ := cmd.Flags().GetString("output")
	format := output.Format(formatStr)
	w := cmd.OutOrStdout()

	for {
		select {
		case raw, ok := <-msgs:
			if !ok {
				return nil
			}

			switch format {
			case output.FormatJSON:
				// NDJSON
				fmt.Fprintln(w, string(raw))
			default:
				var stateMsg struct {
					State string `json:"state"`
				}
				if err := json.Unmarshal(raw, &stateMsg); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
					continue
				}
				fmt.Fprintf(w, "dry-run-job %d: %s\n", jobID, stateMsg.State)
			}

			// Check for terminal state
			var stateMsg struct {
				State string `json:"state"`
			}
			if json.Unmarshal(raw, &stateMsg) == nil {
				if stateMsg.State == "finished" || stateMsg.State == "error" || stateMsg.State == "killed" {
					return nil
				}
			}

		case err, ok := <-errCh:
			if ok && err != nil {
				return fmt.Errorf("websocket error: %w", err)
			}
			return nil

		case <-sigCtx.Done():
			return nil
		}
	}
}
