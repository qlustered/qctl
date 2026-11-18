package deliverable

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newConfirmColumnsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "confirm-columns <ref> --sheet <sheet> --column <column>",
		Short: "Confirm flagged column bindings on a draft sheet",
		Long: `Save the named column bindings of one draft sheet again, unchanged, so the
server clears their needs-confirmation flag.
The positional reference names the deliverable by slug, name or UUID.
--sheet requires an exact bound sheet name. --column names a template column
by letter (C) or 1-based index (3); repeat it or separate columns with commas.
With no revision selector, the only draft is selected; --release (exact label
or full UUID) or --revision (full UUID) selects another revision, which must
also be a draft.

Every named column must be bound on the sheet. Each named binding whose
needs-confirmation flag is set is saved with its stored source, target column
and format, one at a time in column order. Each save also sets the binding's
origin to user, so accept-column-suggestions never removes or replaces it,
and re-reads its label from the template header. A named binding without the
flag is reported as already confirmed and nothing is sent for it.

The report prints one line per named column. JSON/YAML print the named column
bindings on stdout, as each save returned it or as the revision holds it, and
put the report on stderr.

Examples:
  qctl deliverable confirm-columns monthly-bordereau --sheet Premium --column C,D
  qctl deliverable confirm-columns monthly-bordereau --release 2026.2 --sheet Premium --column AA
  qctl deliverable confirm-columns monthly-bordereau --sheet Premium --column 3 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        runConfirmColumns,
	}
	addDraftSheetFlags(cmd)
	cmd.Flags().StringSlice("column", nil, "Template column to confirm, by letter or 1-based index (repeatable or comma-separated; required)")
	return cmd
}

func runConfirmColumns(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateDraftSheetFlags(cmd); err != nil {
		return err
	}
	keys, _ := cmd.Flags().GetStringSlice("column")
	indexes, err := deliverables.ParseColumnList(keys)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	detail, sheet, err := draftSheet(cmd, c, token, args[0], "column bindings can only be confirmed on a draft revision")
	if err != nil {
		return err
	}
	result, err := c.ConfirmColumns(token, sheet, indexes)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if _, err := fmt.Fprintf(w.Human, "deliverable/%s release %q sheet %q\n%s", detail.DeliverableID,
		terminal.Sanitize(detail.Release), terminal.Sanitize(sheet.SheetName), deliverables.FormatConfirmColumns(result)); err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Bindings())
	}
	return nil
}
