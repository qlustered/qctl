package get

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

func getUploadableTableSeries(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return qerrors.NewBadUsage("--uploadable does not take a table-series reference")
	}
	if err := validateColumnsFlag(cmd, []table_series.UploadableDisplay(nil)); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	items, err := c.ListUploadable(ctx.Credential.AccessToken)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "uploadable table series", []table_series.UploadableDisplay(nil), nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, items, listPage{noun: "uploadable table series"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case string(output.FormatJSON), string(output.FormatYAML):
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		case string(output.FormatName):
			p, err := output.NewPrinterFromCmd(cmd)
			if err != nil {
				return err
			}
			return p.NameAs("table-series", "id").Print(items)
		default:
			return tableui.PrintFromCmd(cmd, table_series.UploadableRows(items), table_series.UploadableColumns)
		}
	})
}
