package deliverable

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newAcceptColumnSuggestionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accept-column-suggestions <ref> --sheet <sheet>",
		Short: "Save confident template-column matches on a draft sheet",
		Long: `Match template header labels to the bound table's columns on one draft sheet.
The positional reference names the deliverable by slug, name or UUID.
--sheet requires an exact bound sheet name. With no revision selector, the
only draft is selected; --release (exact label or full UUID) or --revision
(full UUID) selects another revision, which must also be a draft.

The server saves same_name and standard_field matches with origin suggested,
preserving origin user mappings. Similar names remain hints on empty columns.
When labels or the bound table change, stale suggested mappings are removed
before new matches are saved. Repeating the command with unchanged labels and
table leaves mappings unchanged, including suggested mappings you removed.

The report shows created and replaced counts, all current mappings, and the
returned empty columns and their hints. Replaced counts removed suggested
mappings, including ones without a new match. JSON/YAML print the original
server response on stdout and put the report on stderr.

Examples:
  qctl deliverable accept-column-suggestions monthly-bordereau --sheet Premium
  qctl deliverable accept-column-suggestions monthly-bordereau --release 2026.2 --sheet Premium
  qctl deliverable accept-column-suggestions monthly-bordereau --sheet Premium -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        runAcceptColumnSuggestions,
	}
	addDraftSheetFlags(cmd)
	return cmd
}

func runAcceptColumnSuggestions(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateDraftSheetFlags(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	detail, sheet, err := draftSheet(cmd, c, token, args[0], "column suggestions can only be accepted on a draft revision")
	if err != nil {
		return err
	}
	return acceptSheetSuggestions(cmd, c, token, detail, sheet)
}

func acceptSheetSuggestions(cmd *cobra.Command, c *deliverables.Client, token string, detail *deliverables.RevisionDetail, sheet api.SheetBindingResponse) error {
	result, err := c.AcceptColumnSuggestions(token, sheet.ID)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if _, err := fmt.Fprintf(w.Human, "deliverable/%s release %q sheet %q\n%s", detail.DeliverableID,
		terminal.Sanitize(detail.Release), terminal.Sanitize(sheet.SheetName), deliverables.FormatColumnSuggestions(result)); err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return nil
}
