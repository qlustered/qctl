package describe

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewCompositeDryRunJobCommand creates the describe composite-dry-run-job command.
func NewCompositeDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-dry-run-job <id>",
		Short: "Show details of one composite (linked-table) dry run",
		Long: `Show one composite dry run: which spec revision it previewed against which
linked table, how far it got, and its structured summary once it finishes.

This is not the rule dry-run family (` + "`qctl describe dry-run-job`" + `). For the
per-row evidence behind the summary, use ` + "`qctl inspect composite-dry-run-job`" + `.

Verbosity levels:
  (default)  Manifest: spec is what was launched, status is what the dry run did
  -vv        Raw API response dump (all fields, including metrics)

Examples:
  qctl describe composite-dry-run-job 5512
  qctl describe composite-dry-run-job 5512 -o json
  qctl describe composite-dry-run-job 5512 -vv`,
		Annotations: cmdutil.WithFormats(catalog.For("composite-dry-run-job"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid composite dry run job ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			detail, err := client.Get(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return fmt.Errorf("failed to get composite dry run job: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			if ctx.Verbosity >= 2 {
				rawManifest, err := composite_dry_runs.ToRawManifest(detail)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}
				return encodeCompositeDryRunJob(cmd, outputFormat, rawManifest, rawManifest)
			}

			manifest := composite_dry_runs.ToManifest(detail)
			// summary is build_composite_markdown's output, value markers
			// included. error_message is a stringified exception, rendered
			// the same way the table view's Error Message section renders it.
			markdownFields := []string{"summary", "error_message"}
			processed := markdown.ProcessFieldsPlain(manifest, markdownFields)
			return encodeCompositeDryRunJob(cmd, outputFormat, manifest, processed)
		},
	}

	return cmd
}

// encodeCompositeDryRunJob renders one document in the requested format. table
// prints the structured value; json and yaml print the markdown-processed one.
func encodeCompositeDryRunJob(cmd *cobra.Command, outputFormat string, structured, processed interface{}) error {
	switch outputFormat {
	case outputFormatJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(processed)
	case outputFormatTable:
		var summary, errorMessage string
		if manifest, ok := structured.(*composite_dry_runs.CompositeDryRunJobManifest); ok && manifest.Status != nil {
			summary = manifest.Status.Summary
			// error_message is the specific detail; user_message is the web
			// wizard's display copy, so it follows.
			var parts []string
			for _, part := range []*string{manifest.Status.ErrorMessage, manifest.Status.UserMessage} {
				if part != nil && *part != "" {
					parts = append(parts, *part)
				}
			}
			errorMessage = strings.Join(parts, "\n\n")
			manifest.Status.Summary = inlineTableMessage(manifest.Status.Summary)
			manifest.Status.ErrorMessage = inlineTableMessagePtr(manifest.Status.ErrorMessage)
		}
		p, err := printer.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		if err := p.Print(structured); err != nil {
			return err
		}
		if err := writeTableMessageSection(cmd.OutOrStdout(), "Summary", summary); err != nil {
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
