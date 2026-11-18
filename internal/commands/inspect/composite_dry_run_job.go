package inspect

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// moreRowsExist reports whether a page after page holds rows: from totalRows
// when the server sent one, otherwise from hasNext.
func moreRowsExist(totalRows *int, hasNext bool, page, limit int) bool {
	if totalRows != nil {
		return page*limit < *totalRows
	}
	return hasNext
}

// NewCompositeDryRunJobCommand creates the 'inspect composite-dry-run-job' command.
func NewCompositeDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-dry-run-job <id>",
		Short: "Inspect a composite dry run's per-row evidence",
		Long: `Inspect the per-spine-row evidence behind a composite (linked-table) dry run:
which bucket each sampled spine row landed in, the join values it carried, the
upstream rows it matched, and the values it would have written.

The page is printed as the server sent it, JSON by default. Rows are written
when the dry run finishes, so a dry run that has not finished has none. For
the dry run itself and its structured summary, use
` + "`qctl describe composite-dry-run-job`" + `.

Examples:
  qctl inspect composite-dry-run-job 5512
  qctl inspect composite-dry-run-job 5512 --bucket blocked_zero_match
  qctl inspect composite-dry-run-job 5512 --source claims --page 2
  qctl inspect composite-dry-run-job 5512 -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("composite-dry-run-job"), "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid composite dry run job ID: %s", args[0])
			}
			page, err := cmdutil.PositiveCountFlag(cmd, "page")
			if err != nil {
				return err
			}
			limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
			if err != nil {
				return err
			}
			bucket, _ := cmd.Flags().GetString("bucket")
			source, _ := cmd.Flags().GetString("source")

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			examples, body, err := client.Examples(ctx.Credential.AccessToken, jobID, composite_dry_runs.ExamplesParams{
				Bucket:      bucket,
				SourceAlias: source,
				Page:        page,
				Limit:       limit,
			})
			if err != nil {
				return fmt.Errorf("failed to get composite dry run examples: %w", err)
			}

			// The body is printed rather than the decoded page: join and
			// synthesized values are data cells, and a decode through
			// float64 would round an integer past 2^53.
			formatStr, _ := cmd.Flags().GetString("output")
			if output.Format(formatStr) == output.FormatYAML {
				err = output.WriteJSONAsYAML(cmd.OutOrStdout(), body)
			} else {
				err = output.WriteIndentedJSON(cmd.OutOrStdout(), body)
			}
			if err != nil {
				return fmt.Errorf("failed to print output: %w", err)
			}

			if moreRowsExist(examples.TotalRows, examples.Next != nil, page, limit) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d.\n",
					len(examples.Results), page+1)
			}
			return nil
		},
	}

	cmd.Flags().String("bucket", "", "Only rows in this bucket (e.g. emitted_clean, blocked_zero_match; see qctl explain composite-dry-run-job)")
	cmd.Flags().String("source", "", "Only rows attributed to this source alias")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, "Maximum number of rows to return")

	return cmd
}
