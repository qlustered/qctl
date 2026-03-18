package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDatasetCommand creates the singular "get table <id-or-name>" command.
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table <id-or-name>",
		Short: "Get information about a specific table",
		Long: `Get information about a specific table by ID or name.

By default, shows the same summary columns as "get tables" scoped to one resource.
Use --job-activity to see running/waiting job counts instead.

The argument can be an integer ID or the table name. If the argument is a number,
it is treated as an ID. Otherwise, it is looked up by exact name match.`,
		Args: cobra.ExactArgs(1),
		RunE: runGetTable,
	}

	cmd.Flags().Bool("job-activity", false, "Show job activity (running/waiting job counts)")
	cmd.Flags().BoolP("watch", "w", false, "Watch for updates via websocket (requires --job-activity)")
	cmd.Flags().String("provider", "first-party", "Provider type (first-party|third-party)")
	cmd.Flags().Bool("changes-only", false, "Only show updates when values change (watch mode)")

	return cmd
}

func runGetTable(cmd *cobra.Command, args []string) error {
	// Bootstrap auth context
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	// Resolve table by name or integer ID
	tableID, err := client.ResolveID(ctx.Credential.AccessToken, args[0])
	if err != nil {
		return err
	}

	jobActivity, _ := cmd.Flags().GetBool("job-activity")
	if jobActivity {
		watch, _ := cmd.Flags().GetBool("watch")
		if watch {
			provider, _ := cmd.Flags().GetString("provider")
			changesOnly, _ := cmd.Flags().GetBool("changes-only")
			return runJobActivityWatch(cmd, ctx, tableID, provider, changesOnly)
		}
		return runJobActivityOneShot(cmd, ctx, tableID)
	}

	// Default: show table info (same columns as "get tables")
	result, err := client.GetDataset(ctx.Credential.AccessToken, tableID)
	if err != nil {
		return fmt.Errorf("failed to get table: %w", err)
	}

	setDefaultColumns(cmd, "name,state,destination_name,unresolved_alerts,progress_percent")

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(result); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}
