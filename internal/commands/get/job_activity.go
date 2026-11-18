package get

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	qclient "github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/ws"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const thirdPartyProvider = "third-party"

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
		encoder := jsonnum.NewYAMLEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(activity)
	default:
		fmt.Fprintf(w, "ingestion: running=%d waiting=%d\n", activity.RunningIngestionJobs, activity.WaitingIngestionJobs)
		fmt.Fprintf(w, "profiling: running=%d\n", activity.RunningProfilingJobs)
		return nil
	}
}

// runJobActivityWatch streams tableID's job activity updates over a WebSocket.
// label names the table in the stderr banner; budget is the watch's time
// limit, 0 meaning none.
func runJobActivityWatch(cmd *cobra.Command, cmdCtx *cmdutil.CommandContext, tableID int, label string, budget time.Duration, provider string, changesOnly bool) (err error) {
	client := datasets.NewClient(cmdCtx.ServerURL, cmdCtx.OrganizationID, cmdCtx.Verbosity)
	if provider == thirdPartyProvider {
		_, err = client.GetThirdPartyDataset(cmdCtx.Credential.AccessToken, tableID)
	} else {
		_, err = client.GetDataset(cmdCtx.Credential.AccessToken, tableID)
	}
	if err != nil {
		return fmt.Errorf("failed to get table: %w", err)
	}

	write, notices, err := newJobActivityWriter(cmd, tableID, changesOnly)
	if err != nil {
		return err
	}

	c := ws.NewClient(ws.Config{
		BaseURL:     cmdCtx.ServerURL,
		AccessToken: cmdCtx.Credential.AccessToken,
		Path:        buildWSPath(cmdCtx.OrganizationID, tableID, provider),
		Verbosity:   cmdCtx.Verbosity,
		OnReconnect: ws.ReconnectNotice(notices),
	})

	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:   cmdutil.WatchStream,
		Budget: budget,
		Doing:  "watching job activity on table " + label,
		Next:   cmdutil.ResumeCommand(cmd),
	})
	defer w.Stop()
	// Deferred functions run last-in first-out: the stream closes, then Ended
	// classifies the outcome, then Stop releases the signal handler.
	defer func() { err = w.Ended(err) }()
	ctx = qclient.WithDiagnosticWriter(ctx, notices)

	if err := c.Connect(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}
	defer c.Close()

	msgs, errCh := c.ReadMessages(ctx)
	for {
		select {
		case raw, ok := <-msgs:
			if !ok {
				// errCh is closed before msgs, so this receive does not block.
				if err := <-errCh; err != nil {
					return fmt.Errorf("websocket error: %w", err)
				}
				return nil
			}

			activity, err := jobActivityFromWS(raw)
			if err != nil {
				fmt.Fprintf(notices, "warning: %v\n", err)
				continue
			}
			if err := write(activity); err != nil {
				return err
			}

		case <-ctx.Done():
			return nil
		}
	}
}

func buildWSPath(orgID string, tableID int, provider string) string {
	if provider == thirdPartyProvider {
		return fmt.Sprintf("/api/orgs/%s/ws/third-party/datasets/%d/job-activity", orgID, tableID)
	}
	return fmt.Sprintf("/api/orgs/%s/ws/datasets/%d/job-activity", orgID, tableID)
}

// newJobActivityWriter returns the update function and notice writer:
// json and yaml go to an output.Stream, one compact object per line or one
// document per update, and the text view is redrawn in place on a terminal.
// With changesOnly an update equal to the last one written is dropped.
func newJobActivityWriter(cmd *cobra.Command, tableID int, changesOnly bool) (func(JobActivity) error, io.Writer, error) {
	formatStr, _ := cmd.Flags().GetString("output")
	w := cmd.OutOrStdout()
	isTTY := isTerminal(w)

	if format := output.Format(formatStr); format == output.FormatJSON || format == output.FormatYAML {
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return nil, nil, err
		}
		stream := printer.NewStream(isTTY, changesOnly)
		return func(activity JobActivity) error { return stream.Write(activity) }, stream.NoticeWriter(cmd.ErrOrStderr()), nil
	}

	stream := output.NewPrinter(output.Options{Writer: w}).NewStream(isTTY, false)
	var prev *JobActivity
	return func(activity JobActivity) error {
		if changesOnly && prev != nil && *prev == activity {
			return nil
		}
		prev = &activity
		return stream.WriteTable(renderJobActivityText(activity, tableID))
	}, stream.NoticeWriter(cmd.ErrOrStderr()), nil
}

// renderJobActivityText returns activity as the watch's text view.
func renderJobActivityText(activity JobActivity, tableID int) string {
	return fmt.Sprintf("Table %d\n  Ingestion Running:  %d\n  Ingestion Waiting:  %d\n  Profiling Running:  %d\n",
		tableID, activity.RunningIngestionJobs, activity.WaitingIngestionJobs, activity.RunningProfilingJobs)
}

// isTerminal checks if the writer is a terminal (for in-place updates).
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}
