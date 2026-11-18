package pii_grant //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newRevokeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "Revoke an active grant of unmasked access to a table's personal data",
		Long: `End an active grant before it expires, recording who revoked it and why.

Take the grant id from qctl get pii-grants <table>; pii-grant/<id> is accepted
too. Select the table with --table <name> or --table-id <id>.

--reason is no_longer_needed (the default), wrong_user, compromised or other.
--note adds free text, and is required when the reason is other.

An unknown grant id exits 3, and a grant that has already expired or been
revoked is refused with a 409 (exit 2). Revoking needs the pii:grant
permission (admin or ops); anyone else gets a 403 (exit 4).

Prompts before revoking; --yes skips the prompt and is required when stdin is
not a terminal. On a protected context the command must also name it with
--context. -o json and -o yaml print the revoked grant as the server sent it;
-o name prints pii-grant/<id>.

Examples:
  qctl pii-grant revoke 7 --table orders
  qctl pii-grant revoke pii-grant/7 --table-id 42 --reason compromised --note "laptop lost" --yes`,
		Annotations: catalog.For("pii-grant"),
		Args:        cobra.ExactArgs(1),
		RunE:        runRevoke,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("reason", string(api.PiiAccessGrantRevokeReasonNoLongerNeeded),
		"Why the grant is revoked: "+strings.Join(pii_grants.RevokeReasonNames(), "|"))
	cmd.Flags().String("note", "", "Free-text note (required when --reason is other)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

// revokeInput is everything revoke settles from the command line.
type revokeInput struct {
	grantID int
	reason  pii_grants.RevokeReason
	note    *string
}

// parseRevoke settles the arguments before any credential read or request;
// each failure exits 2.
func parseRevoke(cmd *cobra.Command, arg string) (revokeInput, error) {
	var in revokeInput
	ids, err := cmdutil.ParseIntArgs([]string{arg}, "grant id")
	if err != nil {
		return in, err
	}
	if ids[0] <= 0 {
		return in, qerrors.NewBadUsagef("invalid grant id: %s", arg)
	}
	in.grantID = ids[0]
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return in, err
	}
	raw, _ := cmd.Flags().GetString("reason")
	if in.reason, err = pii_grants.ParseRevokeReason(raw); err != nil {
		return in, err
	}
	note, _ := cmd.Flags().GetString("note")
	if note = strings.TrimSpace(note); note != "" {
		in.note = &note
	}
	if in.reason == api.PiiAccessGrantRevokeReasonOther && in.note == nil {
		return in, qerrors.NewBadUsage("--note is required when --reason is other")
	}
	return in, nil
}

func runRevoke(cmd *cobra.Command, args []string) error {
	in, err := parseRevoke(cmd, args[0])
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
	question := fmt.Sprintf("Revoke pii-grant/%d on %s (reason: %s)?", in.grantID, tableRef(tableID), in.reason)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "revoking access to personal data requires confirmation; pass --yes", question); err != nil {
		return err
	}
	grant, err := pii_grants.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		Revoke(ctx.Credential.AccessToken, tableID, in.grantID, in.reason, in.note)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pii-grant/%d revoked (grantee: %s, %s, reason: %s)",
		grant.ID, terminal.Sanitize(grant.GranteeEmail), tableRef(grant.DatasetID), in.reason)
	return printGrant(cmd, grant, line)
}
