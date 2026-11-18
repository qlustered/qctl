package describe

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the describe cloud-source command
func NewCloudSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Summarize a cloud source: its table, state, last ingest, rows and overrides",
		Long: `Summarize a cloud source: its type and table, state and schedule, when an
ingestion job on it last finished, its clean and quarantined row counts, its
unresolved alerts, and which of the table's settings it overrides — names
only, never values.

For a table-series source, also shows each visible period binding's table,
settings model and applied delta cursor. A '-' cursor has not been applied.
A portal (qluster_ui) source in a table series also shows its email intake:
on or off, its address, and whether each loaded file's issues report is sent
automatically.

For an ordinary source with quarantined rows, one additional request shows
their validation and hold breakdowns. If that read fails, the summary still
prints and a warning goes to stderr. Series sources omit these breakdowns.

This view otherwise makes two requests. For the manifest and table settings:

  qctl get cloud-source <id> -o yaml   the apply-compatible manifest
  qctl get table-config <table>        the table's settings

-o json|yaml emit the same summary as a structured object.

Examples:
  qctl describe cloud-source 42
  qctl describe cloud-source 42 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("cloud-source"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cloudSourceID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid cloud source ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// The detail read goes first: it is the one that answers 404 for
			// a deleted source.
			full, body, err := client.GetCloudSourceBody(ctx.Credential.AccessToken, cloudSourceID)
			if err != nil {
				return fmt.Errorf("failed to get cloud source: %w", err)
			}
			tiny, err := client.GetCloudSourceTiny(ctx.Credential.AccessToken, cloudSourceID)
			if err != nil {
				return fmt.Errorf("failed to get cloud source: %w", err)
			}
			summary, err := cloud_sources.NewSummary(full, body, tiny)
			if err != nil {
				return err
			}
			if summary.TableSeriesID == nil && summary.BadRowsCount != nil && *summary.BadRowsCount > 0 {
				dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
				counts, err := dsClient.GetQuarantineCountByStatus(ctx.Credential.AccessToken, summary.TableID,
					datasets.QuarantineCountScope{DataSourceModelID: &cloudSourceID})
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: quarantine counts unavailable: %v\n", err)
				} else {
					summary.QuarantineByValidation = &datasets.QuarantineValidation{
						PassedValidation: intOrZero(counts.PassedValidation),
						FailedValidation: intOrZero(counts.FailedValidation),
					}
					summary.QuarantineByHold = quarantineHolds(counts)
					summary.QuarantineNotLoaded = counts.NotLoaded
				}
			}

			// No Changed("output") special case: the default view is the text
			// summary, so an unset flag and -o table mean the same thing.
			outputFormat, _ := cmd.Flags().GetString("output")
			switch outputFormat {
			case outputFormatJSON, outputFormatYAML:
				return encodeSchemaStructured(cmd, outputFormat, summary)
			default:
				cloud_sources.RenderSummary(cmd.OutOrStdout(), summary)
				return nil
			}
		},
	}

	return cmd
}
