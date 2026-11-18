package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// maxReadinessLimit is the largest rows page the server accepts.
const maxReadinessLimit = 500

func newReadinessCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "readiness <id>",
		Short: "Show what a deliverable run includes and what blocks its sign-off",
		Long: `Show the readiness of one deliverable run, by its integer id or
deliverable-run/<id>: whether anything blocks its sign-off, each sheet's row
counts by bucket and its row filter report, the errors and warnings, and one
page of the run's rows with each row's bucket, waiver note, whether its row
filter could not decide it, and why. FILTER UNDECIDED is yes or no for live
rows, and - for frozen rows.

Live readiness is recomputed on every call, which is heavy on a large run; the
server gives up after 30 seconds by default. A signed-off or delivered run
shows the summary frozen at sign-off instead ("As of sign-off" in the web
app). A cancelled run has no readiness: the server answers 409 (exit 2).

--bucket keeps the rows of one bucket (the web app's words on the right):
` + bucketTable() + `
--page and --limit page through the rows; the server sends page 1 of 50 rows
unless told otherwise, and --limit takes 1 to 500.

-o json or -o yaml prints the server's whole response, including fields this
qctl does not model.

Examples:
  qctl deliverable-run readiness 42
  qctl deliverable-run readiness 42 --bucket blocked_failing_rules
  qctl deliverable-run readiness deliverable-run/42 --page 2 --limit 200 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runReadiness,
	}
	cmd.Flags().String("bucket", "", "Keep the rows of one bucket: "+strings.Join(deliverables.ReadinessBuckets, ", "))
	cmd.Flags().Int("page", 1, "Page of rows")
	cmd.Flags().Int("limit", 50, "Rows per page, 1 to 500")
	return cmd
}

// bucketTable lists deliverables.ReadinessBuckets, each beside its web app
// label, one per line indented by two spaces.
func bucketTable() string {
	width := 0
	for _, bucket := range deliverables.ReadinessBuckets {
		width = max(width, len(bucket))
	}
	var b strings.Builder
	for _, bucket := range deliverables.ReadinessBuckets {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, bucket, deliverables.ReadinessBucketLabel(bucket))
	}
	return b.String()
}

func runReadiness(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	params, err := readinessParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := c.Readiness(ctx.Credential.AccessToken, runID, params)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return deliverables.WriteReadiness(cmd.OutOrStdout(), result.Readiness)
}

// readinessParams reads --bucket, --page and --limit, keeping each only when
// it was given. An unknown bucket, a page below 1 and a limit outside 1 to
// 500 are bad usage.
func readinessParams(cmd *cobra.Command) (deliverables.ReadinessParams, error) {
	var params deliverables.ReadinessParams
	if cmd.Flags().Changed("bucket") {
		bucket, _ := cmd.Flags().GetString("bucket")
		if !slices.Contains(deliverables.ReadinessBuckets, bucket) {
			return params, qerrors.NewBadUsagef("invalid --bucket: %q (valid: %s)", bucket, strings.Join(deliverables.ReadinessBuckets, ", "))
		}
		params.Bucket = bucket
	}
	page, err := cmdutil.PositiveCountFlag(cmd, "page")
	if err != nil {
		return params, err
	}
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return params, err
	}
	if limit > maxReadinessLimit {
		return params, qerrors.NewBadUsagef("--limit must be between 1 and %d (got %d)", maxReadinessLimit, limit)
	}
	if cmd.Flags().Changed("page") {
		params.Page = page
	}
	if cmd.Flags().Changed("limit") {
		params.Limit = limit
	}
	return params, nil
}
