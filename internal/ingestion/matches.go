package ingestion

import (
	"fmt"
	"slices"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// MatchingJobIDs returns the ID of every job params matches, reading every
// page. A listed job whose kind is outside params.Kinds, or a server that
// lists jobs for stored_item_id 0 when params.StoredItemID is set, is an
// API-incompatible error.
func (c *Client) MatchingJobIDs(accessToken string, params GetIngestionJobsParams) ([]int, error) {
	guard := filterGuard{client: c, token: accessToken, params: params}
	var ids []int
	params.Limit = 100
	// Atlas's ordered (OFFSET) pagination reports total_rows but not next. Use
	// the unique primary key so page boundaries cannot repeat or skip jobs.
	params.OrderBy = "id"

	for page := 1; ; page++ {
		params.Page = page
		resp, err := c.GetIngestionJobs(accessToken, params)
		if err != nil {
			if len(ids) > 0 {
				return ids, fmt.Errorf("partial results (got %d): %w", len(ids), err)
			}
			return nil, fmt.Errorf("failed to fetch jobs matching filter: %w", err)
		}
		if err := guard.check(resp.Results); err != nil {
			return nil, err
		}

		for _, job := range resp.Results {
			ids = append(ids, job.ID)
		}

		if resp.TotalRows != nil {
			if len(ids) >= *resp.TotalRows || len(resp.Results) == 0 {
				break
			}
		} else if resp.Next == nil || len(resp.Results) == 0 {
			break
		}
	}

	return ids, nil
}

// filterGuard checks listed jobs against the kind and file keys of params.
type filterGuard struct {
	client      *Client
	token       string
	params      GetIngestionJobsParams
	fileHonored bool
}

// check returns an API-incompatible error for the first job in jobs that
// shows the server ignored params.Kinds or params.StoredItemID.
func (g *filterGuard) check(jobs []IngestionJobTiny) error {
	for _, job := range jobs {
		if len(g.params.Kinds) > 0 && !slices.Contains(g.params.Kinds, job.Kind) {
			evidence := fmt.Sprintf("it listed job %d, a %s job", job.ID, job.Kind)
			if job.Kind == "" {
				evidence = fmt.Sprintf("it listed job %d with no kind", job.ID)
			}
			return ignoredFilterError("kind", evidence)
		}
		if err := g.checkFile(job); err != nil {
			return err
		}
	}
	return nil
}

// checkFile accepts a job on params.StoredItemID. A job on another stored
// item, which may be one of its parts, is accepted once a listing for
// stored_item_id 0 comes back empty.
func (g *filterGuard) checkFile(job IngestionJobTiny) error {
	want := g.params.StoredItemID
	if want == nil || g.fileHonored || (job.StoredItemID != nil && *job.StoredItemID == *want) {
		return nil
	}
	noFile := 0
	resp, err := g.client.GetIngestionJobs(g.token, GetIngestionJobsParams{StoredItemID: &noFile, Limit: 1})
	if err != nil {
		return fmt.Errorf("failed to check that the server filters jobs by file: %w", err)
	}
	if len(resp.Results) > 0 {
		return ignoredFilterError("file", fmt.Sprintf("asked for the jobs of file 0, which does not exist, it listed job %d", resp.Results[0].ID))
	}
	g.fileHonored = true
	return nil
}

// ignoredFilterError is the API-incompatible error for a filter key the
// server ignored.
func ignoredFilterError(key, evidence string) error {
	return qerrors.NewAPIIncompatible(fmt.Sprintf(
		"the server ignored the filter's %s (%s), so the filter would match jobs outside it; "+
			"this server predates filtering ingestion jobs by kind and file. Name the jobs by ID instead",
		key, evidence))
}
