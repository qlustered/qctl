package describe

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// NewWarningCommand creates the describe warning command
func NewWarningCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "warning <id>",
		Short: "Show details of a specific warning",
		Long: `Show details of a specific warning.

The output is the warning object as the API returns it, plus
affecting_rows_count — not a kind: Warning manifest, so there is no
apiVersion, metadata, spec or status. Markdown in msg is rendered as plain
text, and keys are sorted. The default is YAML, followed by YAML comments for
the rule's declared severity (when it has one), who flagged the warning's rows
(when anyone did), and the runnable next actions; -o table prints the warning
as a table (pick fields with --columns) followed by those notes as plain
text, and -o json prints the object alone.

The flags are user_submitted_issues ('qctl warning submit'), retracted and
detached ones included. The FLAGGED BY note lists one line per flagger and
thread, live flags first, and a Next: line reads a thread, preferring a live
flag's:

  FLAGGED BY
    Dana Ray <dana@example.com>: rows 1042, 1043 (amount) — comment-thread/<uuid>
  Next: qctl describe comment-thread <uuid> --table-id <id>

affecting_rows_count comes from a second request to the warning's stats
endpoint. If that request fails, the value is left out and a warning is printed
on stderr.

Example output (abridged):

  affecting_rows_count: 3
  ...
  dataset_id: 10
  dataset_name: user_data
  ...
  id: 55
  issue_type: SCHEMA_DRIFT
  ...
  msg: Schema drift detected
  ...
  warning_actions_list: []`,
		Annotations: cmdutil.WithFormats(catalog.For("warning"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse warning ID from arg
			warningID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid warning ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create warnings client
			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// Get warning
			result, err := client.GetWarning(ctx.Credential.AccessToken, warningID)
			if err != nil {
				return fmt.Errorf("failed to get warning: %w", err)
			}
			var affectingRowsCount *int
			if stats, err := client.GetWarningStats(ctx.Credential.AccessToken, warningID); err != nil {
				warnStatsUnavailable(cmd, err)
			} else {
				affectingRowsCount = stats.AffectingRowsCount
			}
			doc := withAffectingRowsCount(result, affectingRowsCount)

			// Get output format - default to YAML for describe commands
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			// Fields that contain markdown
			markdownFields := []string{"message", "msg"}

			// For table format, use the printer with markdown rendering
			if outputFormat == outputFormatTable {
				p, err := printer.NewPrinterFromCmdWithMarkdown(cmd, markdownFields)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				if err := p.Print(doc); err != nil {
					return err
				}
				// The plain form: a table is not a YAML document, so the `# `
				// the default view needs would be noise here. The footer's
				// facts are available in no other part of the output -- which
				// actions need input and what fields they want, which fan out
				// across data sources, which one is BULK -- and a context with
				// `output: table` reaches this branch through
				// cmdutil.ApplyContextOutputFormat without the reader ever
				// typing a flag, which is how the silence went unnoticed.
				fmt.Fprint(cmd.OutOrStdout(), warningFooterPlain(result))
				return nil
			}

			// Process markdown in message fields for JSON/YAML output (plain text)
			processedResult := markdown.ProcessFieldsPlain(doc, markdownFields)

			// For JSON output
			if outputFormat == outputFormatJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedResult)
			}

			// YAML output (default), with the runnable commands for
			// warning_actions_list appended as YAML comments so the document
			// stays parseable. `-o json` is the only view without a footer: it
			// has no comment syntax, and the raw response is the machine view —
			// the same split describe alert makes.
			//
			// The footer is rendered off `result`, not `processedResult`: it
			// needs the typed warning.
			return writeYAMLWithFooter(cmd, processedResult, warningFooter(result))
		},
	}

	return cmd
}

// withAffectingRowsCount returns a struct holding w's fields followed by
// AffectingRowsCount, set to count and tagged
// `json:"affecting_rows_count,omitempty"`.
func withAffectingRowsCount(w *warnings.WarningFull, count *int) any {
	src := reflect.ValueOf(w).Elem()
	fields := make([]reflect.StructField, 0, src.NumField()+1)
	for i := 0; i < src.NumField(); i++ {
		fields = append(fields, src.Type().Field(i))
	}
	fields = append(fields, reflect.StructField{
		Name: "AffectingRowsCount",
		Type: reflect.TypeOf(count),
		Tag:  `json:"affecting_rows_count,omitempty"`,
	})
	out := reflect.New(reflect.StructOf(fields)).Elem()
	for i := 0; i < src.NumField(); i++ {
		out.Field(i).Set(src.Field(i))
	}
	out.Field(src.NumField()).Set(reflect.ValueOf(count))
	return out.Interface()
}

// warningFooterPlain is everything `describe warning` prints after the
// document: the DECLARED SEVERITY note first, then FLAGGED BY, then HELD BY
// CHANGE PROPOSAL, then NEXT ACTIONS. Mirrors alertFooterPlain, and for the same reason --
// `WarningSchema.body` is the alert body type, so a warning carries
// `declared_severity` too.
//
// The note matters MORE here than on the alert side. This command renders the
// raw response, so `body.declared_severity` already reaches the reader today --
// as one line among two dozen null siblings, with nothing saying it is the
// rule's value rather than the warning's. Nothing about the document changes;
// the note is what makes the line legible.
func warningFooterPlain(w *warnings.WarningFull) string {
	return warnings.RenderDeclaredSeverityPlain(w) + warnings.RenderFlaggedByPlain(w) +
		alerts.RenderHeldByPlain(w.ChangeProposal, "warning") +
		warnings.RenderNextActionsPlain(w, cmdutil.CanPrompt())
}

// warningFooter is warningFooterPlain as YAML comments, for the default
// document view. Like alertFooter it joins the package's own comment wrappers;
// YAMLComment is line-wise, so that is byte-identical to commenting the joined
// plain text.
func warningFooter(w *warnings.WarningFull) string {
	return warnings.RenderDeclaredSeverity(w) + warnings.RenderFlaggedBy(w) +
		alerts.RenderHeldBy(w.ChangeProposal, "warning") +
		warnings.RenderNextActions(w, cmdutil.CanPrompt())
}
