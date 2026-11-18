package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_run_jobs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewCompositeRunJobCommand creates the describe composite-run-job command.
func NewCompositeRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-run-job <id>",
		Short: "Show details of one composite recompute run",
		Long: `Show one composite recompute run: what invalidated it, which spec groups and
downstream tables it rebuilt, and the ids of the rule runs it recorded.

This is not the dry-run family (` + "`qctl describe dry-run-job`" + `): a composite run
job rebuilds a composite (linked) table for real, it does not preview anything.

Verbosity levels:
  (default)  Manifest: spec is what the invalidation asked for, status is what the run did
  -vv        Raw API response dump (all fields, useful for debugging)

Examples:
  qctl describe composite-run-job 123
  qctl describe composite-run-job 123 -o json
  qctl describe composite-run-job 123 -vv`,
		Annotations: cmdutil.WithFormats(catalog.For("composite-run-job"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				// Bad usage, not a generic error: a typed id is the user's
				// typo, and a script needs exit 2 to tell it from a server
				// failure it should retry.
				return qerrors.NewBadUsagef("invalid composite run job ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := composite_run_jobs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			result, err := client.GetCompositeRunJob(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return fmt.Errorf("failed to get composite run job: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			// For -vv (verbosity >= 2), output the raw API response.
			if ctx.Verbosity >= 2 {
				rawManifest, err := composite_run_jobs.APIResponseToRawManifest(result)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}
				return encodeCompositeRunJob(cmd, outputFormat, rawManifest, rawManifest)
			}

			manifest := composite_run_jobs.APIResponseToManifest(result)
			// The run's log lines and error message carry the backend's
			// Markdown, and so can msg: an infrastructure failure writes the
			// same text into both (atlas record_composite_run_infra_failure).
			markdownFields := []string{"message", "msg", "error_message"}
			processed := markdown.ProcessFieldsPlain(manifest, markdownFields)
			return encodeCompositeRunJob(cmd, outputFormat, manifest, processed)
		},
	}

	return cmd
}

// encodeCompositeRunJob renders one document in the requested format. table
// prints the structured value; json and yaml print the markdown-processed one,
// which is the same value when there is no markdown to process.
func encodeCompositeRunJob(cmd *cobra.Command, outputFormat string, structured, processed interface{}) error {
	switch outputFormat {
	case outputFormatJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(processed)
	case outputFormatTable:
		var message, errorMessage string
		if manifest, ok := structured.(*composite_run_jobs.CompositeRunJobManifest); ok && manifest.Status != nil {
			message = manifest.Status.Msg
			if manifest.Status.ErrorMessage != nil {
				errorMessage = *manifest.Status.ErrorMessage
			}
			manifest.Status.Msg = inlineTableMessage(manifest.Status.Msg)
			manifest.Status.ErrorMessage = inlineTableMessagePtr(manifest.Status.ErrorMessage)
			manifest.Status.Logs = inlineTableLogMessages(manifest.Status.Logs)
		}
		p, err := printer.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		if err := p.Print(structured); err != nil {
			return err
		}
		if err := writeTableMessageSection(cmd.OutOrStdout(), "Message", message); err != nil {
			return err
		}
		return writeTableMessageSection(cmd.OutOrStdout(), "Error Message", errorMessage)
	default:
		encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(processed)
	}
}
