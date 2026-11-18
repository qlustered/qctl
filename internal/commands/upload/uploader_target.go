package upload

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
)

// resolveUploaderTarget resolves an external uploader's table and source.
func resolveUploaderTarget(ctx *cmdutil.CommandContext, tableID int, tableName string, opts *fileOptions) (cmdutil.ResolvedTable, cmdutil.ResolvedCloudSource, error) {
	table := cmdutil.ResolvedTable{ID: tableID}
	if tableName != "" {
		return table, cmdutil.ResolvedCloudSource{}, qerrors.NewBadUsage("external uploaders cannot look up table names; use --table-id, or --cloud-source-id with --period")
	}
	lookup := table
	if opts.cloudSourceID > 0 {
		lookup = cmdutil.ResolvedTable{}
	}
	cs, err := cmdutil.ResolveCloudSourceRecord(ctx, lookup, opts.cloudSourceID, opts.cloudSourceName)
	if err != nil || tableID == 0 {
		return table, cs, err
	}
	if cs.TableSeriesID != nil {
		client := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		periods, err := client.ListSubmissionPeriods(ctx.Credential.AccessToken, *cs.TableSeriesID)
		if err != nil {
			return table, cs, fmt.Errorf("failed to list periods of cloud source %d: %w", cs.ID, err)
		}
		for _, p := range periods {
			if p.DatasetID != nil && *p.DatasetID == tableID {
				return uploaderPeriodTable(cs, p), cs, nil
			}
		}
		return table, cs, qerrors.NewBadUsagef("table ID %d is not an accessible submission period of cloud source %d's series", tableID, cs.ID)
	}
	if cs.DatasetID != tableID {
		return table, cs, qerrors.NewBadUsagef("cloud source %d feeds table ID %d, not table ID %d", cs.ID, cs.DatasetID, tableID)
	}
	table.Name = cs.DatasetName
	return table, cs, nil
}

// uploaderPeriodTable returns a table identified by a submission period that
// has a table.
func uploaderPeriodTable(cs cmdutil.ResolvedCloudSource, p api.TableSeriesSubmissionPeriodSchema) cmdutil.ResolvedTable {
	table := cmdutil.ResolvedTable{ID: *p.DatasetID, SeriesID: cs.TableSeriesID, PeriodKey: p.PeriodKey}
	if *p.DatasetID == cs.DatasetID {
		table.Name = cs.DatasetName
	}
	return table
}
