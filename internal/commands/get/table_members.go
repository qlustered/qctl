package get

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

type tableMemberRow struct {
	ID         string `json:"id" yaml:"id"`
	Email      string `json:"email" yaml:"email"`
	Role       string `json:"role" yaml:"role"`
	Permission string `json:"permission_level" yaml:"permission_level"`
	Effective  string `json:"effective_permission_level" yaml:"effective_permission_level"`
}

// NewTableMembersCommand creates get table-members.
func NewTableMembersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-members [table]",
		Short: "List a table's members and their granted and effective levels",
		Long: `List all grants on a table through the current organization, ordered by email.
Only active members other than external users are listed. Effective levels
also depend on each member's organization role; a grant does not add role permissions.
JSON/YAML print a List of the API member records. Use the full user UUID
with table-member set-permission or table-member remove.

Examples:
  qctl get table-members orders
  qctl get table-members --table 42 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("table-member"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runTableMembers,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmdutil.DeclareColumns(cmd, []tableMemberRow(nil))
	return cmd
}

func runTableMembers(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
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
	c := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	members, err := c.ListMembers(ctx.Credential.AccessToken, id)
	if err != nil {
		return err
	}
	return printListPage(cmd, members, listPage{noun: "table members"}, func(data any) error {
		if _, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		}
		return printTableMembers(cmd, members)
	})
}

func printTableMembers(cmd *cobra.Command, members []api.DataSetMemberSchema) error {
	rows := make([]tableMemberRow, 0, len(members))
	for _, m := range members {
		rows = append(rows, tableMemberRow{ID: m.User.ID.String(), Email: m.User.Email, Role: string(m.Role),
			Permission: string(m.PermissionLevel), Effective: string(m.EffectivePermissionLevel)})
	}
	return tableui.PrintFromCmd(cmd, rows, "id,email,role,permission_level,effective_permission_level")
}
