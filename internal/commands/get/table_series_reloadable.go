package get

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

func getReloadableFiles(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return qerrors.NewBadUsage("--reloadable-files requires a table series name or UUID")
	}
	if err := table_series.ValidateReference(args[0]); err != nil {
		return err
	}
	period, _ := cmd.Flags().GetString("period")
	if !cmd.Flags().Changed("period") {
		return qerrors.NewBadUsage("--reloadable-files requires --period")
	}
	if err := table_series.ValidatePeriodKey(period); err != nil {
		return err
	}
	if err := validateColumnsFlag(cmd, []table_series.ReloadableFileDisplay(nil)); err != nil {
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
	items, _, err := c.ListReloadableFiles(token, id, period)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "reloadable files", []table_series.ReloadableFileDisplay(nil), nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, items, listPage{noun: "reloadable files"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case string(output.FormatJSON), string(output.FormatYAML):
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		case string(output.FormatName):
			p, err := output.NewPrinterFromCmd(cmd)
			if err != nil {
				return err
			}
			return p.NameAs("file", "id").Print(items)
		default:
			return tableui.PrintFromCmd(cmd, table_series.ReloadableFileRows(items), table_series.ReloadableFileColumns)
		}
	})
}
