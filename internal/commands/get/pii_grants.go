package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const piiGrantColumns = "id,state,grantee,granted_by,created,expires,reason"

// piiGrantRow is the table-display projection of one grant.
type piiGrantRow struct {
	ID           int    `json:"id"`
	State        string `json:"state"`
	Grantee      string `json:"grantee"`
	GrantedBy    string `json:"granted_by"`
	Created      string `json:"created"`
	Expires      string `json:"expires"`
	Reason       string `json:"reason"`
	RevokedBy    string `json:"revoked_by"`
	Revoked      string `json:"revoked"`
	RevokeReason string `json:"revoke_reason"`
	RevokeNote   string `json:"revoke_note"`
}

// NewPiiGrantsCommand creates the get pii-grants command.
func NewPiiGrantsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pii-grants [table]",
		Short: "List a table's grants of temporary unmasked access to personal data",
		Long: `List a table's grants of unmasked access to its personal data, active,
expired and revoked, newest first.

The table is a positional ID or name, or --table/--table-id. STATE is active,
expired or revoked, as the server computes it when it answers. The route needs
the pii:grant permission (admin or ops); anyone else gets a 403 (exit 4).

--page and --limit select one page (default: page 1, limit 50; limit at most
100). Available columns: id, state, grantee, granted_by, created, expires,
reason, revoked_by, revoked, revoke_reason, revoke_note. -o json and -o yaml
print a List of the grants as the server sent them; -o name prints
pii-grant/<id> for each grant.

Examples:
  qctl get pii-grants orders
  qctl get pii-grants --table-id 42 --columns id,state,grantee,revoke_reason,revoke_note
  qctl get pii-grants orders -o json`,
		Annotations: cmdutil.WithArgKinds(catalog.For("pii-grant"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runPiiGrants,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 50, "Grants per page (max 100)")
	cmdutil.DeclareColumns(cmd, []piiGrantRow(nil))
	return cmd
}

func runPiiGrants(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return err
	}
	if limit > 100 {
		return qerrors.NewBadUsage("--limit must be at most 100")
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	list, err := pii_grants.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		List(ctx.Credential.AccessToken, tableID, page, limit)
	if err != nil {
		return fmt.Errorf("failed to list PII grants: %w", err)
	}

	lp := listPage{noun: "PII grants", page: &page, limit: &limit, total: &list.Total, noFilters: true}
	if len(list.Grants) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "PII grants", []piiGrantRow{}, lp.total)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, list.Grants, lp, func(data any) error {
		if l, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		return tableui.PrintFromCmd(cmd, piiGrantRows(list.Grants), piiGrantColumns)
	})
}

func piiGrantRows(grants []pii_grants.Grant) []piiGrantRow {
	rows := make([]piiGrantRow, 0, len(grants))
	for _, g := range grants {
		row := piiGrantRow{
			ID:        g.ID,
			State:     string(g.State),
			Grantee:   g.GranteeEmail,
			GrantedBy: g.GrantedByEmail,
			Created:   timeutil.FormatRelative(g.CreatedAt),
			Expires:   timeutil.FormatRelative(g.ExpiresAt),
			Reason:    strings.TrimSpace(g.Reason),
			Revoked:   timeutil.FormatRelativePtr(g.RevokedAt),
		}
		if g.RevokedByEmail != nil {
			row.RevokedBy = *g.RevokedByEmail
		}
		if g.RevokeReason != nil {
			row.RevokeReason = string(*g.RevokeReason)
		}
		if g.RevokeNote != nil {
			row.RevokeNote = *g.RevokeNote
		}
		rows = append(rows, row)
	}
	return rows
}
