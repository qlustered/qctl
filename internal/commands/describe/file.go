package describe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewFileCommand creates the 'describe file' command
func NewFileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Show details of a specific file",
		Long: `Show detailed information about a specific file (stored item).

For YAML output (default), the file is converted into a manifest-shaped document. JSON
output returns the same shape. It is read-only: apply -f refuses kind: File; change
is_trashed with 'qctl delete file' / 'qctl undelete file'. On a trashed file,
status.trash records who trashed it, when and why, and the rows it deleted.

For a file of a cloud source granted to you, status.submission shows its way to
its table: state, outcome, table series and period, and each step with what it
waits on, including the change proposal a step waits for review or revision of.

status.quarantine splits the file's undeleted quarantine rows, counted now:
waiting_for_approval_count (rows that pass validation and wait for approval)
and rows_to_fix_count (every other row). Both are null while the file waits
for routing or when its table's loading process is not append,
append_skip_identical_rows or mirror. It is left out when the read is refused:
for an external caller, or one without source:data:read on the file's table.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: File
  metadata:
    name: orders.csv
    labels:
      dataset_id: "11"
      data_source_model_id: "22"
  spec:
    dataset_id: 11
    dataset_name: orders
    cloud_source_id: 22
    cloud_source_name: s3-bucket
    file_name: orders.csv
    file_type: csv
    encoding: utf-8
    is_trashed: false
    backup_key: org/1/orders.csv
    csv_delimiter: ","
    csv_escapechar: "\\"
    csv_quotechar: "\""
    header_line_number_for_file: 1
    row_number_for_first_line_of_data: 2`,
		Annotations: cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse file ID from argument
			fileID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid file ID '%s': must be a number", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get file
			result, err := client.GetStoredItem(ctx.Credential.AccessToken, fileID)
			if err != nil {
				return fmt.Errorf("failed to get file: %w", err)
			}

			manifest := stored_items.APIResponseToManifest(result)
			manifest.Status.ReplacementQuestion = replacementQuestion(cmd.ErrOrStderr(), client, ctx.Credential.AccessToken, fileID)
			manifest.Status.Submission = submissionProgress(cmd.ErrOrStderr(), client, ctx.Credential.AccessToken, fileID)
			manifest.Status.Quarantine = fileQuarantine(cmd.ErrOrStderr(), client, ctx.Credential.AccessToken, fileID)
			printFileHints(cmd.ErrOrStderr(), fileID, manifest.Status)

			// Determine output format (default to YAML manifest for describe)
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			switch outputFormat {
			case outputFormatJSON:
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(manifest)
			case outputFormatTable:
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifest)
			default:
				encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(manifest)
			}
		},
	}

	return cmd
}

// replacementQuestion reads the file's replacement question. A 403 (the
// caller may not answer it) or a 404 (a server without the route) leaves it
// out silently; any other failure leaves it out with a warning.
func replacementQuestion(out io.Writer, client *stored_items.Client, token string, fileID int) *stored_items.FileReplacementQuestion {
	question, err := client.GetReplacementQuestion(token, fileID)
	if err != nil {
		if refusedOrMissing(err) {
			return nil
		}
		fmt.Fprintf(out, "warning: replacement question unavailable: %v\n", err)
		return nil
	}
	return stored_items.NewFileReplacementQuestion(question)
}

// submissionProgress reads the file's submission progress. A 403 or a 404 (a
// file of no source granted to the caller, or a server without the route)
// leaves it out silently; any other failure leaves it out with a warning.
func submissionProgress(out io.Writer, client *stored_items.Client, token string, fileID int) *stored_items.FileSubmission {
	progress, err := client.GetSubmissionProgress(token, fileID)
	if err != nil {
		if refusedOrMissing(err) {
			return nil
		}
		fmt.Fprintf(out, "warning: submission progress unavailable: %v\n", err)
		return nil
	}
	return stored_items.NewFileSubmission(progress)
}

// fileQuarantine reads the file's quarantine split from its progress. A 403
// (an external caller, or one without source-data read on the file's table)
// or a 404 leaves it out silently; any other failure leaves it out with a
// warning.
func fileQuarantine(out io.Writer, client *stored_items.Client, token string, fileID int) *stored_items.FileQuarantine {
	progress, err := client.GetFileProgress(token, fileID)
	if err != nil {
		if refusedOrMissing(err) {
			return nil
		}
		fmt.Fprintf(out, "warning: quarantine counts unavailable: %v\n", err)
		return nil
	}
	return stored_items.NewFileQuarantine(progress)
}

// refusedOrMissing reports whether err is a 403 or a 404 answer.
func refusedOrMissing(err error) bool {
	var apiErr *stored_items.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound)
}

// printFileHints names the commands that answer an open replacement question,
// read the submission email's discussion, list the lines an earlier delete
// dropped and read a change proposal a step waits on.
func printFileHints(out io.Writer, fileID int, status *stored_items.FileStatus) {
	if q := status.ReplacementQuestion; q != nil && q.Answer == nil {
		fmt.Fprintf(out, "File %d asks whether it replaces file %d (%s). Answer with one of:\n",
			fileID, q.OlderStoredItemID, terminal.Sanitize(q.OldFileName))
		fmt.Fprintf(out, "  qctl file replace-older %d   # discard file %d as replaced by this one\n", fileID, q.OlderStoredItemID)
		fmt.Fprintf(out, "  qctl file keep-both %d       # keep both files\n", fileID)
	}
	if thread := status.SubmissionCommentThread; thread != nil {
		fmt.Fprintf(out, "Submission email: qctl describe comment-thread %s --table-id %d\n", thread.ID, thread.DatasetID)
	}
	printDroppedLinesHint(out, fileID, status.DeletedLinesCount)
	if sub := status.Submission; sub != nil {
		for _, step := range sub.Steps {
			if w := step.ChangeProposal; w != nil {
				fmt.Fprintf(out, "Step %s of file %d waits on change proposal %s (%s): qctl describe change-proposal %s\n",
					terminal.Sanitize(step.Step), fileID, w.ID, terminal.Sanitize(w.State), w.ID)
			}
		}
	}
}

// printDroppedLinesHint names get dropped-lines for fileID when count is
// positive.
func printDroppedLinesHint(out io.Writer, fileID int, count *int) {
	if count == nil || *count <= 0 {
		return
	}
	fmt.Fprintf(out, "Next: qctl get dropped-lines %d   # %d line(s) not loaded because an earlier delete excluded them\n", fileID, *count)
}
