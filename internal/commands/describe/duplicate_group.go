package describe

import (
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDuplicateGroupCommand creates the describe duplicate-group command.
func NewDuplicateGroupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "duplicate-group <group-id>",
		Short: "Show one duplicate group: key, decision, members and what you may do",
		Long: `Show one duplicate group of a mirror or append table: its origin and state,
its key (masked where your role masks the column), its own effective decision,
its pending decision and the decision it carries, each with its approvals and
disagreements, every member with where its row is and its outcome and row
values, the cells that differ between members, the senders' responses,
earlier decisions, the discussion, what you may do on it, and next actions
from its state and what you may do.

The group id must be a full UUID (or duplicate-group/<uuid>), and the table
must be named with --table or --table-id. 'qctl get duplicate-groups
--row-id <id>' finds the group of a row.

--sender shows the group as a data source's sender sees it: only the
copies on your cloud sources, with each one's file, file row, location and
outcome; the sender state (held with why, decided, loaded or ended); the
decision in effect, never a pending one; your sources' live and earlier
responses; the discussion when it is shared with your source; whether you may
respond; and the respond and withdraw-response commands. A separate-rows
response is a request, not a decision: the table's reviewers decide.

Use -o json|yaml for the raw API response.

Examples:
  qctl describe duplicate-group 0190aaaa-1111-4222-8333-444444444444 --table orders
  qctl describe duplicate-group 0190aaaa-1111-4222-8333-444444444444 --table-id 42 -o yaml
  qctl describe duplicate-group 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --sender`,
		Annotations: cmdutil.WithFormats(catalog.For("duplicate-group"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDescribeDuplicateGroup,
	}
	cmdutil.AddTableFlags(cmd, "Table (a mirror or append table)")
	cmd.Flags().Bool("sender", false, "Show the group as your cloud sources' sender sees it")
	return cmd
}

func runDescribeDuplicateGroup(cmd *cobra.Command, args []string) error {
	groupID, err := duplicate_groups.ParseGroupID(args[0])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
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
	client := duplicate_groups.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if sender, _ := cmd.Flags().GetBool("sender"); sender {
		return describeSourceDuplicateGroup(cmd, client, ctx.Credential.AccessToken, tableID, groupID)
	}
	group, err := client.Get(ctx.Credential.AccessToken, tableID, groupID)
	if err != nil {
		return cmdutil.HintOnNotFound(err, fmt.Sprintf(
			"no duplicate group %s on table/%d; 'qctl get duplicate-groups --table-id %d --include-ended' lists them",
			groupID, tableID, tableID))
	}
	if format, raw := rawDuplicateGroupFormat(cmd); raw {
		return output.WriteRaw(cmd.OutOrStdout(), format, group)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), duplicate_groups.RenderDetail(tableID, group))
	return err
}

// describeSourceDuplicateGroup prints the group as the caller's cloud
// sources see it.
func describeSourceDuplicateGroup(cmd *cobra.Command, client *duplicate_groups.Client, token string, tableID int,
	groupID openapi_types.UUID) error {
	group, err := client.GetSource(token, tableID, groupID)
	if err != nil {
		return cmdutil.HintOnNotFound(err, fmt.Sprintf(
			"no duplicate group %s with a copy on your cloud sources in table/%d; "+
				"'qctl get duplicate-groups --table-id %d --sender --include-ended' lists them",
			groupID, tableID, tableID))
	}
	if format, raw := rawDuplicateGroupFormat(cmd); raw {
		return output.WriteRaw(cmd.OutOrStdout(), format, group)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), duplicate_groups.RenderSourceDetail(tableID, group))
	return err
}

// rawDuplicateGroupFormat is the -o format and whether it is json or yaml.
func rawDuplicateGroupFormat(cmd *cobra.Command) (string, bool) {
	if !cmd.Flags().Changed("output") {
		return "", false
	}
	format, _ := cmd.Flags().GetString("output")
	return format, format == outputFormatJSON || format == outputFormatYAML
}
