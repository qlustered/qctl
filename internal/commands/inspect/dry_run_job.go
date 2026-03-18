package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Terminal states for dry-run jobs
var terminalStates = map[string]bool{
	"finished": true,
	"error":    true,
	"killed":   true,
}

// NewDryRunJobCommand creates the 'inspect dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	var (
		view    string
		follow  bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "dry-run-job <id>",
		Short: "Inspect dry-run job preview artifacts",
		Long: `Inspect the preview results of a dry-run job.

Two views are available:
  compact  Summarized stats and exemplar rows (default)
  full     Full row-level diff data

Use --follow to stream progress updates via WebSocket until the job reaches
a terminal state, then fetch and display the preview artifact.

Examples:
  # View compact preview
  qctl inspect dry-run-job 123

  # View full preview
  qctl inspect dry-run-job 123 --view full

  # Follow progress then show compact preview
  qctl inspect dry-run-job 123 --follow

  # Follow with timeout
  qctl inspect dry-run-job 123 --follow --timeout 5m`,
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

			if follow {
				return runFollowMode(cmd, ctx, client, jobID, view, timeout)
			}

			return runOneShotMode(cmd, ctx, client, jobID, view)
		},
	}

	cmd.Flags().StringVar(&view, "view", "compact", "Preview view (compact|full)")
	cmd.Flags().BoolVar(&follow, "follow", false, "Stream progress then show final artifact")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Timeout for follow mode")

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

// runFollowMode streams WS updates then fetches the artifact
func runFollowMode(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, client *dry_runs.Client, jobID int, view string, timeout time.Duration) error {
	wsPath := fmt.Sprintf("/api/orgs/%s/ws/dry-runs/%d", cmdCtx.OrganizationID, jobID)

	c := ws.NewClient(ws.Config{
		BaseURL:     cmdCtx.ServerURL,
		AccessToken: cmdCtx.Credential.AccessToken,
		Path:        wsPath,
		Verbosity:   cmdCtx.Verbosity,
	})

	// Setup context with timeout and signal handling
	bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sigCtx, stop := signal.NotifyContext(bgCtx, os.Interrupt)
	defer stop()

	if err := c.Connect(sigCtx); err != nil {
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}
	defer c.Close()

	msgs, errCh := c.ReadMessages(sigCtx)

	// Stream state updates to stderr
	for {
		select {
		case raw, ok := <-msgs:
			if !ok {
				return fetchAndPrintArtifact(cmd, cmdCtx, client, jobID, view)
			}

			// Parse state from message
			var stateMsg struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(raw, &stateMsg); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to parse WS message: %v\n", err)
				continue
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "dry-run-job %d: %s\n", jobID, stateMsg.State)

			if terminalStates[stateMsg.State] {
				return fetchAndPrintArtifact(cmd, cmdCtx, client, jobID, view)
			}

		case err, ok := <-errCh:
			if ok && err != nil {
				return fmt.Errorf("websocket error: %w", err)
			}
			return fetchAndPrintArtifact(cmd, cmdCtx, client, jobID, view)

		case <-sigCtx.Done():
			if bgCtx.Err() != nil {
				return fmt.Errorf("timed out waiting for dry-run job %d to complete", jobID)
			}
			return nil
		}
	}
}

// fetchAndPrintArtifact fetches the preview artifact after follow mode completes
func fetchAndPrintArtifact(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, jobID int, view string) error {
	return runOneShotMode(cmd, ctx, client, jobID, view)
}

// printResult outputs the result in the requested format
func printResult(w io.Writer, formatStr string, data interface{}) error {
	format := output.Format(formatStr)

	switch format {
	case output.FormatYAML:
		// Marshal to JSON first to use json struct tags, then unmarshal to
		// interface{} so the YAML encoder preserves the snake_case field names.
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("failed to marshal to JSON: %w", err)
		}
		var generic interface{}
		if err := json.Unmarshal(jsonBytes, &generic); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(generic)
	default:
		// JSON (default for inspect)
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)
	}
}
