package get

import (
	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

func getSubmissionPeriods(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return qerrors.NewBadUsage("--periods requires a full table-series UUID; find table_series_id with describe cloud-source <id>")
	}
	id, err := uuid.Parse(args[0])
	if err != nil || len(args[0]) != 36 {
		return qerrors.NewBadUsage("--periods requires a full table-series UUID; name lookup requires series-list permission")
	}
	if err := validateColumnsFlag(cmd, []table_series.SubmissionPeriodDisplay(nil)); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	items, err := c.ListSubmissionPeriods(ctx.Credential.AccessToken, id)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "submission periods", []table_series.SubmissionPeriodDisplay(nil), nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, items, listPage{noun: "submission periods"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case string(output.FormatJSON), string(output.FormatYAML):
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		case string(output.FormatName):
			p, err := output.NewPrinterFromCmd(cmd)
			if err != nil {
				return err
			}
			return p.NameAs("table", "dataset_id").Print(items)
		default:
			return tableui.PrintFromCmd(cmd, table_series.SubmissionPeriodRows(items), table_series.SubmissionPeriodColumns)
		}
	})
}
