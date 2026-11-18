package delete

import (
	"fmt"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

const untagBatchSize = 200

type taggedTable struct {
	ID   int
	Name string
}

// listTaggedTables reads the caller-visible active and disabled tables with this kind.
func listTaggedTables(server, orgID, token string, verbosity int, kindID openapi_types.UUID) ([]taggedTable, error) {
	client := datasets.NewClient(server, orgID, verbosity)
	var tables []taggedTable
	seen := make(map[int]bool)
	var total *int
	for page := 1; ; page++ {
		resp, err := client.GetDatasets(token, datasets.GetDatasetsParams{
			ProblemDomainID: &kindID, OrderBy: "id", Reverse: true,
			Page: page, Limit: untagBatchSize,
		})
		if err != nil {
			if page == 1 && apierror.IsNoVisibleRows(err) {
				return nil, nil
			}
			return nil, err
		}
		if page == 1 {
			total = resp.TotalRows
		}
		for _, table := range resp.Results {
			if seen[table.ID] {
				return nil, fmt.Errorf("table %d appeared on more than one page; retry", table.ID)
			}
			seen[table.ID] = true
			tables = append(tables, taggedTable{ID: table.ID, Name: table.Name})
		}
		if total != nil {
			if len(tables) >= *total || len(resp.Results) == 0 {
				break
			}
		} else if resp.Next == nil {
			break
		}
		if len(resp.Results) == 0 {
			return nil, fmt.Errorf("table listing has a next page after an empty page; retry")
		}
	}
	if total != nil && len(tables) != *total {
		return nil, fmt.Errorf("table listing changed while paging (expected %d, found %d); retry", *total, len(tables))
	}
	return tables, nil
}

// untagTaggedTables sends bounded batches and reports committed removals.
func untagTaggedTables(client *dataset_kinds.Client, token string, kindID openapi_types.UUID, tables []taggedTable) (int, error) {
	removed := 0
	for start := 0; start < len(tables); start += untagBatchSize {
		end := min(start+untagBatchSize, len(tables))
		ids := make([]int, 0, end-start)
		for _, table := range tables[start:end] {
			ids = append(ids, table.ID)
		}
		resp, err := client.RemoveFromDatasets(token, kindID, ids)
		if err != nil {
			return removed, fmt.Errorf("%d earlier table(s) were untagged; current batch may also have changed; failed to untag tables: %w", removed, err)
		}
		if len(resp.Results) != len(ids) {
			return removed, fmt.Errorf("%d earlier table(s) were untagged; unexpected untag response with %d results for %d tables", removed, len(resp.Results), len(ids))
		}
		var failed []string
		for i, result := range resp.Results {
			if result.DatasetID != ids[i] {
				return removed, fmt.Errorf("%d earlier table(s) were untagged; unexpected untag response for table %d", removed, result.DatasetID)
			}
			switch result.Status {
			case api.RemoveProblemDomainFromDatasetResultSchemaStatusRemoved:
				removed++
			case api.RemoveProblemDomainFromDatasetResultSchemaStatusNotTagged:
			case api.RemoveProblemDomainFromDatasetResultSchemaStatusNoAccess, api.RemoveProblemDomainFromDatasetResultSchemaStatusNotFound, api.RemoveProblemDomainFromDatasetResultSchemaStatusViewOnly:
				failed = append(failed, fmt.Sprintf("%s (%d): %s", terminal.Sanitize(tables[start+i].Name), result.DatasetID, result.Status))
			default:
				return removed, fmt.Errorf("%d table(s) were untagged; unexpected untag status %q for table %d", removed, result.Status, result.DatasetID)
			}
		}
		if len(failed) > 0 {
			return removed, qerrors.New(1, fmt.Sprintf("could not untag %d table(s): %s; %d table(s) were untagged and remain so; table kind was not deleted", len(failed), strings.Join(failed, ", "), removed))
		}
	}
	return removed, nil
}
