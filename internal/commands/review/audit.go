package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/spf13/cobra"
)

// auditRow is the table-display projection of one audit entry.
type auditRow struct {
	When    string `json:"when"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Details string `json:"details"`
}

func newAuditCommand() *cobra.Command {
	var page, limit int

	cmd := &cobra.Command{
		Use:   "audit <review-id>",
		Short: "Show the audit log of a rule review",
		Long: `Show the paginated audit trail of a rule review (creation, decisions,
reviewer changes, reopenings, policy-gate events, ...).

Actions are rendered verbatim from the backend. Use -o json|yaml for the
raw response.`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmdutil.ValidateUUIDArgs(args, "review id")
			if err != nil {
				return err
			}
			// Refused, not forwarded: the backend requires page/limit >= 1
			// (../atlas/capi/capi/views/rule_review_view.py's
			// `page: int = FastApiQuery(default=1, ge=1)`) and would 422 on a
			// non-positive value. It is the CLIENT that drops the parameter
			// (rule_reviews/client.go's `if page > 0` / `if limit > 0`), so a
			// typo'd --page used to look like a successful read of a page the
			// user never asked for — the backend's own default, reached only
			// because qctl never sent the value at all.
			if page, err = cmdutil.PositiveCountFlag(cmd, "page"); err != nil {
				return err
			}
			if limit, err = cmdutil.PositiveCountFlag(cmd, "limit"); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			auditPage, err := client.AuditLog(ctx.Credential.AccessToken, args[0], page, limit)
			if err != nil {
				return fmt.Errorf("failed to get audit log: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") && (outputFormat == "json" || outputFormat == "yaml") {
				return output.WriteRaw(cmd.OutOrStdout(), outputFormat, auditPage)
			}

			rows := make([]auditRow, 0, len(auditPage.AuditEntries))
			for _, e := range auditPage.AuditEntries {
				rows = append(rows, auditRow{
					When:    timeutil.FormatRelative(e.CreatedAt),
					Actor:   rule_reviews.UserDisplay(e.Actor, e.ActorID),
					Action:  e.Action, // open-ended string — verbatim
					Details: formatAuditDetails(e.Details),
				})
			}
			if err := tableui.PrintFromCmd(cmd, rows, "when,actor,action,details"); err != nil {
				return err
			}
			if auditPage.Total > auditPage.Page*auditPage.Limit {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"More audit entries — fetch the next page with --page %d.\n", auditPage.Page+1)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "Page number")
	cmd.Flags().IntVar(&limit, "limit", 20, "Entries per page (max 100)")
	cmdutil.DeclareColumns(cmd, []auditRow(nil))
	return cmd
}

// formatAuditDetails renders the free-form details map as sorted "k=v" pairs,
// each value passed through jsonnum.Whole.
func formatAuditDetails(details *map[string]interface{}) string {
	if details == nil || len(*details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(*details))
	for k := range *details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%v", k, jsonnum.Whole((*details)[k])))
	}
	return strings.Join(pairs, " ")
}
