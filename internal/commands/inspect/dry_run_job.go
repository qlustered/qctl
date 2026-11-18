package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
)

// NewDryRunJobCommand creates the 'inspect dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	var (
		view   string
		follow bool
	)

	cmd := &cobra.Command{
		Use:   "dry-run-job <id>",
		Short: "Inspect dry-run job preview artifacts",
		Long: `Inspect the preview results of a dry-run job.

Two views are available:
  compact  Summarized stats and exemplar rows (default)
  full     Full row-level diff data

Use --follow to stream progress updates to stderr via WebSocket until the job
reaches a terminal state: finished, error, killed, stopped,
version_id_mismatch, file_missing, blocked or partitioned. A finished job's
preview is then printed and the command exits 0; any other terminal state
prints no preview and exits 1 with a Next: line, and running out of --timeout
exits 124 and leaves the job running.

Examples:
  # View compact preview
  qctl inspect dry-run-job 123

  # View full preview
  qctl inspect dry-run-job 123 --view full

  # Follow progress then show compact preview
  qctl inspect dry-run-job 123 --follow

  # Follow with timeout
  qctl inspect dry-run-job 123 --follow --timeout 5m`,
		// printResult defaults to JSON and renders YAML for -o yaml; there is no
		// table view to ask for, so "table" is deliberately absent even though it
		// is the root --output flag's default. That is safe: the validator only
		// checks --output when the user actually passed it.
		Annotations: cmdutil.WithFormats(catalog.For("dry-run-job"), "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid dry-run job ID: %s", args[0])
			}

			if !follow && cmd.Flags().Changed("timeout") {
				return qerrors.NewBadUsage("--timeout requires --follow")
			}
			var budget time.Duration
			if follow {
				if budget, err = cmdutil.WatchBudget(cmd); err != nil {
					return err
				}
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			if follow {
				if err := followDryRunJob(cmd, ctx, client, jobID, budget); err != nil {
					return err
				}
			}

			return runOneShotMode(cmd, ctx, client, jobID, view)
		},
	}

	cmd.Flags().StringVar(&view, "view", "compact", "Preview view (compact|full)")
	cmd.Flags().BoolVar(&follow, "follow", false, "Stream progress to stderr, then show the preview if the job finished")
	cmdutil.AddWatchTimeoutFlag(cmd, 10*time.Minute)

	return cmd
}

// runOneShotMode fetches the preview and prints it
func runOneShotMode(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, jobID int, view string) error {
	formatStr, _ := cmd.Flags().GetString("output")
	w := cmd.OutOrStdout()

	if view == "full" {
		preview, err := client.GetDryRunJobPreview(ctx.Credential.AccessToken, jobID)
		if err != nil {
			return fmt.Errorf("failed to get full preview: %w", err)
		}
		return printResult(w, formatStr, preview)
	}

	// compact (default)
	preview, err := client.GetDryRunJobPreviewCompact(ctx.Credential.AccessToken, jobID)
	if err != nil {
		return fmt.Errorf("failed to get compact preview: %w", err)
	}
	return printResult(w, formatStr, preview)
}

// followDryRunJob reads the job and, unless its state is already terminal,
// follows its progress stream for up to budget (0 for no limit), writing a
// "dry-run-job <id>: <state>" line to stderr for each state. It returns the
// job's Verdict, or why the follow ended without one.
func followDryRunJob(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, client *dry_runs.Client, jobID int, budget time.Duration) error {
	token := cmdCtx.Credential.AccessToken
	resume := cmdutil.ResumeCommand(cmd)
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:   cmdutil.WaitUntilDone,
		Budget: budget,
		Doing:  fmt.Sprintf("waiting for dry-run-job %d to finish", jobID),
		Next:   resume,
	})
	defer w.Stop()
	job, err := client.GetDryRunJobContext(ctx, token, jobID)
	if err != nil {
		return w.Ended(fmt.Errorf("failed to get dry-run job: %w", err))
	}
	if ctx.Err() != nil {
		return w.Ended(context.Cause(ctx))
	}
	progress := func(raw json.RawMessage) (string, error) {
		var frame struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to parse WS message: %v\n", err)
			return "", nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "dry-run-job %d: %s\n", jobID, terminal.Sanitize(frame.State))
		return frame.State, nil
	}
	if dry_runs.IsTerminal(job.State) {
		_, _ = progress(dry_runs.StateFrame(job.State))
		return w.Ended(client.VerdictContext(ctx, token, job))
	}

	return w.Ended(client.Follow(ctx, token, job, dry_runs.FollowOptions{
		OnFrame:     progress,
		OnReconnect: ws.ReconnectNotice(cmd.ErrOrStderr()),
		Resume:      resume,
	}))
}

// printResult writes data as YAML for -o yaml and as JSON for every other format.
func printResult(w io.Writer, formatStr string, data interface{}) error {
	if output.Format(formatStr) != output.FormatYAML {
		formatStr = string(output.FormatJSON)
	}
	return output.WriteRaw(w, formatStr, data)
}
