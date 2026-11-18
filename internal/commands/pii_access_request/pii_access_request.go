// Package pii_access_request implements the requester and admin commands for
// requests for temporary unmasked access to a table's personal data.
package pii_access_request //nolint:revive // Package name mirrors the command.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_access_requests"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCommand creates the pii-access-request command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "pii-access-request", Short: "Request temporary unmasked access to personal data, or decide a request",
		Long: `Ask a table's admins for temporary access to its unmasked personal data, or,
as an admin, decide such a request.

  request    ask for unmasked access for 1h, 8h, 24h or 7d (alias: ask)
  withdraw   withdraw your own open request
  approve    approve an open request, creating a pii-grant (admin or ops)
  deny       deny an open request with a reason (admin or ops)

List your own requests with qctl get pii-access-requests <table>, and every
requester's with qctl get pii-access-requests <table> --all.`,
		Annotations: catalog.Parent(), RunE: cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newRequestCommand(), newWithdrawCommand(), newApproveCommand(), newDenyCommand())
	return cmd
}

func newRequestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "request", Aliases: []string{"ask"}, Short: "Ask the table's admins for temporary unmasked access",
		Example: `  qctl pii-access-request request --table orders --duration 1h --reason "reconcile refunds" --yes
  qctl pii-access-request ask --shared --table-id 42 --reason "audit" --yes`,
		Annotations: catalog.For("pii-access-request"), Args: cobra.NoArgs, RunE: runRequest,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmdutil.AddSharedDataFlag(cmd)
	cmd.Flags().String("duration", "1h", "Requested duration: "+strings.Join(pii_grants.DurationNames(), "|"))
	cmd.Flags().String("reason", "", "Why unmasked access is needed (required, up to 4000 characters)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func runRequest(cmd *cobra.Command, _ []string) error {
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	raw, _ := cmd.Flags().GetString("duration")
	duration, err := pii_grants.ParseDuration(raw)
	if err != nil {
		return err
	}
	reason, err := parseReason(cmd)
	if err != nil {
		return err
	}
	ctx, tableID, shared, err := target(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "requesting access requires confirmation; pass --yes",
		fmt.Sprintf("Ask for unmasked access to table/%d's personal data for %s?", tableID, duration)); err != nil {
		return err
	}
	request, err := pii_access_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity, shared).
		Create(ctx.Credential.AccessToken, tableID, duration, reason)
	if err != nil {
		var throttle *apierror.ThrottleError
		if errors.As(err, &throttle) && throttle.RetryAfter > 0 {
			return fmt.Errorf("%w\nTry again after %s", err, throttle.RetryAfter)
		}
		return err
	}
	return printRequest(cmd, request, shared)
}

func newWithdrawCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "withdraw <request-id>", Short: "Withdraw your own open request for unmasked access",
		Example:     `  qctl pii-access-request withdraw pii-access-request/7 --table-id 42 --yes`,
		Annotations: catalog.For("pii-access-request"), Args: cobra.ExactArgs(1), RunE: runWithdraw,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmdutil.AddSharedDataFlag(cmd)
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

func runWithdraw(cmd *cobra.Command, args []string) error {
	requestID, err := parseRequestID(args[0])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	ctx, tableID, shared, err := target(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "withdrawing a request requires confirmation; pass --yes",
		fmt.Sprintf("Withdraw pii-access-request/%d on table/%d?", requestID, tableID)); err != nil {
		return err
	}
	request, err := pii_access_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity, shared).
		Withdraw(ctx.Credential.AccessToken, tableID, requestID)
	if err != nil {
		return err
	}
	return printRequest(cmd, request, shared)
}

// parseRequestID parses a request id argument; pii-access-request/<id> is
// stripped before RunE. Anything but a positive integer exits 2.
func parseRequestID(arg string) (int, error) {
	ids, err := cmdutil.ParseIntArgs([]string{arg}, "request id")
	if err != nil {
		return 0, err
	}
	if ids[0] <= 0 {
		return 0, qerrors.NewBadUsage("request id must be positive")
	}
	return ids[0], nil
}

// parseReason returns --reason trimmed; a blank one or one over 4000
// characters exits 2.
func parseReason(cmd *cobra.Command) (string, error) {
	if err := cmdutil.RejectBlankFlags(cmd, "reason"); err != nil {
		return "", err
	}
	reason, _ := cmd.Flags().GetString("reason")
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 4000 {
		return "", qerrors.NewBadUsage("--reason must be at most 4000 characters")
	}
	return reason, nil
}

func target(cmd *cobra.Command) (*cmdutil.CommandContext, int, bool, error) {
	shared, _ := cmd.Flags().GetBool("shared")
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, 0, shared, err
	}
	id, err := cmdutil.ResolveDataTableID(ctx, cmd, "", shared)
	return ctx, id, shared, err
}

func printRequest(cmd *cobra.Command, request *pii_access_requests.Request, shared bool) error {
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteRaw(out, format, request)
	case "name": //nolint:goconst // Keep output format handling local to this command.
		_, err := fmt.Fprintf(out, "pii-access-request/%d\n", request.ID)
		return err
	}
	if _, err := fmt.Fprintf(out, "pii-access-request/%d %s (table/%d)\n", request.ID,
		terminal.Sanitize(string(request.State)), request.DatasetID); err != nil {
		return err
	}
	provider := ""
	if shared {
		provider = " --shared"
	}
	_, err := fmt.Fprintf(out, "Next: qctl get pii-access-requests --table-id %d%s\n", request.DatasetID, provider)
	return err
}
