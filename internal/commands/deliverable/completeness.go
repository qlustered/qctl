package deliverable

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newCompletenessCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completeness <ref>",
		Short: "Show rows missed from delivered reports and where they go next",
		Long: `Show a deliverable's missed rows (the web app's "Missed rows" tab),
by exact slug or name, full UUID, or a UUID prefix of at least 8 hex digits.
Deliverables in the trash are included. This report changes nothing.

Each row names its sheet, table id, row id, the delivered report that missed
it, and what happens next: left out by a waiver, carried into a newer run,
held by a pending re-issue, or carried into the next period's run. The
Re-issue column says eligible when the server permits a re-issue and the row
has neither a waiver nor a pending re-issue; creating a run still applies
the server's usual gates. Run ids are shown without fetching their states.

--limit takes 1 to 10000 (default 1000). The server marks the report truncated
even at exactly the limit, so more rows may exist. An undecided row without
a counting waiver or an invalid row filter can fail the report with 400
(exit 2); the diagnostic explains how to check readiness before waiving.

-o json or -o yaml prints the server's whole response.

Examples:
  qctl deliverable completeness monthly-bordereau
  qctl deliverable completeness monthly-bordereau --limit 5000 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runCompleteness,
	}
	cmd.Flags().Int("limit", 1000, "Most missed rows to return, 1 to 10000")
	return cmd
}

func runCompleteness(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	params, err := completenessParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, args[0], true)
	if err != nil {
		return err
	}
	result, err := c.Completeness(token, id, params)
	if err != nil {
		return completenessAdvice(err, id)
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return deliverables.WriteCompleteness(cmd.OutOrStdout(), result.CompletenessResponse)
}

func completenessParams(cmd *cobra.Command) (deliverables.CompletenessParams, error) {
	var params deliverables.CompletenessParams
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return params, err
	}
	if limit > deliverables.MaxCompletenessLimit {
		return params, qerrors.NewBadUsagef("--limit must be between 1 and %d (got %d)", deliverables.MaxCompletenessLimit, limit)
	}
	if cmd.Flags().Changed("limit") {
		params.Limit = limit
	}
	return params, nil
}

func completenessAdvice(err error, id uuid.UUID) error {
	var apiErr *deliverables.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return err
	}
	return fmt.Errorf(`%w

Fix a filter that does not validate or compile against its table.
If the filter cannot decide a row, fix its data/filter or leave it out on the
carrier: the lowest-id new-period run in open, rendered, stale or signed_off.
Find the run with: qctl get deliverable-runs %s
Only an open, rendered or stale carrier can be waived. Check its readiness:
  qctl deliverable-run readiness <carrier-run-id>
Use the table and row ids from an eligible blocked or carried_unbound entry,
or the verified waivable_row target of a carried_rows error:
  qctl deliverable-run waive <carrier-run-id> --table-id <table-id> --row-id <row-id> --reason "<reason>"
Older servers may provide no waivable_row target. If readiness provides
neither an eligible row nor that target, fix its data/filter first`, err, id)
}
