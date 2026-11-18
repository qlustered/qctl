package datasets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func (c *Client) prepareConceptAssignments(token string, existing *DatasetFull, spec TableSpec) (*api.DatasetProblemDomainsPutRequestSchema, *api.DatasetColumnConceptsPutRequestSchema, []TableFieldChange, error) {
	kindsBody, kindChange, err := c.prepareTableKinds(token, existing, spec.TableKinds)
	if err != nil {
		return nil, nil, nil, err
	}
	conceptsBody, conceptChange, err := c.prepareColumnConcepts(token, existing, spec.ColumnStandardFields)
	if err != nil {
		return nil, nil, nil, err
	}
	var changes []TableFieldChange
	if kindChange != nil {
		changes = append(changes, *kindChange)
	}
	if conceptChange != nil {
		changes = append(changes, *conceptChange)
	}
	return kindsBody, conceptsBody, changes, nil
}

func (c *Client) prepareTableKinds(token string, existing *DatasetFull, desired *[]string) (*api.DatasetProblemDomainsPutRequestSchema, *TableFieldChange, error) {
	if desired == nil {
		return nil, nil, nil
	}
	old := make([]string, 0)
	if existing != nil && existing.ProblemDomains != nil {
		for _, item := range *existing.ProblemDomains {
			old = append(old, item.Slug)
		}
	}
	want := append([]string{}, (*desired)...)
	sort.Strings(old)
	sort.Strings(want)
	want = slices.Compact(want)
	if existing != nil && slices.Equal(old, want) {
		return nil, nil, nil
	}
	change := assignmentChange("spec.table_kinds", old, want, existing != nil)
	if existing == nil && len(want) == 0 {
		return nil, &change, nil
	}
	body := &api.DatasetProblemDomainsPutRequestSchema{ProblemDomainIds: make([]uuid.UUID, 0, len(want))}
	resolver := dataset_kinds.NewClient(c.baseURL, c.organizationID.String(), c.verbosity)
	for _, slug := range want {
		kind, err := resolver.BySlug(token, slug)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve table kind %q: %w", slug, err)
		}
		if kind == nil {
			return nil, nil, qerrors.NewBadUsagef("unknown table kind slug %q", slug)
		}
		body.ProblemDomainIds = append(body.ProblemDomainIds, kind.ID)
	}
	return body, &change, nil
}

func (c *Client) prepareColumnConcepts(token string, existing *DatasetFull, desired *map[string]string) (*api.DatasetColumnConceptsPutRequestSchema, *TableFieldChange, error) {
	if desired == nil {
		return nil, nil, nil
	}
	old := make(map[string]string)
	if existing != nil && existing.ColumnConcepts != nil {
		for _, item := range *existing.ColumnConcepts {
			old[item.DatasetCol] = item.Concept.Slug
		}
	}
	want := make(map[string]string, len(*desired))
	for col, slug := range *desired {
		want[col] = slug
	}
	if existing != nil && reflect.DeepEqual(old, want) {
		return nil, nil, nil
	}
	body, err := c.columnConceptsBody(token, old, want)
	if err != nil {
		return nil, nil, err
	}
	change := assignmentChange("spec.column_standard_fields", old, want, existing != nil)
	if len(body.ColumnConcepts) == 0 {
		return nil, &change, nil
	}
	return body, &change, nil
}

func (c *Client) columnConceptsBody(token string, old, want map[string]string) (*api.DatasetColumnConceptsPutRequestSchema, error) {
	body := &api.DatasetColumnConceptsPutRequestSchema{ColumnConcepts: make([]api.DatasetColumnConceptAssignmentSchema, 0)}
	columns := make([]string, 0, len(old)+len(want))
	for col := range old {
		columns = append(columns, col)
	}
	for col := range want {
		if _, present := old[col]; !present {
			columns = append(columns, col)
		}
	}
	sort.Strings(columns)
	resolver := concepts.NewClient(c.baseURL, c.organizationID.String(), c.verbosity)
	for _, col := range columns {
		slug, present := want[col]
		if present && old[col] == slug {
			continue
		}
		entry := api.DatasetColumnConceptAssignmentSchema{DatasetCol: col}
		if present {
			concept, err := resolver.BySlug(token, slug)
			if err != nil {
				return nil, fmt.Errorf("resolve standard field %q for column %q: %w", slug, col, err)
			}
			if concept == nil {
				return nil, qerrors.NewBadUsagef("unknown standard field slug %q for column %q", slug, col)
			}
			entry.ConceptID = &concept.ID
		}
		body.ColumnConcepts = append(body.ColumnConcepts, entry)
	}
	return body, nil
}

func assignmentChange(path string, old, next any, oldExists bool) TableFieldChange {
	oldJSON, _ := json.Marshal(old)
	nextJSON, _ := json.Marshal(next)
	return newTableFieldChange(path, oldJSON, oldExists, nextJSON, true)
}

func (c *Client) putTableKinds(token string, id int, body api.DatasetProblemDomainsPutRequestSchema) error {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity})
	if err != nil {
		return err
	}
	resp, err := apiClient.API.PutDatasetProblemDomainsWithResponse(context.Background(), c.organizationID, id, &api.PutDatasetProblemDomainsParams{}, body)
	if err != nil {
		return wrapAPIError(apiClient, err, "set table kinds")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "set table kinds")
	}
	return nil
}

func (c *Client) putColumnConcepts(token string, id int, body api.DatasetColumnConceptsPutRequestSchema) error {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity})
	if err != nil {
		return err
	}
	resp, err := apiClient.API.PutDatasetColumnConceptsWithResponse(context.Background(), c.organizationID, id, &api.PutDatasetColumnConceptsParams{}, body)
	if err != nil {
		return wrapAPIError(apiClient, err, "set column standard fields")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "set column standard fields")
	}
	return nil
}
