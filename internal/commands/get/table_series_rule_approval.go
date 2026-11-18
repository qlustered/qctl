package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

func getRuleApproval(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return qerrors.NewBadUsage("--rule-approval requires a table-series name or UUID")
	}
	if err := table_series.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateColumnsFlag(cmd, []table_series.RuleApprovalDisplay(nil)); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, args[0])
	if err != nil {
		return err
	}
	approval, err := c.GetRuleApproval(token, id)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return table_series.WriteRuleApproval(cmd.OutOrStdout(), format, approval)
	case output.FormatName:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "table-series/%s\n", id)
		return err
	}
	printContextBanner(cmd, ctx)
	return tableui.PrintFromCmd(cmd, table_series.RuleApprovalRows(id, &approval.Status), table_series.RuleApprovalColumns)
}
