package alert

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// addKeyFlags registers --key-columns and --no-key-columns.
func addKeyFlags(cmd *cobra.Command) {
	cmd.Flags().StringSlice("key-columns", nil,
		"With --action apply_migration on a table's first migration, the key columns to set, comma-separated (sends key_columns)")
	cmd.Flags().Bool("no-key-columns", false,
		"With --action apply_migration on a table's first migration, record the table as having no key columns (sends no_key_columns: true)")
}

// dryRunMigration is the migration details an apply_migration skeleton is
// written from, nil for another action or when the read fails, which warns on
// stderr.
func dryRunMigration(cmd *cobra.Command, ctx *cmdutil.CommandContext, alert *alerts.AlertFull, action string) *datasets.TableSchemaDetail {
	if action != string(api.AlertActionApplyMigration) {
		return nil
	}
	read := alerts.ReadMigration(ctx, alert.DatasetID)
	if read.Err != nil {
		line, _, _ := strings.Cut(read.Err.Error(), "\n")
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: could not read table %d's key step, so the manifest does not say whether it needs key_columns: %s\n",
			alert.DatasetID, terminal.Sanitize(line))
	}
	return read.Detail
}
