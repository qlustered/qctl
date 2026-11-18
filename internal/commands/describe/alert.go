package describe

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewAlertCommand creates the describe alert command
func NewAlertCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alert <id>",
		Short: "Show details of a specific alert",
		Long: `Show details of a specific alert including its configuration and affected items.

By default, only essential fields are shown. Use -v for full details including
body highlights (suggested values, anomaly scores, etc.), or -vv for a raw dump
of the complete API response.

For cell_override_conflict alerts, the default view includes the fix in
spec.field_value and the upstream value and baseline in spec.body. Numeric
values use the applied column's display units; -vv keeps stored units.

Verbosity levels:
  (default)  Essential fields only — omits debugging/niche fields
  -v         Full alert details + body highlights (suggested values, anomaly scores, etc.)
  -vv        Raw API response dump (all fields, useful for debugging)

For a row-level alert, status.affecting_rows_count (raw_stats under -vv) comes
from a second request to the alert's stats endpoint. If that request fails, the
field is left out and a warning is printed on stderr.

The flags people raised on the alert's rows ('qctl alert submit') are in
spec.user_submitted_issues (raw_response.user_submitted_issues under -vv),
retracted and detached ones included. A FLAGGED BY section after the document
summarizes them, one line per flagger and thread, live flags first, and a
Next: line reads a thread, preferring a live flag's:

  FLAGGED BY
    Dana Ray <dana@example.com>: rows 1042, 1043 (amount) — comment-thread/<uuid>
  Next: qctl describe comment-thread <uuid> --table-id <id>

The section is YAML comments in the default view, plain text with -o table,
and absent from -o json. The default and -v table views omit the list from
the SPEC cell; -vv keeps it in the raw response.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: Alert
  metadata:
    id: 1
  spec:
    issue_type: MISSING_COLUMN
    message: "Column 'email' is missing"
    dataset_id: 10
    dataset_name: user_data
    field_name: email
    blocks_ingestion_for_dataset: true
    actions:
      - ignore_issue_for_value
  status:
    resolved: false
    count: 3
    created_at: "2025-01-15T10:00:00Z"`,
		Annotations: cmdutil.WithFormats(catalog.For("alert"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse alert ID from arg
			alertID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid alert ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create alerts client
			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// Get alert
			result, err := client.GetAlert(ctx.Credential.AccessToken, alertID)
			if err != nil {
				return fmt.Errorf("failed to get alert: %w", err)
			}
			stats := fetchAlertStats(cmd, client, ctx.Credential.AccessToken, result)

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}
			markdownFields := []string{"message"}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				rawManifest, err := alerts.APIResponseToRawManifest(result, stats)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}

				if outputFormat == outputFormatJSON {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(rawManifest)
				}

				if outputFormat == outputFormatTable {
					return writeAlertTable(cmd, withoutNilRawStats(rawManifest), markdownFields, result, "")
				}

				// The default raw dump is YAML. -vv is strictly MORE than the
				// default view, so dropping the footer as verbosity rises is
				// backwards; the comment form keeps the document parseable.
				return writeYAMLWithFooter(cmd, rawManifest, alertFooter(result))
			}

			// Convert to manifest format with verbosity-aware field selection
			manifest := alerts.APIResponseToManifest(result, stats, ctx.Verbosity)
			formatAlertConflict(cmd, ctx, result, manifest)

			// Process markdown in message field for JSON/YAML output (plain text)
			processedManifest := markdown.ProcessFieldsPlain(manifest, markdownFields)

			// For JSON output, encode the processed manifest
			if outputFormat == outputFormatJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedManifest)
			}

			// For table format, use the printer with markdown rendering for message
			if outputFormat == outputFormatTable {
				message := ""
				if manifest.Spec != nil {
					message = manifest.Spec.Message
					manifest.Spec.Message = inlineTableMessage(manifest.Spec.Message)
					manifest.Spec.UserSubmittedIssues = nil
				}
				return writeAlertTable(cmd, manifest, markdownFields, result, message)
			}

			// YAML output (default), with the runnable commands for spec.actions
			// appended as YAML comments so the document stays parseable.
			// `-o json` is the only view without a footer: it has no comment
			// syntax, and the raw response is the machine view.
			return writeYAMLWithFooter(cmd, processedManifest, alertFooter(result))
		},
	}

	return cmd
}

// fetchAlertStats returns the alert's stats, or nil for an alert that is not
// row level, which is not asked. A failed read is also nil, after one stderr
// warning.
func fetchAlertStats(cmd *cobra.Command, client *alerts.Client, token string, alert *alerts.AlertFull) *alerts.IssueStats {
	if !alert.IsRowLevel {
		return nil
	}
	stats, err := client.GetAlertStats(token, alert.ID)
	if err != nil {
		warnStatsUnavailable(cmd, err)
		return nil
	}
	return stats
}

// warnStatsUnavailable prints the one stderr line a failed stats read gets:
// the first line of err.
func warnStatsUnavailable(cmd *cobra.Command, err error) {
	first, _, _ := strings.Cut(err.Error(), "\n")
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: affected row count unavailable: %s\n", first)
}

// withoutNilRawStats returns m, or when m.RawStats is nil, a struct holding
// m's other fields.
func withoutNilRawStats(m *alerts.AlertRawManifest) any {
	if m.RawStats != nil {
		return m
	}
	src := reflect.ValueOf(m).Elem()
	fields := make([]reflect.StructField, 0, src.NumField()-1)
	values := make([]reflect.Value, 0, src.NumField()-1)
	for i := 0; i < src.NumField(); i++ {
		if field := src.Type().Field(i); field.Name != "RawStats" {
			fields = append(fields, field)
			values = append(values, src.Field(i))
		}
	}
	out := reflect.New(reflect.StructOf(fields)).Elem()
	for i, value := range values {
		out.Field(i).Set(value)
	}
	return out.Interface()
}

// writeAlertTable renders either alert manifest tier and appends the footer in
// plain text. A table is not a YAML document, so comment markers would be noise.
func writeAlertTable(cmd *cobra.Command, manifest any, markdownFields []string, result *alerts.AlertFull, message string) error {
	p, err := printer.NewPrinterFromCmdWithMarkdown(cmd, markdownFields)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	if err := p.Print(manifest); err != nil {
		return err
	}
	if err := writeTableMessageSection(cmd.OutOrStdout(), "Message", message); err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), alertFooterPlain(result))
	return nil
}

// alertFooterPlain is everything `describe alert` prints after the document:
// the DECLARED SEVERITY note first, then FLAGGED BY, then HELD BY CHANGE
// PROPOSAL, then NEXT ACTIONS.
//
// The note leads because it explains a value the reader has already scrolled
// past, and it is NOT gated on verbosity even though `spec.body.declared_severity`
// is a -v field: the note carries the value inline, so at the default tier it is
// the only place the override is stated at all. That is the same bargain the
// row-filter line already makes -- a footer fact available in no other part of
// the output.
//
// Both halves withhold themselves independently: RenderNextActionsPlain returns
// "" for a resolved alert with nothing to run, while the note keeps printing
// there, because a resolved alert's severity was still overridden.
func alertFooterPlain(a *alerts.AlertFull) string {
	return alerts.RenderDeclaredSeverityPlain(a.Body, alerts.IssueClassAlert) +
		alerts.RenderFlaggedByPlain(a) +
		alerts.RenderHeldByPlain(a.ChangeProposal, "alert") +
		alerts.RenderNextActionsPlain(a, cmdutil.CanPrompt())
}

// alertFooter is alertFooterPlain as YAML comments, for the views that print a
// parseable document. It joins the package's own comment wrappers rather
// than commenting the joined plain text: YAMLComment prefixes line by line and
// leaves empty lines alone, so the two spellings are byte-identical, and this
// one keeps each section's plain and commented forms paired in one place.
func alertFooter(a *alerts.AlertFull) string {
	return alerts.RenderDeclaredSeverity(a.Body, alerts.IssueClassAlert) +
		alerts.RenderFlaggedBy(a) +
		alerts.RenderHeldBy(a.ChangeProposal, "alert") +
		alerts.RenderNextActions(a, cmdutil.CanPrompt())
}
