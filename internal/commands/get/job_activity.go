package get

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// JobActivity is the unified model for job activity status.
// Both REST and WebSocket responses are mapped into this struct.
type JobActivity struct {
	RunningIngestionJobs  int  `json:"running_ingestion_jobs" yaml:"running_ingestion_jobs"`
	WaitingIngestionJobs  int  `json:"waiting_ingestion_jobs" yaml:"waiting_ingestion_jobs"`
	RunningProfilingJobs  int  `json:"running_profiling_jobs" yaml:"running_profiling_jobs"`
	ShouldRefreshDatagrid bool `json:"should_refresh_datagrid" yaml:"should_refresh_datagrid"`
}

// jobActivityFromREST maps the REST response to the unified model.
func jobActivityFromREST(resp *datasets.JobRunningCountResponse) JobActivity {
	return JobActivity{
		RunningIngestionJobs:  resp.IngestionJobCount,
		WaitingIngestionJobs:  resp.WaitingIngestionJobCount,
		RunningProfilingJobs:  resp.TrainingJobCount,
		ShouldRefreshDatagrid: false, // REST endpoint doesn't provide this
	}
}

// wsMessage represents the WebSocket message with its own field names.
type wsMessage struct {
	RunningIngestionJobCount int  `json:"running_ingestion_job_count"`
	RunningTrainingJobCount  int  `json:"running_training_job_count"`
	WaitingIngestionJobCount int  `json:"waiting_ingestion_job_count"`
	ShouldRefreshDatagrid    bool `json:"should_refresh_datagrid"`
}

// jobActivityFromWS maps a WebSocket message to the unified model.
func jobActivityFromWS(raw json.RawMessage) (JobActivity, error) {
	var msg wsMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return JobActivity{}, fmt.Errorf("failed to parse websocket message: %w", err)
	}
	return JobActivity{
		RunningIngestionJobs:  msg.RunningIngestionJobCount,
		WaitingIngestionJobs:  msg.WaitingIngestionJobCount,
		RunningProfilingJobs:  msg.RunningTrainingJobCount,
		ShouldRefreshDatagrid: msg.ShouldRefreshDatagrid,
	}, nil
}

// runJobActivityOneShot fetches job activity via REST and prints it once.
func runJobActivityOneShot(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int) error {
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	resp, err := client.GetDatasetJobActivity(ctx.Credential.AccessToken, tableID, nil)
	if err != nil {
		return fmt.Errorf("failed to get job activity: %w", err)
	}

	activity := jobActivityFromREST(resp)
	return printJobActivity(cmd, activity)
}

// printJobActivity prints a single JobActivity snapshot in the requested format.
func printJobActivity(cmd *cobra.Command, activity JobActivity) error {
	formatStr, _ := cmd.Flags().GetString("output")
	format := output.Format(formatStr)
	w := cmd.OutOrStdout()

	switch format {
	case output.FormatJSON:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(activity)
	case output.FormatYAML:
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(activity)
	default:
		fmt.Fprintf(w, "ingestion: running=%d waiting=%d\n", activity.RunningIngestionJobs, activity.WaitingIngestionJobs)
		fmt.Fprintf(w, "profiling: running=%d\n", activity.RunningProfilingJobs)
		return nil
	}
}

// runJobActivityWatch streams job activity updates via WebSocket.
func runJobActivityWatch(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, tableID int, provider string, changesOnly bool) error {
	// Build WebSocket path
	wsPath := buildWSPath(cmdCtx.OrganizationID, tableID, provider)

	c := ws.NewClient(ws.Config{
		BaseURL:     cmdCtx.ServerURL,
		AccessToken: cmdCtx.Credential.AccessToken,
		Path:        wsPath,
		Verbosity:   cmdCtx.Verbosity,
	})

	// Setup signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}
	defer c.Close()

	msgs, errCh := c.ReadMessages(ctx)

	formatStr, _ := cmd.Flags().GetString("output")
	format := output.Format(formatStr)
	w := cmd.OutOrStdout()
	isTTY := isTerminal(w)

	var prev *JobActivity
	var prevLines int

	for {
		select {
		case raw, ok := <-msgs:
			if !ok {
				return nil
			}

			activity, err := jobActivityFromWS(raw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
				continue
			}

			// --changes-only: skip if unchanged
			if changesOnly && prev != nil && reflect.DeepEqual(*prev, activity) {
				continue
			}
			prev = &JobActivity{}
			*prev = activity

			n, err := renderWatchUpdate(w, format, activity, tableID, isTTY, prevLines)
			if err != nil {
				return err
			}
			prevLines = n

		case err, ok := <-errCh:
			if ok && err != nil {
				return fmt.Errorf("websocket error: %w", err)
			}
			return nil

		case <-ctx.Done():
			return nil
		}
	}
}

func buildWSPath(orgID string, tableID int, provider string) string {
	if provider == "third-party" {
		return fmt.Sprintf("/api/orgs/%s/ws/third-party/datasets/%d/job-activity", orgID, tableID)
	}
	return fmt.Sprintf("/api/orgs/%s/ws/datasets/%d/job-activity", orgID, tableID)
}

func renderWatchUpdate(w io.Writer, format output.Format, activity JobActivity, tableID int, isTTY bool, prevLines int) (int, error) {
	switch format {
	case output.FormatJSON:
		// NDJSON: compact JSON, one per line
		data, err := json.Marshal(activity)
		if err != nil {
			return 0, err
		}
		fmt.Fprintln(w, string(data))
		return 1, nil

	case output.FormatYAML:
		// YAML multi-doc
		fmt.Fprintln(w, "---")
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		if err := encoder.Encode(activity); err != nil {
			return 0, err
		}
		encoder.Close()
		return 0, nil

	default:
		lines := []string{
			fmt.Sprintf("Table %d", tableID),
			fmt.Sprintf("  Ingestion Running:  %d", activity.RunningIngestionJobs),
			fmt.Sprintf("  Ingestion Waiting:  %d", activity.WaitingIngestionJobs),
			fmt.Sprintf("  Profiling Running:  %d", activity.RunningProfilingJobs),
		}

		if isTTY && prevLines > 0 {
			// Move cursor up and clear to redraw in place
			fmt.Fprintf(w, "\033[%dA\033[J", prevLines)
		}

		for _, line := range lines {
			fmt.Fprintln(w, line)
		}

		return len(lines), nil
	}
}

// isTerminal checks if the writer is a terminal (for in-place updates).
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}
