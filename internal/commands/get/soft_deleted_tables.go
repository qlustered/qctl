package get

import (
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const softDeletedTableColumns = "reference,name,organization,destination,database,relation,deleted,deleted_by,size"

// softDeletedTableRow is the table-display projection of one report table.
type softDeletedTableRow struct {
	Reference      string `json:"reference"`
	Name           string `json:"name"`
	Organization   string `json:"organization"`
	OrganizationID string `json:"organization_id"`
	Destination    string `json:"destination"`
	DestinationID  string `json:"destination_id"`
	Database       string `json:"database"`
	Relation       string `json:"relation"`
	Deleted        string `json:"deleted"`
	DeletedAt      string `json:"deleted_at"`
	DeletedBy      string `json:"deleted_by"`
	DeletedByID    string `json:"deleted_by_id"`
	Size           string `json:"size"`
	SizeBytes      string `json:"size_bytes"`
}

// NewSoftDeletedTablesCommand creates the get soft-deleted-tables command.
func NewSoftDeletedTablesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "soft-deleted-tables",
		Short: "Report soft-deleted tables and their destination size (ops, platform-wide)",
		Long: `Report the tables soft-deleted at least --min-age-days ago, with who deleted
each one and the size of its destination tables.

This is an operations surface: the route answers 404 (exit 3) to anyone whose
role in the current organization is not ops. The report is platform-wide --
every organization's deleted tables, whichever organization the current
context points at.

Rows come in the server's order: grouped by organization, destination and
database, oldest deletion first within a group. Without --min-age-days the
server applies its own report setting. A table summary on stderr gives the
cutoff, the table count and the total size; a destination the server could
not read has no size, and stderr names its error.

Available columns: reference, name, organization, organization_id,
destination, destination_id, database, relation, deleted, deleted_at,
deleted_by, deleted_by_id, size, size_bytes. -o json and -o yaml emit the raw
wire report; -o name prints table/<id> for each table.

Examples:
  qctl get soft-deleted-tables
  qctl get soft-deleted-tables --min-age-days 0
  qctl get soft-deleted-tables -o json`,
		Annotations: output.WithNameKind(catalog.For("soft-deleted-tables"), "table"),
		Args:        cobra.NoArgs,
		RunE:        runSoftDeletedTables,
	}
	cmd.Flags().Int("min-age-days", 0, "Only tables deleted at least this many days ago (default: the server's report setting)")
	cmdutil.DeclareColumns(cmd, []softDeletedTableRow(nil))
	return cmd
}

func runSoftDeletedTables(cmd *cobra.Command, _ []string) error {
	var minAgeDays *int
	if cmd.Flags().Changed("min-age-days") {
		days, err := cmdutil.NonNegativeCountFlag(cmd, "min-age-days")
		if err != nil {
			return err
		}
		minAgeDays = &days
	}
	if isTableFormat(cmd) {
		if err := validateColumnsFlag(cmd, []softDeletedTableRow(nil)); err != nil {
			return err
		}
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	report, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetSoftDeletedTables(ctx.Credential.AccessToken, minAgeDays)
	if err != nil {
		if qerrors.GetExitCode(err) == qerrors.ExitNotFound {
			return fmt.Errorf("failed to get soft-deleted tables: %w\n\n%s", err,
				"The report answers 404 unless your role in the current organization is ops.")
		}
		return fmt.Errorf("failed to get soft-deleted tables: %w", err)
	}

	format, _ := cmd.Flags().GetString("output")
	switch format {
	case string(output.FormatJSON), string(output.FormatYAML):
		return output.WriteRaw(cmd.OutOrStdout(), format, report)
	case string(output.FormatName):
		for _, row := range softDeletedTableRows(report) {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), row.Reference); err != nil {
				return err
			}
		}
		return nil
	}

	stderr := cmd.ErrOrStderr()
	if report.TableCount == 0 {
		fmt.Fprintf(stderr, "No table was soft-deleted at least %d days ago (on or before %s) in any organization.\n",
			report.MinAgeDays, report.DeletedBefore.UTC().Format(time.RFC3339))
		return nil
	}
	printContextBanner(cmd, ctx)
	if err := tableui.PrintFromCmd(cmd, softDeletedTableRows(report), softDeletedTableColumns); err != nil {
		return err
	}
	printSoftDeletedSummary(cmd, report)
	return nil
}

// softDeletedTableRows flattens the report's groups into one row per table,
// in the server's order.
func softDeletedTableRows(report *api.SoftDeletedTablesReportSchema) []softDeletedTableRow {
	rows := make([]softDeletedTableRow, 0, report.TableCount)
	for _, group := range report.Groups {
		orgID := ""
		if group.OrganizationID != nil {
			orgID = group.OrganizationID.String()
		}
		for _, table := range group.Tables {
			row := softDeletedTableRow{
				Reference:      fmt.Sprintf("table/%d", table.DatasetID),
				Name:           table.Name,
				Organization:   group.OrganizationName,
				OrganizationID: orgID,
				Destination:    group.DestinationName,
				DestinationID:  strconv.Itoa(group.DestinationID),
				Database:       group.DatabaseName,
				Relation:       table.SchemaName + "." + table.TableName,
				Deleted:        timeutil.FormatRelative(table.DeletedAt),
				DeletedAt:      table.DeletedAt.UTC().Format(time.RFC3339),
				DeletedBy:      emailOrID(table.DeletedByName, table.DeletedByID),
				Size:           "unknown",
			}
			if table.DeletedByID != nil {
				row.DeletedByID = table.DeletedByID.String()
			}
			if table.SizeBytes != nil {
				row.Size = formatByteSize(*table.SizeBytes)
				row.SizeBytes = strconv.Itoa(*table.SizeBytes)
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// printSoftDeletedSummary writes the report's totals to stderr, then one line
// per group whose destination the server could not read.
func printSoftDeletedSummary(cmd *cobra.Command, report *api.SoftDeletedTablesReportSchema) {
	stderr := cmd.ErrOrStderr()
	var unknown []api.SoftDeletedTablesGroupSchema
	for _, group := range report.Groups {
		if group.SizeBytes == nil {
			unknown = append(unknown, group)
		}
	}
	fmt.Fprintf(stderr, "\n%d table(s) soft-deleted at least %d days ago (on or before %s), %s in all",
		report.TableCount, report.MinAgeDays, report.DeletedBefore.UTC().Format(time.RFC3339),
		formatByteSize(report.SizeBytes))
	if len(unknown) > 0 {
		fmt.Fprintf(stderr, ", not counting %d group(s) of unknown size", len(unknown))
	}
	fmt.Fprintln(stderr, ".")
	for _, group := range unknown {
		reason := "no reason given"
		if group.SizeError != nil && *group.SizeError != "" {
			reason = *group.SizeError
		}
		fmt.Fprintf(stderr, "Size unknown for %s / %s (%s): %s\n",
			terminal.Sanitize(group.OrganizationName), terminal.Sanitize(group.DestinationName),
			terminal.Sanitize(group.DatabaseName), terminal.Sanitize(reason))
	}
}

// formatByteSize renders size in the largest 1024-based unit that keeps it at
// or above 1, to one decimal place.
func formatByteSize(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d bytes", size)
	}
	value := float64(size)
	for _, unit := range []string{"KB", "MB", "GB"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f TB", value/1024)
}
