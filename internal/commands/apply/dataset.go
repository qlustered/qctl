package apply

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDatasetCommand creates the apply dataset/table command
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "table",
		Aliases: []string{"dataset"},
		Short:   "Apply a table (dataset) configuration from a file",
		Long: `Apply a table (dataset) configuration from a YAML file.

A table (also called a dataset) holds a single managed dataset. To join
multiple existing tables into one derived output, see 'qctl apply linked-table'
(composite dataset).

This command creates a new table if it doesn't exist, or updates an existing one
when the name matches. The operation is idempotent - running it multiple times
with the same file produces the same result.

SCHEMA
======
For the complete schema with all field types, descriptions, and constraints, run:

  qctl explain table

Basic structure:

  apiVersion: qluster.ai/v1          # Required. Must be "qluster.ai/v1"
  kind: Table                        # Required. Must be "Table"
  metadata:
    name: <string>                   # Required. Table display name
  spec:
    destination_id: <int>            # Required. Destination to write into
    database_name: <string>          # Required. Destination database name
    schema_name: <string>            # Required for full create; quick create uses server default
    table_name: <string>             # Required. Destination table name
    migration_policy: <string>       # Required for full create; quick create uses server default
    data_loading_process: <string>   # Required for full create; quick create defaults to append
    backup_settings_id: <int>        # Required. Backup configuration ID
    # ... additional optional fields available via 'qctl explain table'

EXAMPLE
=======
# File: table-orders.yaml
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  table_name: orders
  backup_settings_id: 1

# Apply the configuration:
qctl apply table -f table-orders.yaml

# View full schema documentation:
qctl explain table

# View specific field details:
qctl explain table.migration_policy`,
		Annotations: cmdutil.WithFormats(catalog.For("table"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)")
			}
			return applyStream(cmd, filePath, onlyKind(applyTable))
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// applyTable is the shared apply logic for table manifests, used by both the
// "apply table" subcommand and the generic "apply -f" dispatcher.
func applyTable(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	tableManifest, err := datasets.ParseTableManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}

	if errs := tableManifest.Validate(); len(errs) > 0 {
		var messages []string
		for _, e := range errs {
			messages = append(messages, e.Error())
		}
		return nil, qerrors.NewBadUsagef("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	plan, err := client.PrepareApply(ctx.Credential.AccessToken, tableManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare dataset apply: %w", err)
	}

	if !plan.IsCreate() {
		changes := plan.Changes()
		if len(changes) == 0 {
			return printTableApplyResult(cmd, &datasets.ApplyResult{
				Name:    tableManifest.Metadata.Name,
				ID:      plan.Existing().ID,
				Status:  "unchanged",
				Action:  "unchanged",
				Message: "table already matches the manifest",
			}), nil
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if err := refuseUnconfirmedStructuredOutput(cmd, doc, yes); err != nil {
			return nil, err
		}
		if !yes {
			if err := confirmTableChanges(humanOut(cmd), cmd.ErrOrStderr(), ctx, plan.Existing(), doc, changes); err != nil {
				return nil, err
			}
		}
	}

	result, err := client.ApplyPrepared(ctx.Credential.AccessToken, plan)
	if err != nil {
		return nil, fmt.Errorf("failed to apply dataset: %w", err)
	}
	return printTableApplyResult(cmd, result), nil
}

// printTableApplyResult prints the `table/<name> <action>` line and returns
// its result and reference.
func printTableApplyResult(cmd *cobra.Command, result *datasets.ApplyResult) *applyResult {
	fmt.Fprintf(humanOut(cmd), "table/%s %s\n",
		terminal.Sanitize(result.Name), terminal.Sanitize(result.Action))
	return namedApplyResult(result, "table", result.ID)
}

// refuseUnconfirmedStructuredOutput refuses, with exit 2, an update that
// would prompt under -o json or yaml without --yes.
func refuseUnconfirmedStructuredOutput(cmd *cobra.Command, doc manifestDoc, yes bool) error {
	if yes || !structuredOutput(cmd) {
		return nil
	}
	format, _ := cmd.Flags().GetString("output")
	return qerrors.NewBadUsagef("confirmation required for %s with %s output: re-run with --yes (-y) to keep stdout machine-readable",
		doc.Source, strings.ToUpper(format))
}

// confirmTableChanges prints the live identity and manifest-scoped change list
// before gating the prepared PATCH. A decline is a skipped apply document, not
// a successful update and not a failed request.
func confirmTableChanges(out, promptOut io.Writer, ctx *cmdutil.CommandContext, existing *datasets.DatasetFull,
	doc manifestDoc, changes []datasets.TableFieldChange) error {
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = unknownOrganizationName
	}
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), ctx.OrganizationID)
	fmt.Fprintf(out, "Table        : %s (id: %d)\n", terminal.Sanitize(existing.Name), existing.ID)
	fmt.Fprintf(out, "File         : %s\n\n", doc.Source)
	fmt.Fprintln(out, "Changes to apply:")
	fmt.Fprint(out, datasets.FormatTableFieldChanges(changes, "  "))
	fmt.Fprintln(out)

	if !cmdutil.CanPrompt() {
		return qerrors.NewBadUsagef("confirmation required for %s: re-run with --yes (-y) to apply without prompting", doc.Source)
	}
	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Apply these table changes?")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintf(out, "Skipping %s\n\n", doc.Source)
		return errDocumentDeclined
	}
	fmt.Fprintf(out, "Updating table %s...\n", terminal.Sanitize(existing.Name))
	return nil
}
