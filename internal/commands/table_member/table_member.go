// Package table_member implements table membership changes.
package table_member //nolint:revive // Package name mirrors the command.

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	verbLeave         = "leave"
	verbSetPermission = "set-permission"
)

// NewCommand creates the table-member group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "table-member", Short: "Change or remove table grants",
		Long: `Manage existing table grants. List them with qctl get table-members <table>.
Grant new access with qctl share table <table> --to <email> --permission <level>.
Owners and ops/admin can manage other users; any member can leave.
Removing or lowering the last owner grant is refused unless you are ops/admin.`,
		Annotations: catalog.Parent(), RunE: cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newMutation(verbSetPermission), newMutation("remove"), newMutation(verbLeave))
	return cmd
}

func newMutation(verb string) *cobra.Command {
	use, short := verb+" <user-id>", "Change a member's granted table level"
	args := cobra.ExactArgs(1)
	kinds := []string{"-"}
	if verb == "remove" {
		short = "Remove a member's table grant"
	}
	if verb == verbLeave {
		use, short, args, kinds = "leave [table]", "Remove your own table grant", cobra.MaximumNArgs(1), []string{"table"}
	}
	cmd := &cobra.Command{
		Use: use, Short: short,
		Long: short + `.

For set-permission/remove, supply the full user UUID from get table-members
and select the table with --table <name> or --table-id <id>.
leave takes a table ID or name, or either table flag, and reads your identity
from auth me before removing your grant. ops/admin keep access through their role.
A non-privileged last owner must make someone else an owner first (409, exit 2).
External users cannot receive or change grants; leftover grants can be removed.

Prompts before changing access; --yes skips the prompt. Removing your own
grant, or lowering your own level, can require another owner to restore it.
JSON/YAML print the changed member, or {result, dataset_id, user_id} on removal.
There is no name output because a grant is addressed by both table and user.

Examples:
  qctl table-member set-permission 11111111-2222-3333-4444-555555555555 --table orders --permission editor --yes
  qctl table-member remove 11111111-2222-3333-4444-555555555555 --table orders --yes
  qctl table-member leave orders --yes`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("table-member"), "table", "json", "yaml"), kinds...),
		Args:        args,
		RunE:        func(cmd *cobra.Command, args []string) error { return runMutation(cmd, args, verb) },
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	if verb == verbSetPermission {
		cmd.Flags().String("permission", "", "Grant level: owner|editor|read_only")
		_ = cmd.MarkFlagRequired("permission")
	}
	return cmd
}

func parseMutation(cmd *cobra.Command, args []string, verb string) (string, uuid.UUID, api.DataSetPermission, error) {
	var userID uuid.UUID
	var level api.DataSetPermission
	var err error
	positional := ""
	if verb == verbLeave {
		if len(args) > 0 {
			positional = args[0]
		}
		err = cmdutil.ValidateTableSelector(cmd, positional)
	} else {
		userID, err = cmdutil.ParseUUIDArg(args[0], "user-id")
		if err == nil {
			err = cmdutil.ValidateTableSelectorFromFlags(cmd)
		}
	}
	if err != nil {
		return "", uuid.Nil, "", err
	}
	if verb == verbSetPermission {
		value, _ := cmd.Flags().GetString("permission")
		level, err = datasets.ParsePermissionLevel(value)
		if err != nil {
			return "", uuid.Nil, "", err
		}
	}
	return positional, userID, level, nil
}

func runMutation(cmd *cobra.Command, args []string, verb string) error {
	positional, userID, level, err := parseMutation(cmd, args, verb)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	id, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	if verb == verbLeave {
		me, err := auth.NewClient(ctx.ServerURL, ctx.Verbosity).GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
		if err != nil {
			return err
		}
		userID, err = uuid.Parse(me.ID)
		if err != nil {
			return fmt.Errorf("invalid current user id: %w", err)
		}
	}
	yes, _ := cmd.Flags().GetBool("yes")
	question := fmt.Sprintf("Remove user %s's grant on table %d?", userID, id)
	if verb == verbSetPermission {
		question = fmt.Sprintf("Set user %s's grant on table %d to %s?", userID, id, level)
	}
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "changing table access requires confirmation; pass --yes", question); err != nil {
		return err
	}
	c := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	var result any
	if verb == verbSetPermission {
		result, err = c.ChangeMemberLevel(ctx.Credential.AccessToken, id, userID, level)
	} else {
		err = c.RemoveMember(ctx.Credential.AccessToken, id, userID)
		result = struct {
			Result    string    `json:"result" yaml:"result"`
			DatasetID int       `json:"dataset_id" yaml:"dataset_id"`
			UserID    uuid.UUID `json:"user_id" yaml:"user_id"`
		}{"removed", id, userID}
	}
	if err != nil {
		return err
	}
	return printMutation(cmd, id, userID, result)
}

func printMutation(cmd *cobra.Command, id int, userID uuid.UUID, result any) error {
	format, _ := cmd.Flags().GetString("output")
	if format == "json" || format == "yaml" {
		return output.WriteRaw(cmd.OutOrStdout(), format, result)
	}
	if member, ok := result.(*api.DataSetMemberSchema); ok {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "table/%d user %s grant set to %s (effective %s)\n", id, userID,
			terminal.Sanitize(string(member.PermissionLevel)), terminal.Sanitize(string(member.EffectivePermissionLevel)))
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "table/%d user %s grant removed\n", id, userID)
	return err
}
