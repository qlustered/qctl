package pii_grant //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// createInput is everything create settles from the command line.
type createInput struct {
	toEmail  string
	toID     openapi_types.UUID
	duration pii_grants.Duration
	reason   string
}

func newCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --to <email>",
		Short: "Grant a user temporary unmasked access to a table's personal data",
		Long: `Grant one user unmasked access to a table's personal data for a fixed
duration, recording who granted it and why.

Select the table with --table <name> or --table-id <id>. Name the user with
--to <email>, which is looked up among the table's members (qctl get
table-members), or with --to-id <uuid>. A user who reads the table through a
share from another organization is not a member here, so name them by UUID.

--duration is 1h (the default), 8h, 24h or 7d, counted from the moment the
server records the grant. --reason is required and is kept with the grant.

The server refuses (400, exit 2) a grant to yourself, to a user who cannot
read the table, and to a user whose role in this organization already sees
personal data unmasked. Granting needs the pii:grant permission (admin or ops);
anyone else gets a 403 (exit 4).

Prompts before granting; --yes skips the prompt and is required when stdin is
not a terminal. -o json and -o yaml print the grant as the server sent it;
-o name prints pii-grant/<id>.

Examples:
  qctl pii-grant create --table orders --to alice@example.com --reason "INC-4411: reconcile refunds"
  qctl pii-grant create --table-id 42 --to-id 11111111-2222-3333-4444-555555555555 --duration 8h --reason "audit" --yes`,
		Annotations: catalog.For("pii-grant"),
		Args:        cobra.NoArgs,
		RunE:        runCreate,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("to", "", "Email of the table member to grant access to")
	cmd.Flags().String("to-id", "", "UUID of the user to grant access to")
	cmd.Flags().String("duration", "1h", "How long the grant lasts: "+strings.Join(pii_grants.DurationNames(), "|"))
	cmd.Flags().String("reason", "", "Why the user needs unmasked access (required)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

// parseCreate settles every flag before any credential read or request; each
// failure exits 2.
func parseCreate(cmd *cobra.Command) (createInput, error) {
	var in createInput
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return in, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"to", "to-id"}); err != nil {
		return in, err
	}
	if err := cmdutil.RequireOneOfFlags(cmd, []string{"to", "to-id"}); err != nil {
		return in, err
	}
	if err := cmdutil.RejectBlankFlags(cmd, "to", "reason"); err != nil {
		return in, err
	}
	if raw, _ := cmd.Flags().GetString("to-id"); cmd.Flags().Changed("to-id") {
		id, err := cmdutil.ParseUUIDArg(strings.TrimSpace(raw), "--to-id")
		if err != nil {
			return in, err
		}
		in.toID = id
	}
	raw, _ := cmd.Flags().GetString("duration")
	duration, err := pii_grants.ParseDuration(raw)
	if err != nil {
		return in, err
	}
	in.duration = duration
	in.toEmail, _ = cmd.Flags().GetString("to")
	in.toEmail = strings.TrimSpace(in.toEmail)
	in.reason, _ = cmd.Flags().GetString("reason")
	in.reason = strings.TrimSpace(in.reason)
	return in, nil
}

func runCreate(cmd *cobra.Command, _ []string) error {
	in, err := parseCreate(cmd)
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
	token := ctx.Credential.AccessToken
	grantee, who := in.toID, in.toID.String()
	if in.toEmail != "" {
		grantee, err = resolveGrantee(ctx, token, tableID, in.toEmail)
		if err != nil {
			return err
		}
		who = in.toEmail
	}

	yes, _ := cmd.Flags().GetBool("yes")
	question := fmt.Sprintf("Grant %s unmasked access to %s's personal data for %s?", who, tableRef(tableID), in.duration)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "granting access to personal data requires confirmation; pass --yes", question); err != nil {
		return err
	}

	grant, err := pii_grants.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		Create(token, tableID, grantee, in.duration, in.reason)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pii-grant/%d created: %s sees %s's personal data unmasked until %s",
		grant.ID, terminal.Sanitize(grant.GranteeEmail), tableRef(grant.DatasetID), expiry(grant.ExpiresAt))
	return printGrant(cmd, grant, line)
}

// resolveGrantee maps --to to a user id through the table's member listing.
func resolveGrantee(ctx *cmdutil.CommandContext, token string, tableID int, email string) (openapi_types.UUID, error) {
	members, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ListMembers(token, tableID)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("failed to resolve --to %q: %w", email, err)
	}
	candidates := make([]pii_grants.Member, len(members))
	for i, m := range members {
		candidates[i] = pii_grants.Member{ID: m.User.ID, Email: m.User.Email}
	}
	return pii_grants.ResolveGrantee(candidates, email, tableRef(tableID))
}
