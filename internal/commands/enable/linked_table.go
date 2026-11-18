package enable

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewLinkedTableCommand creates the 'enable linked-table' command.
func NewLinkedTableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "linked-table [table]",
		Short: "Enable a spec revision of a linked table",
		Long: `Enable a spec revision of a linked table: the revision becomes the one the
table is built from, the revision enabled before it is disabled, and a rebuild
of the table is queued.

The table is the linked table, as a positional id-or-name, --table <name> or
--table-id <id>. The revision is the table's latest spec revision -- the
newest one, whatever its state -- unless --revision names another revision of
the same linked table. Naming an older, disabled revision rolls the table back
to it.

A revision that is already enabled is left as it is: nothing is sent, and the
command says so and exits 0. To rebuild the table from it, use
'qctl run composite-run-job'.

An enable gate that refuses the revision answers 400, and the command exits 2
with the backend's reason. The usual one is the dry-run gate: the revision's
latest composite dry run must exist, have finished, have run against the
revision's current spec, have sampled at least one spine row and have found no
activation blockers. Launch one with 'qctl create composite-dry-run-job' and
wait for it to finish.

Output: linked-table/<table> enabled (or already enabled), the revision, and a
Next: line after an enable. -o json and -o yaml print the server's answer --
for a revision that was already enabled, the same fields for it -- and -o name
prints linked-table/<table-id> alone.

Examples:
  qctl enable linked-table orders_enriched
  qctl enable linked-table --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000`,
		Annotations: cmdutil.WithFormats(catalog.For("linked-table"), "table", "json", "yaml", "name"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runEnableLinkedTable,
	}

	cmdutil.AddTableFlags(cmd, "Linked table")
	cmd.Flags().String("revision", "", "Spec revision UUID to enable (default: the table's latest revision)")

	return cmd
}

func runEnableLinkedTable(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	var revisionID *uuid.UUID
	if cmd.Flags().Changed("revision") {
		raw, _ := cmd.Flags().GetString("revision")
		id, err := cmdutil.ParseUUIDArg(raw, "--revision")
		if err != nil {
			return err
		}
		revisionID = &id
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}

	client, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	token := ctx.Credential.AccessToken
	revision, err := client.ResolveRevision(token, tableID, revisionID)
	if err != nil {
		return err
	}
	table := cmdutil.TableLabel(cmd, positional, tableID)

	// The backend refuses enabled -> enabled with a 400 (_assert_transition in
	// atlas cettings/cettings/models/composite_spec_controller.py enable), so
	// an enabled revision is reported without a request.
	if revision.State == "enabled" {
		body, err := json.Marshal(api.CompositeSpecRevisionEnableResponse{
			CompositeSpecRevisionID: revision.ID,
			CompositeSpecGroupID:    revision.GroupID,
			State:                   revision.State,
		})
		if err != nil {
			return fmt.Errorf("failed to encode the revision: %w", err)
		}
		return writeLinkedTableEnabled(cmd, tableID, table, revision, body, false)
	}

	result, body, err := client.EnableRevision(token, revision.ID)
	if err != nil {
		return err
	}
	revision.State = result.State
	return writeLinkedTableEnabled(cmd, tableID, table, revision, body, true)
}

// writeLinkedTableEnabled prints the outcome: body for -o json|yaml,
// linked-table/<table-id> for -o name, and otherwise an outcome line, the
// revision and, when enabled is true, a Next: line.
func writeLinkedTableEnabled(cmd *cobra.Command, tableID int, table string, revision *linked_tables.Revision, body []byte, enabled bool) error {
	out := cmd.OutOrStdout()
	table = terminal.Sanitize(table)
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json":
		return output.WriteIndentedJSON(out, body)
	case "yaml":
		return output.WriteJSONAsYAML(out, body)
	case "name":
		_, err := fmt.Fprintf(out, "linked-table/%d\n", tableID)
		return err
	}

	if !enabled {
		fmt.Fprintf(out, "linked-table/%s already enabled\n", table)
		_, err := fmt.Fprintf(out, "  revision : %s (%s)\n", terminal.Sanitize(revision.Name()), terminal.Sanitize(revision.State))
		return err
	}
	fmt.Fprintf(out, "linked-table/%s enabled\n", table)
	fmt.Fprintf(out, "  revision : %s (%s)\n", terminal.Sanitize(revision.Name()), terminal.Sanitize(revision.State))
	_, err := fmt.Fprintf(out, "Next: qctl get composite-run-jobs --group-id %s\n", revision.GroupID)
	return err
}
