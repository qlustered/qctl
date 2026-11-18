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

func newConfirmCellsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "confirm-cells <ref> --sheet <sheet> --cell <A1>",
		Short: "Confirm flagged cell bindings on a draft revision",
		Long: `Save the named cell bindings of a draft revision again so the server clears
their needs-confirmation flag. The positional reference names the deliverable
by slug, name or UUID. --sheet requires an exact sheet name; it need not have
a sheet binding. --cell takes an A1 reference (B2); repeat it or separate
references with commas. Letters are case-insensitive and absolute markers
and leading row zeros are normalized. Every named cell must be bound.

With no revision selector, the only draft is selected; --release (exact label
or full UUID) or --revision (full UUID) selects another revision, which must
also be a draft. Each named flagged binding is saved with its stored source,
row filter, format and label-cell reference. The server re-reads its label
from the template, or chooses a nearby label when none was stored. A named
binding without the flag is reported as already confirmed and is not saved.

JSON/YAML print the named bindings on stdout, as returned by each save or held
by the revision, and put the report on stderr. Saves stop at the first failure;
the error names any cells confirmed before it.

Examples:
  qctl deliverable confirm-cells monthly-bordereau --sheet Summary --cell B2,D2
  qctl deliverable confirm-cells monthly-bordereau --release 2026.2 --sheet Summary --cell B2
  qctl deliverable confirm-cells monthly-bordereau --sheet Summary --cell B2 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        runConfirmCells,
	}
	addDraftSheetFlags(cmd)
	cmd.Flags().Lookup("sheet").Usage = "Exact sheet name containing the cell bindings (required)"
	cmd.Flags().StringSlice("cell", nil, "Cell to confirm, by A1 reference (repeatable or comma-separated; required)")
	return cmd
}

func runConfirmCells(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateDraftSheetFlags(cmd); err != nil {
		return err
	}
	keys, _ := cmd.Flags().GetStringSlice("cell")
	refs, err := deliverables.ParseCellList(keys)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	detail, err := draftRevision(cmd, c, token, args[0], "cell bindings can only be confirmed on a draft revision")
	if err != nil {
		return err
	}
	sheet, _ := cmd.Flags().GetString("sheet")
	result, err := c.ConfirmCells(token, detail.DeliverableRevisionResponse, sheet, refs)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if _, err := fmt.Fprintf(w.Human, "deliverable/%s release %q sheet %q\n%s", detail.DeliverableID,
		terminal.Sanitize(detail.Release), terminal.Sanitize(sheet), deliverables.FormatConfirmCells(result)); err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Bindings())
	}
	return nil
}
