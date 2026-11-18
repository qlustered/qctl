package download

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

// issuesReportJob names the job in messages.
const issuesReportJob = "issues report"

// NewIssuesReportCommand creates the 'download issues-report' command.
func NewIssuesReportCommand() *cobra.Command {
	opts := &exportOptions{}

	cmd := &cobra.Command{
		Use:   "issues-report <file-id>",
		Short: "Export a file's broker issues report as an Excel workbook",
		Long: `Export one file's issues report, the Excel workbook written for its broker,
and download it. It lists the file's held rows and its loaded rows carrying
warnings, on Overview, Rules and Issues sheets, plus a Glossary sheet when
the file saved at least one of its original headers.

The report runs on the server. By default qctl waits for it to finish and
writes the file; --no-wait queues it and returns.

A file that is not ready for a report is refused with exit 2, the reason
and a Hint: line. The refusal comes when the report is requested or, if the
file's state changed in between, when it runs, which ends the wait at once.
A report the server failed to build or store also ends it, with exit 1.
A report of this file already queued is not waited for: the command exits 2
and names the notifications it will appear under.

The file is named by the server: '<table> - Issues report <UTC date>.xlsx',
or 'Issues report <UTC date>.xlsx' when nothing of the table's name is left
once the characters a file name cannot hold are removed.

<file-id> is the id 'qctl get files' lists and 'qctl describe file' takes.

Examples:
  # file 123's issues report, into the working directory
  qctl download issues-report 123

  # to a path you pick
  qctl download issues-report 123 --output ./issues.xlsx

  # queue the report and return immediately
  qctl download issues-report 123 --no-wait`,
		Annotations: cmdutil.WithArgKinds(catalog.For("file"), "file"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runIssuesReport(cmd, args[0])
		},
	}

	opts.registerFlags(cmd, "the workbook", "report")
	return cmd
}

func (o *exportOptions) runIssuesReport(cmd *cobra.Command, arg string) error {
	ids, err := cmdutil.ParseIntArgs([]string{arg}, "file ID")
	if err != nil {
		return err
	}
	fileID := ids[0]
	if fileID <= 0 {
		return qerrors.NewBadUsagef("invalid file ID %d: file IDs are positive integers", fileID)
	}
	pollEvery, waitBudget, err := o.validate(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create stored items client: %w", err)
	}

	wait := notifications.WaitOptions{
		// No FileNameHint: the name carries the table and the day, so two
		// files of one table share it. The download_handle the POST returns
		// tells them apart; queueAndCollect fills it in.
		Producer: notifications.ProducerBrokerIssuesReport,
		Interval: pollEvery,
		Timeout:  waitBudget,
	}
	if !o.noWait {
		// Keep the table for announcements without a handle.
		item, err := client.GetStoredItem(token, fileID)
		if err != nil {
			return fmt.Errorf("failed to get file %d: %w", fileID, err)
		}
		wait.DatasetID = item.DatasetID
	}

	err = o.queueAndCollect(cmd, ctx, queuedJob{
		label: issuesReportJob,
		// Not waited for: the refusal carries no download_handle, and without
		// one the wait accepts any newer report row of the table, another
		// file's included, because every file's report of a table shares the
		// day's name.
		queuedAdvice: "this file's issues report is already queued; once it appears in " +
			"'qctl get notifications --type file_ready_to_download --type file_download_failed', " +
			"run this again to download it",
		send: func() (string, error) {
			return client.PutIssuesReportForDownload(token, fileID)
		},
	}, wait)
	return explainIssuesReportFailure(err, fileID)
}

// explainIssuesReportFailure appends a Hint: line to the route's 409
// BROKER_REPORT_REFUSED and to a *notifications.DownloadFailedError, keeping
// the %w chain, and returns any other error unchanged.
func explainIssuesReportFailure(err error, fileID int) error {
	var failed *notifications.DownloadFailedError
	if errors.As(err, &failed) {
		refusal := string(failed.Refusal())
		if refusal == "" {
			return fmt.Errorf("%w\n\nHint: re-running this command requests the %s again", err, issuesReportJob)
		}
		return fmt.Errorf("%w\n\nHint: %s", err, stored_items.IssuesReportRefusalAdvice(refusal, fileID, "download"))
	}
	var apiErr *stored_items.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != stored_items.CodeBrokerReportRefused {
		return err
	}
	reason := apiErr.ContextString("reason")
	hint := stored_items.IssuesReportRefusalAdvice(reason, fileID, "download")
	if reason != "" {
		hint += " (reason: " + reason + ")"
	}
	return fmt.Errorf("%w\n\nHint: %s", err, hint)
}
