package reveal

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pii_reveals"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newCellCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cell [table]",
		Short: "Show one masked personal-data cell unmasked",
		Long: `Show the unmasked value of one cell of a personal-data column.

Name the table as the positional argument, or with --table <name> or
--table-id <id>. --row-id is the row's id (the id column of qctl get data) and
--field the column's name. The row is looked up among the table's clean rows;
--is-bad-data looks among its quarantined rows instead.

The value is printed alone on stdout, so it can be captured; the line saying
the reveal was recorded goes to stderr. A cell that holds null prints nothing
on stdout. -o json and -o yaml print the server's answer, {value, event_id}.

The server refuses a column that is not a personal-data column (400, exit 2),
and a row that does not exist or was deleted (404, exit 3).

Examples:
  qctl reveal cell orders --row-id 1042 --field email
  qctl reveal cell --table-id 42 --row-id 7 --field phone --is-bad-data -o json`,
		Annotations: annotations("table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runCell,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().Int("row-id", 0, "Id of the row holding the cell (required)")
	cmd.Flags().String("field", "", "Name of the personal-data column (required)")
	cmd.Flags().Bool("is-bad-data", false, "Look the row up among the table's quarantined rows")
	_ = cmd.MarkFlagRequired("row-id")
	_ = cmd.MarkFlagRequired("field")
	return cmd
}

func runCell(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	rowID, _, err := cmdutil.PositiveIDFlag(cmd, "row-id")
	if err != nil {
		return err
	}
	if err := cmdutil.RejectBlankFlags(cmd, "field"); err != nil {
		return err
	}
	field, _ := cmd.Flags().GetString("field")
	isBadData, _ := cmd.Flags().GetBool("is-bad-data")

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	what := fmt.Sprintf("row %d field %s", rowID, terminal.Sanitize(field))
	return send(cmd, ctx, tableID, pii_reveals.CellTarget(rowID, field, isBadData), what, terminal.Sanitize)
}
