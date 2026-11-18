package describe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTableSchemaCommand creates the describe table-schema command.
func NewTableSchemaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "table-schema [table]",
		// The API calls a table's schema a "migration"
		// (GET .../datasets/{id}/migration-details), so both spellings resolve.
		Aliases: []string{"migration", "table-migration"},
		Short:   "Show a table's schema state and pending changes",
		Long: `Show a table's schema state: applied and draft (pending) field definitions,
per-field diffs, and confirmation status.

The table may be given by ID or name. Default output is a header block
followed by the field grid. The text header also counts pending rule suggestions
and how many block the draft migration. Suggestions you cannot read are shown
as unavailable. Review their parameters with:
  qctl get rule-suggestions <table> --show-params

While the table's key columns can still be chosen on its migration, the
header adds "Key step:" and "Key columns:" and a key candidates table
follows it: each suggested key set's DUPLICATE, IDENTICAL and BLANK row
counts, and PROBLEM = DUPLICATE - IDENTICAL + BLANK. Counted over a sample,
every count is a lower bound for the file.

After a period recreate, the text view shows the previous key columns,
whether they can be explicitly reused, and their row counts when available.
Carried rule suggestions show their IDs, names, fit and missing columns.

Verbosity levels:
  (default)  Header + field grid
  -v         Adds per-field diffs, current vs new definitions, incompatible
             overrides, and technical IDs
  -vv        Raw API response dump

-o json|yaml outputs the full structured schema state (notable values are
stripped by default; add -v to include them). Other output formats fall back
to the default text view; use 'qctl get table-schema -o name' for bare
column names.

The table may be given as a positional argument (ID or name), or via
--table <name> / --table-id <id>. --table is always a name; the positional
argument resolves as an ID first, which is why a table literally named "42"
needs the flag.

For a walkthrough, see: qctl guide inspect-schema

Examples:
  qctl describe table-schema orders
  qctl describe table-schema 42 -v
  qctl describe table-schema orders -v -o yaml
  qctl describe table-schema --table orders
  qctl describe table-schema --table-id 42`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("table-schema"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			detail, err := client.GetSchemaDetails(ctx.Credential.AccessToken, datasetID)
			if err != nil {
				var status *apierror.OperationalError
				if errors.As(err, &status) && status.StatusCode == http.StatusNotFound &&
					status.Title == "Processing" && status.Severity == "info" {
					return renderSchemaProcessing(cmd, ctx, datasetID, status.Message)
				}
				return fmt.Errorf("failed to get table schema: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")

			// -vv: raw API response dump, notable values included.
			if ctx.Verbosity >= 2 {
				payload, err := datasets.SchemaStructuredPayload(detail, true)
				if err != nil {
					return err
				}
				if outputFormat == outputFormatYAML {
					return encodeSchemaStructured(cmd, outputFormatYAML, payload)
				}
				return encodeSchemaStructured(cmd, outputFormatJSON, payload)
			}

			switch outputFormat {
			case outputFormatJSON, outputFormatYAML:
				payload, err := datasets.SchemaStructuredPayload(detail, ctx.Verbosity >= 1)
				if err != nil {
					return err
				}
				return encodeSchemaStructured(cmd, outputFormat, payload)
			default:
				// Text view: header + grid (+ -v change details). This is the
				// -o table arm; the command declares table,json,yaml, so root
				// rejects anything else before RunE runs -- name included,
				// which is why there is no name view to fall back to here.
				return renderTableSchemaText(cmd, ctx, datasetID, detail)
			}
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

func renderSchemaProcessing(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int, message string) error {
	format, _ := cmd.Flags().GetString("output")
	if format == outputFormatJSON || format == outputFormatYAML || ctx.Verbosity >= 2 {
		if format != outputFormatYAML {
			format = outputFormatJSON
		}
		return encodeSchemaStructured(cmd, format, struct {
			DatasetID int    `json:"dataset_id" yaml:"dataset_id"`
			State     string `json:"state" yaml:"state"`
			Message   string `json:"message" yaml:"message"`
		}{DatasetID: tableID, State: "processing", Message: message})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Table:        %d\nStatus:       Processing\n\n%s\n\nNext: qctl get profiling-jobs --table-id %d\n",
		tableID, terminal.Sanitize(message), tableID)
	return nil
}

func renderTableSchemaText(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int, detail *datasets.TableSchemaDetail) error {
	var suggestions datasets.SuggestionsSection
	client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err == nil {
		suggestions, err = fetchSummarySuggestions(client, ctx.Credential.AccessToken, tableID, detail)
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: suggestions unavailable: %s\n", terminal.Sanitize(err.Error()))
		suggestions.Unavailable = strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	fmt.Fprint(cmd.OutOrStdout(), datasets.FormatSchemaHeader(detail))
	fmt.Fprintf(cmd.OutOrStdout(), "%-13s %s\n\n", "Suggestions:", terminal.Sanitize(datasets.FormatSuggestionsSection(suggestions)))
	if keys := datasets.FormatSchemaKeyCandidates(detail); keys != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", keys)
	}
	if carried := datasets.FormatRecreatedPeriodRules(detail); carried != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", carried)
	}
	_ = cmd.Flags().Set("output", outputFormatTable)
	if err := datasets.RenderSchemaGrid(cmd, detail, ctx.Verbosity); err != nil {
		return err
	}
	if ctx.Verbosity >= 1 {
		fmt.Fprint(cmd.OutOrStdout(), datasets.FormatSchemaChanges(detail))
	}
	return nil
}

// encodeSchemaStructured writes the payload as indented JSON or YAML.
func encodeSchemaStructured(cmd *cobra.Command, format string, payload interface{}) error {
	if format == outputFormatJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}
	encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(payload)
}
