package get

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
)

// NewDryRunJobCommand creates the 'get dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run-job <id>",
		Short: "Get a dry-run job",
		Long: `Get a dry-run job by ID.

Use --watch to stream state updates via WebSocket until the job reaches a
terminal state: finished, error, killed, stopped, version_id_mismatch,
file_missing, blocked or partitioned. A job already in one is printed once,
without opening the stream. --watch exits 0 when the job finished; any other
terminal state exits 1 with a Next: line, and running out of --timeout exits
124 and leaves the job running.

Examples:
  # Get a dry-run job
  qctl get dry-run-job 123

  # Get with JSON output
  qctl get dry-run-job 123 -o json

  # Watch for state changes
  qctl get dry-run-job 123 --watch`,
		Annotations: catalog.For("dry-run-job"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid dry-run job ID: %s", args[0])
			}

			watch, _ := cmd.Flags().GetBool("watch")
			if !watch && cmd.Flags().Changed("timeout") {
				return qerrors.NewBadUsage("--timeout requires --watch")
			}
			var budget time.Duration
			if watch {
				if budget, err = cmdutil.WatchBudget(cmd); err != nil {
					return err
				}
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			if watch {
				return watchDryRunJob(cmd, ctx, client, jobID, budget)
			}

			return getDryRunJob(cmd, ctx, client, jobID)
		},
	}

	cmd.Flags().Bool("watch", false, "Watch for state changes via WebSocket")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)

	cmdutil.DeclareColumns(cmd, []dry_runs.DryRunJobFull(nil))
	return cmd
}

// getDryRunJob fetches and prints a single dry-run job
func getDryRunJob(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, jobID int) error {
	job, err := client.GetDryRunJob(ctx.Credential.AccessToken, jobID)
	if err != nil {
		return fmt.Errorf("failed to get dry-run job: %w", err)
	}

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, dryRunMessageFields)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printer.Print(job)
}

// newDryRunJobFrameWriter returns the function --watch hands each websocket
// frame to, which writes it and returns its state. json and yaml go to an
// output.Stream, one compact frame per line or one document per frame; -o name
// prints dry-run-job/<id> and the default view a "dry-run-job <id>: <state>" line. A
// frame that is not JSON is reported on stderr and skipped.
func newDryRunJobFrameWriter(cmd *cobra.Command, jobID int) (func(json.RawMessage) (string, error), error) {
	formatStr, _ := cmd.Flags().GetString("output")
	format := output.Format(formatStr)
	w := cmd.OutOrStdout()

	var stream *output.Stream
	if format == output.FormatJSON || format == output.FormatYAML {
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return nil, err
		}
		stream = printer.NewStream(isTerminal(w), true)
	}

	return func(raw json.RawMessage) (string, error) {
		var frame map[string]any
		if err := jsonnum.Unmarshal(raw, &frame); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			return "", nil
		}
		state, _ := frame["state"].(string)
		switch {
		case stream != nil:
			return state, stream.Write(frame)
		case format == output.FormatName:
			fmt.Fprintf(w, "dry-run-job/%d\n", jobID)
		default:
			fmt.Fprintf(w, "dry-run-job %d: %s\n", jobID, terminal.Sanitize(state))
		}
		return state, nil
	}, nil
}

// watchDryRunJob reads the job and, unless its state is already terminal,
// follows its progress stream for up to budget (0 for no limit), writing each
// state through newDryRunJobFrameWriter. It returns the job's Verdict, or why
// the watch ended without one.
func watchDryRunJob(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, client *dry_runs.Client, jobID int, budget time.Duration) error {
	write, err := newDryRunJobFrameWriter(cmd, jobID)
	if err != nil {
		return err
	}
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
	if dry_runs.IsTerminal(job.State) {
		if _, err := write(dry_runs.StateFrame(job.State)); err != nil {
			return err
		}
		return w.Ended(client.VerdictContext(ctx, token, job))
	}

	return w.Ended(client.Follow(ctx, token, job, dry_runs.FollowOptions{
		OnFrame:     write,
		OnReconnect: ws.ReconnectNotice(cmd.ErrOrStderr()),
		Resume:      resume,
	}))
}
