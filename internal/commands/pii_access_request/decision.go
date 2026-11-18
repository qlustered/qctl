package pii_access_request //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_access_requests"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newApproveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <request-id>",
		Short: "Approve an open request, granting its requester temporary unmasked access",
		Long: `Approve an open personal-data access request on a table you administer. The
approval creates a pii-grant for the requester, and the requester is notified.

Take the request id from qctl get pii-access-requests <table> --all;
pii-access-request/<id> is accepted too. Select the table with --table <name>
or --table-id <id>.

--duration shortens the grant (1h, 8h, 24h or 7d; never longer than the
requested duration). --reason replaces the request's own reason as the grant's
reason. Left out, each keeps the request's value.

An unknown request id exits 3. A request that is already decided or expired is
refused with a 409 (exit 2); a duration longer than requested, or a requester
who can no longer be granted access, with a 400 (exit 2). Approving needs the
pii:grant permission (admin or ops); anyone else gets a 403 (exit 4).

Prompts before approving; --yes skips the prompt and is required when stdin is
not a terminal. -o json and -o yaml print the decided request as the server
sent it; -o name prints pii-access-request/<id>.

Examples:
  qctl pii-access-request approve 7 --table orders
  qctl pii-access-request approve pii-access-request/7 --table-id 42 --duration 1h --yes`,
		Annotations: catalog.For("pii-access-request"),
		Args:        cobra.ExactArgs(1),
		RunE:        runApprove,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("duration", "", "Grant duration, at most the requested one: "+strings.Join(pii_grants.DurationNames(), "|"))
	cmd.Flags().String("reason", "", "The grant's reason, replacing the request's (up to 4000 characters)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

func runApprove(cmd *cobra.Command, args []string) error {
	requestID, err := parseRequestID(args[0])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	var duration *api.PiiGrantDuration
	if cmd.Flags().Changed("duration") {
		raw, _ := cmd.Flags().GetString("duration")
		d, err := pii_grants.ParseDuration(raw)
		if err != nil {
			return err
		}
		duration = &d
	}
	var reason *string
	if cmd.Flags().Changed("reason") {
		r, err := parseReason(cmd)
		if err != nil {
			return err
		}
		reason = &r
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	length := "the requested duration"
	if duration != nil {
		length = string(*duration)
	}
	yes, _ := cmd.Flags().GetBool("yes")
	question := fmt.Sprintf("Approve pii-access-request/%d on table/%d, granting its requester unmasked access for %s?",
		requestID, tableID, length)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "approving access to personal data requires confirmation; pass --yes", question); err != nil {
		return err
	}
	request, err := pii_access_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity, false).
		Approve(ctx.Credential.AccessToken, tableID, requestID, duration, reason)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pii-access-request/%d approved", request.ID)
	if request.GrantID != nil {
		line += fmt.Sprintf(", pii-grant/%d created", *request.GrantID)
	}
	line += fmt.Sprintf(": %s sees table/%d's personal data unmasked", terminal.Sanitize(request.RequesterEmail), request.DatasetID)
	if request.GrantExpiresAt != nil {
		line += " until " + request.GrantExpiresAt.Local().Format(time.RFC3339)
	}
	return printDecision(cmd, request, line, fmt.Sprintf("qctl get pii-grants --table-id %d", request.DatasetID))
}

func newDenyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deny <request-id>",
		Short: "Deny an open request for temporary unmasked access, with a reason",
		Long: `Deny an open personal-data access request on a table you administer. The
requester is notified with your --reason, and cannot ask again for 24 hours.

Take the request id from qctl get pii-access-requests <table> --all;
pii-access-request/<id> is accepted too. Select the table with --table <name>
or --table-id <id>. --reason is required (up to 4000 characters).

An unknown request id exits 3, and a request that is already decided or
expired is refused with a 409 (exit 2). Denying needs the pii:grant permission
(admin or ops); anyone else gets a 403 (exit 4).

Prompts before denying; --yes skips the prompt and is required when stdin is
not a terminal. On a protected context the command must also name it with
--context. -o json and -o yaml print the decided request as the server sent
it; -o name prints pii-access-request/<id>.

Examples:
  qctl pii-access-request deny 7 --table orders --reason "use the masked export"
  qctl pii-access-request deny pii-access-request/7 --table-id 42 --reason "not needed" --yes`,
		Annotations: catalog.For("pii-access-request"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDeny,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("reason", "", "Why the request is denied, shown to the requester (required, up to 4000 characters)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func runDeny(cmd *cobra.Command, args []string) error {
	requestID, err := parseRequestID(args[0])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	reason, err := parseReason(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "denying a request requires confirmation; pass --yes",
		fmt.Sprintf("Deny pii-access-request/%d on table/%d?", requestID, tableID)); err != nil {
		return err
	}
	request, err := pii_access_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity, false).
		Deny(ctx.Credential.AccessToken, tableID, requestID, reason)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pii-access-request/%d denied (requester: %s, table/%d)",
		request.ID, terminal.Sanitize(request.RequesterEmail), request.DatasetID)
	return printDecision(cmd, request, line, fmt.Sprintf("qctl get pii-access-requests --all --table-id %d", request.DatasetID))
}

// printDecision reports an approval or denial: the request as the server sent
// it for -o json|yaml, pii-access-request/<id> for -o name, and otherwise line
// followed by next.
func printDecision(cmd *cobra.Command, request *pii_access_requests.AdminRequest, line, next string) error {
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteRaw(out, format, request)
	case "name": //nolint:goconst // Keep output format handling local to this command.
		_, err := fmt.Fprintf(out, "pii-access-request/%d\n", request.ID)
		return err
	}
	_, err := fmt.Fprintf(out, "%s\nNext: %s\n", line, next)
	return err
}
