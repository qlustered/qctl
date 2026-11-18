package get

import (
	"fmt"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/spf13/cobra"
)

const privilegeRequestColumns = "id,status,action,target,requestor,approver,requested,decided"

// privilegeRequestRow is the table-display projection of one request.
type privilegeRequestRow struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Action        string `json:"action"`
	Target        string `json:"target"`
	Requestor     string `json:"requestor"`
	Approver      string `json:"approver"`
	Requested     string `json:"requested"`
	Decided       string `json:"decided"`
	Justification string `json:"justification"`
}

// privilegePlatformWideHint follows the empty-result sentence, which names the
// current org: the privilege routes are not org-filtered, so an empty list
// means empty across the platform, not in that org.
func privilegePlatformWideHint() string {
	return "The privilege routes are platform-wide; the org only decides where your ops role is checked.\n"
}

// NewPrivilegeRequestsCommand creates the get privilege-requests command.
func NewPrivilegeRequestsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privilege-requests",
		Short: "List break-glass ops privilege requests (platform-wide)",
		Long: `List requests to grant (escalate) or revoke (demote) a user's ops
eligibility, is_ops, newest first.

This is an operations surface: the route requires the ops role, so an admin or
editor gets a 403 (exit 4). The listing is platform-wide -- every request on
the platform, whichever organization the current context points at; the org
only decides where your ops role is checked.

--status takes Pending, Approved, Rejected or Expired, in any case. A request
lists as Pending until someone tries to approve it: the server evaluates
expiry only then.

--target-user takes an email, a "First Last" name, or a UUID. A name resolves
against the CURRENT org's members, so from a customer org pass the UUID.

Available columns: id, status, action, target, requestor, approver, requested,
decided, justification. -o json and -o yaml emit the raw wire response; -o name
prints privilege-request/<id> for each request.

Examples:
  qctl get privilege-requests
  qctl get privilege-requests --status pending
  qctl get privilege-requests --target-user tom@example.com -o json`,
		Annotations: catalog.For("privilege-request"),
		Args:        cobra.NoArgs,
		RunE:        runPrivilegeRequests,
	}
	cmd.Flags().String("status", "",
		"Only requests with this status. Values: "+strings.Join(privilege_requests.StatusNames(), ", "))
	cmd.Flags().String("target-user", "", `Only requests targeting this user (email, "First Last", or UUID)`)
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, "Requests per page (max 100)")
	cmdutil.DeclareColumns(cmd, []privilegeRequestRow(nil))
	return cmd
}

func runPrivilegeRequests(cmd *cobra.Command, _ []string) error {
	// Everything decidable locally runs before Bootstrap, so a bad value costs
	// no credential read and no request.
	opts, err := privilegeRequestListOptions(cmd)
	if err != nil {
		return err
	}
	target, err := privilegeTargetUserFlag(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := privilege_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if target != "" {
		id, _, err := client.ResolveTargetUser(ctx.Credential.AccessToken, target)
		if err != nil {
			return err
		}
		opts.TargetUserID = &id
	}
	list, err := client.List(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to list privilege requests: %w", err)
	}

	if cmd.Flags().Changed("output") {
		switch outputFormat, _ := cmd.Flags().GetString("output"); outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, list)
		}
	}

	if len(list.Requests) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "privilege requests",
			[]privilegeRequestRow{}, &list.Total, privilegePlatformWideHint)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)

	if err := tableui.PrintFromCmd(cmd, privilegeRequestRows(list.Requests), privilegeRequestColumns); err != nil {
		return err
	}
	printMorePrivilegeHint(cmd, "requests", list.Total, list.Page, list.Limit)
	return nil
}

// privilegeRequestRows projects the listing. The person cells come from the
// emails the listing embeds, so the table needs no directory request.
func privilegeRequestRows(requests []privilege_requests.ListItem) []privilegeRequestRow {
	rows := make([]privilegeRequestRow, 0, len(requests))
	for _, r := range requests {
		row := privilegeRequestRow{
			ID:            r.ID.String(),
			Status:        string(r.Status),
			Action:        privilege_requests.ActionOf(r.OldValue, r.NewValue),
			Target:        emailOrID(r.TargetUserEmail, &r.TargetUserID),
			Requestor:     emailOrID(r.RequestorEmail, &r.RequestorID),
			Approver:      emailOrID(r.ApproverEmail, r.ApproverID),
			Requested:     timeutil.FormatRelative(r.RequestedAt),
			Justification: r.Justification,
		}
		if r.Status != api.PrivilegeRequestStatusPending {
			row.Decided = timeutil.FormatRelativePtr(r.DecisionAt)
		}
		rows = append(rows, row)
	}
	return rows
}

func privilegeRequestListOptions(cmd *cobra.Command) (privilege_requests.ListOptions, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return privilege_requests.ListOptions{}, err
	}
	opts := privilege_requests.ListOptions{Page: page, Limit: limit}
	if cmd.Flags().Changed("status") {
		raw, _ := cmd.Flags().GetString("status")
		status, err := privilege_requests.ParseStatus(raw)
		if err != nil {
			return privilege_requests.ListOptions{}, err
		}
		opts.Status = &status
	}
	return opts, nil
}

// privilegeTargetUserFlag reads --target-user: "" when unset, and bad usage
// when set to nothing (privilege_requests.CheckTargetUser).
func privilegeTargetUserFlag(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("target-user") {
		return "", nil
	}
	raw, _ := cmd.Flags().GetString("target-user")
	if err := privilege_requests.CheckTargetUser(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// emailOrID renders a person cell from a listing's embedded email, falling
// back to the UUID when the server sent none, and to "" when there is no
// person at all (the approver of an undecided request).
func emailOrID(email *string, id *openapi_types.UUID) string {
	if email != nil && *email != "" {
		return *email
	}
	if id == nil {
		return ""
	}
	return id.String()
}

// printMorePrivilegeHint tells the reader a further page exists. The page and
// limit are the ones the server echoed, so the arithmetic matches its total.
func printMorePrivilegeHint(cmd *cobra.Command, noun string, total, page, limit int) {
	if total > page*limit {
		fmt.Fprintf(cmd.ErrOrStderr(), "More %s — fetch the next page with --page %d.\n", noun, page+1)
	}
}
