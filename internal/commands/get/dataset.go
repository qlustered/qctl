package get

import (
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDatasetCommand creates the singular "get table <id-or-name>" command.
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table <id-or-name>",
		Short: "Get information about a specific table",
		Long: `Get information about a specific table by ID or name.

By default, shows name, state, destination_name, bad_rows_count and clean_rows_count.
Use --columns to pick others, or --job-activity to see running/waiting job counts instead.

The argument can be an integer ID or the table name. If the argument is a number,
it is treated as an ID. Otherwise, it is looked up by exact name match.`,
		Annotations: catalog.For("table"),
		Args:        cobra.ExactArgs(1),
		RunE:        runGetDataset,
	}

	cmd.Flags().Bool("job-activity", false, "Show job activity (running/waiting job counts)")
	cmd.Flags().BoolP("watch", "w", false, "Watch for updates via websocket (requires --job-activity)")
	cmd.Flags().String("provider", "first-party", "Provider type (first-party|third-party; requires --job-activity --watch)")
	cmd.Flags().Bool("changes-only", false, "Only show updates when values change (requires --job-activity --watch)")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)

	cmdutil.DeclareColumns(cmd, []datasets.DatasetFull(nil))
	return cmd
}

func runGetDataset(cmd *cobra.Command, args []string) error {
	if args[0] == "" {
		return qerrors.NewBadUsage("table identifier cannot be empty")
	}
	jobActivity, _ := cmd.Flags().GetBool("job-activity")
	if format, _ := cmd.Flags().GetString("output"); jobActivity && format == string(output.FormatName) {
		return qerrors.NewBadUsagef("--job-activity prints job counts, which have no name: use -o json, -o yaml or the default view")
	}
	watch, budget, err := jawTableWatchFlags(cmd, jobActivity)
	if err != nil {
		return err
	}

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

	if jobActivity {
		if watch {
			provider, _ := cmd.Flags().GetString("provider")
			changesOnly, _ := cmd.Flags().GetBool("changes-only")
			return runJobActivityWatch(cmd, ctx, tableID, args[0], budget, provider, changesOnly)
		}
		return runJobActivityOneShot(cmd, ctx, tableID)
	}

	// Default: a one-row summary of the table itself
	result, err := client.GetDataset(ctx.Credential.AccessToken, tableID)
	if err != nil {
		return fmt.Errorf("failed to get table: %w", err)
	}

	// Both structured formats render the same object, and that object is the
	// one `apply` accepts. `get table -o yaml` used to emit the raw backend
	// response with status counters (bad_rows_count, created_at) interleaved
	// into configuration, which is a third shape of a table that nothing
	// consumes and nothing can round-trip.
	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat { //nolint:goconst // Keep output format handling local to this command.
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		manifest, err := datasets.APIResponseToManifest(result)
		if err != nil {
			return err
		}
		return encodeStructured(cmd, outputFormat, manifest)
	}

	// Every name here must be a json tag on api.DataSetSchemaFull — --columns
	// validation rejects the whole command otherwise, so a default naming a
	// field the detail schema lacks breaks `get table` for everyone.
	setDefaultColumns(cmd, "name,state,destination_name,bad_rows_count,clean_rows_count")

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(result); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// jawTableWatchFlags validates watch flags and resolves the watch's time budget.
func jawTableWatchFlags(cmd *cobra.Command, jobActivity bool) (watch bool, budget time.Duration, err error) {
	watch, _ = cmd.Flags().GetBool("watch")
	if watch && !jobActivity {
		return false, 0, qerrors.NewBadUsage("--watch requires --job-activity")
	}
	for _, name := range []string{"changes-only", "provider"} {
		if cmd.Flags().Changed(name) && !watch {
			return false, 0, qerrors.NewBadUsagef("--%s requires --job-activity --watch", name)
		}
	}
	if cmd.Flags().Changed("timeout") && !watch {
		return false, 0, qerrors.NewBadUsage("--timeout requires --watch")
	}
	if !watch {
		return false, 0, nil
	}
	budget, err = cmdutil.WatchBudget(cmd)
	return true, budget, err
}
