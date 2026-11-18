package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_access_requests"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const (
	piiAccessRequestColumns      = "id,state,duration,created,expires,deny_reason,grant_expires"
	piiAccessRequestAdminColumns = "id,requester,state,duration,created,expires,reason,decided_by,grant"
)

type piiAccessRequestRow struct {
	ID           int    `json:"id"`
	State        string `json:"state"`
	Duration     string `json:"duration"`
	Created      string `json:"created"`
	Expires      string `json:"expires"`
	Reason       string `json:"reason"`
	Decided      string `json:"decided"`
	DenyReason   string `json:"deny_reason"`
	GrantExpires string `json:"grant_expires"`
}

type piiAccessRequestAdminRow struct {
	ID           int    `json:"id"`
	Requester    string `json:"requester"`
	Agent        string `json:"agent"`
	State        string `json:"state"`
	Duration     string `json:"duration"`
	Created      string `json:"created"`
	Expires      string `json:"expires"`
	Reason       string `json:"reason"`
	Decided      string `json:"decided"`
	DecidedBy    string `json:"decided_by"`
	DenyReason   string `json:"deny_reason"`
	Grant        string `json:"grant"`
	GrantExpires string `json:"grant_expires"`
}

// NewPiiAccessRequestsCommand creates the get pii-access-requests command.
func NewPiiAccessRequestsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pii-access-requests [table]",
		Short: "List requests for temporary unmasked access to personal data",
		Long: `List your own personal-data access requests on one table, newest first.

Use --shared for a table shared from another organization. --all lists every
requester's requests on the table instead, with REQUESTER, DECIDED_BY and the
GRANT an approval created; it needs the pii:grant permission (admin or ops)
and cannot be combined with --shared. STATE is the
server's open, approved, denied, withdrawn or expired state. An approved
request's state does not tell you whether its grant is still active; check
grant_expires_at in JSON/YAML or the grant_expires column.

--page and --limit select one page (default page 1, limit 50; maximum 100).
-o json|yaml emits a List of the API request records. -o name emits
pii-access-request/<id>, accepted by pii-access-request withdraw, approve
and deny.

Examples:
  qctl get pii-access-requests orders
  qctl get pii-access-requests --shared --table-id 42 -o json
  qctl get pii-access-requests orders --all`,
		Annotations: cmdutil.WithArgKinds(catalog.For("pii-access-request"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runPiiAccessRequests,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmdutil.AddSharedDataFlag(cmd)
	cmd.Flags().Bool("all", false, "List every requester's requests on the table, not just yours (admin/ops only)")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 50, "Requests per page (max 100)")
	cmdutil.DeclareColumns(cmd, []piiAccessRequestRow(nil), []piiAccessRequestAdminRow(nil))
	return cmd
}

func runPiiAccessRequests(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"all", "shared"}); err != nil {
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
	shared, _ := cmd.Flags().GetBool("shared")
	tableID, err := cmdutil.ResolveDataTableID(ctx, cmd, positional, shared)
	if err != nil {
		return err
	}
	client := pii_access_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity, shared)
	if all, _ := cmd.Flags().GetBool("all"); all {
		list, err := client.ListAll(ctx.Credential.AccessToken, tableID, page, limit)
		if err != nil {
			return fmt.Errorf("failed to list PII access requests: %w", err)
		}
		return printPiiAccessRequests(cmd, ctx, list.Requests, page, limit, list.Total,
			piiAccessRequestAdminRows(list.Requests), piiAccessRequestAdminColumns)
	}
	list, err := client.List(ctx.Credential.AccessToken, tableID, page, limit)
	if err != nil {
		return fmt.Errorf("failed to list PII access requests: %w", err)
	}
	return printPiiAccessRequests(cmd, ctx, list.Requests, page, limit, list.Total,
		piiAccessRequestRows(list.Requests), piiAccessRequestColumns)
}

// printPiiAccessRequests prints one page: the API records as a List for
// -o json|yaml, and rows under columns otherwise.
func printPiiAccessRequests[R, T any](cmd *cobra.Command, ctx *cmdutil.CommandContext, requests []R,
	page, limit, total int, rows []T, columns string) error {
	lp := listPage{noun: "PII access requests", page: &page, limit: &limit, total: &total, noFilters: true}
	if len(requests) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "PII access requests", []piiAccessRequestRow{}, lp.total)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, requests, lp, func(data any) error {
		if l, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		return tableui.PrintFromCmd(cmd, rows, columns)
	})
}

func piiAccessRequestRows(requests []pii_access_requests.Request) []piiAccessRequestRow {
	rows := make([]piiAccessRequestRow, 0, len(requests))
	for _, r := range requests {
		row := piiAccessRequestRow{
			ID: r.ID, State: string(r.State), Duration: string(r.RequestedDuration),
			Created: timeutil.FormatRelative(r.CreatedAt), Expires: timeutil.FormatRelative(r.ExpiresAt),
			Reason: strings.TrimSpace(r.Reason), Decided: timeutil.FormatRelativePtr(r.DecidedAt),
			GrantExpires: timeutil.FormatRelativePtr(r.GrantExpiresAt),
		}
		if r.DenyReason != nil {
			row.DenyReason = *r.DenyReason
		}
		rows = append(rows, row)
	}
	return rows
}

func piiAccessRequestAdminRows(requests []pii_access_requests.AdminRequest) []piiAccessRequestAdminRow {
	rows := make([]piiAccessRequestAdminRow, 0, len(requests))
	for _, r := range requests {
		own := piiAccessRequestRows([]pii_access_requests.Request{{
			ID: r.ID, State: r.State, RequestedDuration: r.RequestedDuration, CreatedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt, Reason: r.Reason, DecidedAt: r.DecidedAt, DenyReason: r.DenyReason,
			GrantExpiresAt: r.GrantExpiresAt,
		}})[0]
		row := piiAccessRequestAdminRow{
			ID: own.ID, Requester: r.RequesterEmail, Agent: boolYesNo(r.RequestedByAgent), State: own.State,
			Duration: own.Duration, Created: own.Created, Expires: own.Expires, Reason: own.Reason,
			Decided: own.Decided, DenyReason: own.DenyReason, GrantExpires: own.GrantExpires,
		}
		if r.DecidedByEmail != nil {
			row.DecidedBy = *r.DecidedByEmail
		}
		if r.GrantID != nil {
			row.Grant = fmt.Sprintf("pii-grant/%d", *r.GrantID)
		}
		rows = append(rows, row)
	}
	return rows
}
