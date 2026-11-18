// Package deliverable implements deliverable authoring actions and completeness reads.
package deliverable

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCommand creates the deliverable parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "deliverable",
		Short:       "Act on a deliverable's setup or report missed rows",
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newAcceptColumnSuggestionsCommand())
	cmd.AddCommand(newConfirmColumnsCommand())
	cmd.AddCommand(newConfirmCellsCommand())
	cmd.AddCommand(newCompletenessCommand())
	cmd.AddCommand(newTestFilterCommand())
	return cmd
}

// addDraftSheetFlags registers --release, --revision and --sheet.
func addDraftSheetFlags(cmd *cobra.Command) {
	cmd.Flags().String("release", "", "Draft revision release label or full UUID (default: the only draft)")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Draft revision full UUID")
	cmd.Flags().String("sheet", "", "Exact bound sheet name (required)")
}

// validateDraftSheetFlags is bad usage for an empty --sheet, an empty
// --release or --revision, and what deliverables.ValidateRevisionSelector
// refuses.
func validateDraftSheetFlags(cmd *cobra.Command) error {
	sheet, _ := cmd.Flags().GetString("sheet")
	if sheet == "" {
		return qerrors.NewBadUsage("--sheet is required and cannot be empty")
	}
	for _, flag := range []string{"release", "revision"} {
		value, _ := cmd.Flags().GetString(flag)
		if cmd.Flags().Changed(flag) && value == "" {
			return qerrors.NewBadUsagef("--%s cannot be empty", flag)
		}
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	return deliverables.ValidateRevisionSelector(release, revision)
}

// draftSheet resolves the deliverable ref names and the revision --release or
// --revision selects (default: its only draft), and returns that revision's
// tree and its sheet binding named --sheet. A revision of another deliverable,
// or a sheet the revision does not bind, is not found; a revision that is not
// a draft is bad usage ending in refusal.
func draftSheet(cmd *cobra.Command, c *deliverables.Client, token, ref, refusal string) (*deliverables.RevisionDetail, api.SheetBindingResponse, error) {
	detail, err := draftRevision(cmd, c, token, ref, refusal)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	sheetName, _ := cmd.Flags().GetString("sheet")
	sheet, err := boundSheet(detail, sheetName)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	return detail, sheet, nil
}

// draftRevision resolves ref and the selected draft's current binding tree.
func draftRevision(cmd *cobra.Command, c *deliverables.Client, token, ref, refusal string) (*deliverables.RevisionDetail, error) {
	id, err := c.ResolveID(token, ref, true)
	if err != nil {
		return nil, err
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	target, err := c.ResolveDraftTarget(token, id, release, revision)
	if err != nil {
		return nil, err
	}
	detail, err := c.GetRevision(token, target.ID)
	if err != nil {
		return nil, err
	}
	if detail.DeliverableID != id {
		return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("revision %s does not belong to deliverable %s", target.ID, id)))
	}
	if detail.State != api.DeliverableRevisionStateDraft {
		return nil, qerrors.NewBadUsagef("release %q is %s; %s",
			terminal.Sanitize(detail.Release), terminal.Sanitize(string(detail.State)), refusal)
	}
	return detail, nil
}

// boundSheet returns detail's sheet binding named exactly name. A sheet the
// revision does not bind is not found.
func boundSheet(detail *deliverables.RevisionDetail, name string) (api.SheetBindingResponse, error) {
	if detail.SheetBindings != nil {
		for _, sheet := range *detail.SheetBindings {
			if sheet.SheetName == name {
				return sheet, nil
			}
		}
	}
	return api.SheetBindingResponse{}, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
		"no sheet binding matches %q on release %q; use 'qctl describe deliverable-revision %s --revision %s'",
		terminal.Sanitize(name), terminal.Sanitize(detail.Release), detail.DeliverableID, detail.ID,
	)))
}
