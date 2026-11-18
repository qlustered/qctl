package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newDriftCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift <id>",
		Short: "Show how a delivered run's rows differ from the live rows",
		Long: `Show the drift of one delivered run, by its integer id or
deliverable-run/<id>: the delivered rows that no longer match the live data,
each with the reasons why, and the remedy the server names. It is a report
only and changes nothing.

The remedy is one of:
  none         no drift, or a delivered re-issue of the same period supersedes the run
  reissue      drift in the latest delivered period, which a re-issue corrects
  restatement  drift in an older period, which needs a restatement or contra entry

Drift is recomputed on every call, which is heavy on a large run; the server
gives up after 30 seconds by default. A run that is not delivered has no
drift: the server answers 409 (exit 2).

--limit caps the rows returned; the server sends 1000 unless told otherwise,
and --limit takes 1 to 10000.

-o json or -o yaml prints the server's whole response, including fields this
qctl does not model.

Examples:
  qctl deliverable-run drift 42
  qctl deliverable-run drift deliverable-run/42 --limit 5000 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDrift,
	}
	cmd.Flags().Int("limit", 1000, "Most drifted rows to return, 1 to 10000")
	return cmd
}

func runDrift(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	params, err := driftParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := c.Drift(ctx.Credential.AccessToken, runID, params)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return deliverables.WriteDrift(cmd.OutOrStdout(), result.DeliveredRunDriftResponse)
}

// driftParams reads --limit, keeping it only when it was given. A limit
// outside 1 to deliverables.MaxDriftLimit is bad usage.
func driftParams(cmd *cobra.Command) (deliverables.DriftParams, error) {
	var params deliverables.DriftParams
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return params, err
	}
	if limit > deliverables.MaxDriftLimit {
		return params, qerrors.NewBadUsagef("--limit must be between 1 and %d (got %d)", deliverables.MaxDriftLimit, limit)
	}
	if cmd.Flags().Changed("limit") {
		params.Limit = limit
	}
	return params, nil
}
