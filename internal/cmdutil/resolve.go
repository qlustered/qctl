package cmdutil

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// errCloudSourceNeedsDataset guards the dataset-scoped lookups. Both of them
// send dataset_id unconditionally, so a zero would query dataset_id=0, match
// nothing, and report a missing cloud source for what is really a missing
// table. Callers with their own flag names should check first and say so in
// their own vocabulary; this is the invariant backstop.
//
// Bad usage (exit 2), not a generic error: the user omitted a required flag,
// the same class of mistake ValidateConflictingFlags and PositiveIDFlag
// already report at exit 2.
var errCloudSourceNeedsDataset = qerrors.NewBadUsage("resolving a cloud source requires a table; pass --table or --table-id, or use --cloud-source-id")

// refLabel renders a table reference for an error message: the quoted name when
// it is known, "ID <n>" when it is not. Keeps messages from rendering an empty
// quoted name or passing an ID off as a name.
func refLabel(id int, name string) string {
	if name != "" {
		return fmt.Sprintf("'%s'", name)
	}
	return fmt.Sprintf("ID %d", id)
}

// ResolveTable resolves a table by ID or name.
// Returns (datasetID, datasetName, error).
func ResolveTable(ctx *CommandContext, tableID int, tableName string) (int, string, error) {
	t, err := ResolveTableRecord(ctx, tableID, tableName)
	if err != nil {
		return 0, "", err
	}
	return t.ID, t.Name, nil
}

// ResolvedTable is a table as ResolveTableRecord found it. Full is the by-ID
// response, and nil when the table was found by name. SeriesID and PeriodKey
// name the table's series and its period there; both are empty for a table in
// no series.
type ResolvedTable struct {
	ID        int
	Name      string
	Full      *datasets.DatasetFull
	SeriesID  *uuid.UUID
	PeriodKey string
}

// seriesMembership returns the series ID and period key of a table, or nil and
// "" when either is missing.
func seriesMembership(series *api.TableSeriesTinySchema, period *api.TableSeriesMemberPeriodSchema) (*uuid.UUID, string) {
	if series == nil || period == nil || series.ID == uuid.Nil || period.PeriodKey == "" {
		return nil, ""
	}
	id := series.ID
	return &id, period.PeriodKey
}

// ResolveTableRecord resolves a table by ID or name, as ResolveTable does, and
// keeps the response it resolved from.
func ResolveTableRecord(ctx *CommandContext, tableID int, tableName string) (ResolvedTable, error) {
	return resolveTableRecord(ctx, tableID, tableName, "--table-id")
}

// ResolveTableByName resolves a table by exact name, as ResolveTableRecord
// does, with idFlag as the by-ID flag its not-found and ambiguous-name errors
// suggest.
func ResolveTableByName(ctx *CommandContext, tableName, idFlag string) (ResolvedTable, error) {
	return resolveTableRecord(ctx, 0, tableName, idFlag)
}

func resolveTableRecord(ctx *CommandContext, tableID int, tableName, idFlag string) (ResolvedTable, error) {
	datasetsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	switch {
	case tableID > 0:
		dataset, err := datasetsClient.GetDataset(token, tableID)
		if err != nil {
			return ResolvedTable{}, fmt.Errorf("failed to fetch table %d: %w", tableID, err)
		}
		seriesID, periodKey := seriesMembership(dataset.Series, dataset.Period)
		return ResolvedTable{ID: tableID, Name: dataset.Name, Full: dataset, SeriesID: seriesID, PeriodKey: periodKey}, nil

	case tableName != "":
		dataset, err := datasetsClient.ResolveByName(token, tableName, idFlag)
		if err != nil {
			return ResolvedTable{}, err
		}
		seriesID, periodKey := seriesMembership(dataset.Series, dataset.Period)
		return ResolvedTable{ID: dataset.ID, Name: dataset.Name, SeriesID: seriesID, PeriodKey: periodKey}, nil

	default:
		if tableID != 0 {
			// Reached only when a caller skipped its own flag validation: the
			// user typed a table ID, so "you must specify one" would contradict
			// them. Commands read these flags with PositiveIDFlag, which names
			// the flag and fails first.
			return ResolvedTable{}, qerrors.NewBadUsagef("invalid table ID %d: table IDs are positive integers", tableID)
		}
		// Bad usage (exit 2): the user omitted a required flag, the same class
		// of mistake OptionalTableID's own "table specified more than once"
		// already reports at exit 2.
		return ResolvedTable{}, qerrors.NewBadUsage("must specify either --table-id or --table")
	}
}

// ResolveCloudSource resolves a cloud source by ID, name, or auto-detection.
// An ID given with a datasetID must be a source of that table, or it is an exit-2
// error. Returns (cloudSourceID, cloudSourceName, error).
func ResolveCloudSource(ctx *CommandContext, datasetID int, datasetName string, cloudSourceID int, cloudSourceName string) (int, string, error) {
	cs, err := resolveCloudSourceRecord(ctx, ResolvedTable{ID: datasetID, Name: datasetName}, cloudSourceID, cloudSourceName, false)
	if err != nil {
		return 0, "", err
	}
	return cs.ID, cs.Name, nil
}

// ResolvedCloudSource is a cloud source as ResolveCloudSourceRecord found it.
// DatasetID is 0 and DatasetName "" when the response left the table out; a
// series source's table is its template table. TableSeriesID is nil and
// RoutingMode is "" for a source that feeds no series. Full is the by-ID
// response, and nil when the source was found by name or auto-detected.
type ResolvedCloudSource struct {
	ID                 int
	Name               string
	DatasetID          int
	DatasetName        string
	DeltaAppliedCursor *int
	TableSeriesID      *uuid.UUID
	RoutingMode        string
	Full               *cloud_sources.CloudSourceFull
}

// ResolveCloudSourceRecord resolves a cloud source as ResolveCloudSource does,
// and keeps the fields of the response it resolved from. An ID given with a
// table is also accepted when the table is a member of the source's series.
func ResolveCloudSourceRecord(ctx *CommandContext, table ResolvedTable, cloudSourceID int, cloudSourceName string) (ResolvedCloudSource, error) {
	return resolveCloudSourceRecord(ctx, table, cloudSourceID, cloudSourceName, true)
}

func resolveCloudSourceRecord(ctx *CommandContext, table ResolvedTable, cloudSourceID int, cloudSourceName string, acceptSeriesMember bool) (ResolvedCloudSource, error) {
	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return ResolvedCloudSource{}, fmt.Errorf("failed to create cloud sources client: %w", err)
	}
	token := ctx.Credential.AccessToken
	datasetID, datasetName := table.ID, table.Name

	var tiny cloud_sources.CloudSourceTiny
	switch {
	case cloudSourceID > 0:
		cs, err := client.GetCloudSource(token, cloudSourceID)
		if err != nil {
			return ResolvedCloudSource{}, fmt.Errorf("failed to fetch cloud source %d: %w", cloudSourceID, err)
		}
		r := ResolvedCloudSource{
			ID:                 cloudSourceID,
			DatasetName:        derefString(cs.DatasetName),
			DeltaAppliedCursor: cs.DeltaAppliedCursor,
			TableSeriesID:      cs.TableSeriesID,
			RoutingMode:        derefString(cs.RoutingMode),
			Full:               cs,
		}
		if cs.Name != nil {
			r.Name = *cs.Name
		}
		if cs.DatasetID != nil {
			r.DatasetID = *cs.DatasetID
		}
		if err := validateCloudSourceTable(ctx, table, r, acceptSeriesMember); err != nil {
			return ResolvedCloudSource{}, err
		}
		return r, nil

	case datasetID <= 0:
		// Every branch below scopes its lookup by dataset_id.
		return ResolvedCloudSource{}, errCloudSourceNeedsDataset

	case cloudSourceName != "":
		tiny, err = resolveCloudSourceByName(client, token, datasetID, datasetName, cloudSourceName)

	default:
		tiny, err = autoDetectCloudSource(client, token, datasetID, datasetName)
	}
	if err != nil {
		return ResolvedCloudSource{}, err
	}
	return ResolvedCloudSource{
		ID:                 tiny.ID,
		Name:               tiny.Name,
		DatasetID:          tiny.DatasetID,
		DatasetName:        tiny.DatasetName,
		DeltaAppliedCursor: tiny.DeltaAppliedCursor,
		TableSeriesID:      tiny.TableSeriesID,
		RoutingMode:        derefString(tiny.RoutingMode),
	}, nil
}

func validateCloudSourceTable(ctx *CommandContext, table ResolvedTable, cs ResolvedCloudSource, acceptSeriesMember bool) error {
	if table.ID <= 0 || cs.DatasetID == 0 || cs.DatasetID == table.ID {
		return nil
	}
	if acceptSeriesMember && cs.TableSeriesID != nil {
		if table.SeriesID == nil && table.Full == nil && table.PeriodKey == "" {
			var err error
			table, err = ResolveTableRecord(ctx, table.ID, table.Name)
			if err != nil {
				return err
			}
		}
		if feedsSeriesOf(cs, table) {
			return nil
		}
	}
	return qerrors.NewBadUsagef("cloud source %d feeds table ID %d, not table %s",
		cs.ID, cs.DatasetID, refLabel(table.ID, table.Name))
}

// feedsSeriesOf reports whether cs feeds the series table belongs to.
func feedsSeriesOf(cs ResolvedCloudSource, table ResolvedTable) bool {
	return cs.TableSeriesID != nil && table.SeriesID != nil && *cs.TableSeriesID == *table.SeriesID
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resolveCloudSourceByName finds a cloud source by exact name match.
func resolveCloudSourceByName(client *cloud_sources.Client, token string, datasetID int, datasetName, cloudSourceName string) (cloud_sources.CloudSourceTiny, error) {
	params := cloud_sources.GetCloudSourcesParams{
		DatasetID:   &datasetID,
		SearchQuery: &cloudSourceName,
	}
	results, _, err := client.GetAllCloudSources(token, params, 10)
	if err != nil {
		return cloud_sources.CloudSourceTiny{}, fmt.Errorf("failed to search for cloud source: %w", err)
	}

	// Filter for exact name match
	var matches []cloud_sources.CloudSourceTiny
	for _, cs := range results {
		if cs.Name == cloudSourceName {
			matches = append(matches, cs)
		}
	}

	if len(matches) == 0 {
		// Typed for the reason ResolveDataset's doc gives: a bare fmt.Errorf
		// degrades to exit 1, and the pages promise 3 for a name that names
		// nothing. The --cloud-source-id path already gets 3 from the
		// server's own 404.
		return cloud_sources.CloudSourceTiny{}, qerrors.NewNotFound(apierror.WithScopeNote(
			fmt.Sprintf("cloud source '%s' not found for table %s; use --cloud-source-id", cloudSourceName, refLabel(datasetID, datasetName))))
	}
	if len(matches) > 1 {
		return cloud_sources.CloudSourceTiny{}, fmt.Errorf("cloud source '%s' is ambiguous for table %s; use --cloud-source-id", cloudSourceName, refLabel(datasetID, datasetName))
	}

	return matches[0], nil
}

// autoDetectCloudSource finds the cloud source when the table has only one.
func autoDetectCloudSource(client *cloud_sources.Client, token string, datasetID int, datasetName string) (cloud_sources.CloudSourceTiny, error) {
	params := cloud_sources.GetCloudSourcesParams{DatasetID: &datasetID}
	results, _, err := client.GetAllCloudSources(token, params, 10)
	if err != nil {
		return cloud_sources.CloudSourceTiny{}, fmt.Errorf("failed to list cloud sources: %w", err)
	}

	if len(results) == 0 {
		return cloud_sources.CloudSourceTiny{}, fmt.Errorf("table %s has no cloud sources", refLabel(datasetID, datasetName))
	}
	if len(results) > 1 {
		return cloud_sources.CloudSourceTiny{}, qerrors.NewBadUsagef("table %s has multiple cloud sources; specify one with --cloud-source or --cloud-source-id", refLabel(datasetID, datasetName))
	}

	return results[0], nil
}
