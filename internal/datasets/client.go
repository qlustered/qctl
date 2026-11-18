package datasets

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/prompt"
)

// Client handles HTTP requests for dataset operations
type Client struct {
	thirdParty     bool
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new datasets client
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

// Type aliases for generated types - use these directly
type (
	DatasetTiny             = api.DataSetTinySchema
	DatasetSmall            = api.DataSetSmallSchema
	DatasetFull             = api.DataSetSchemaFull
	DatasetsPage            = api.DatasetsListSchema
	DatasetStats            = api.DataSetMiniStatsSchema
	JobRunningCountRequest  = api.JobRunningCountRequestSchema
	JobRunningCountResponse = api.JobRunningCountResponseSchema
	DataSetState            = api.DataSetState
	DataLoadingProcess      = api.DataLoadingProcess
	MigrationPolicy         = api.MigrationPolicy
	CompressionType         = api.CompressionType
	UserInfoTiny            = api.DataSetUserInfoTinySchema
	SettingsSchema          = api.SettingsSchema
	TableSchemaDetail       = api.MigrationDetailResponse
	FieldSchemaDetail       = api.FieldDatabaseDetailDict
	FieldSchemaInfo         = api.FieldMigrationInfo
	SchemaRelativeState     = api.MigrationRelativeState
	DatasetRecoverResponse  = api.DatasetRecoverResponseSchema
	ShareDatasetsResult     = api.AddUserToDataSetsResponseSchema
)

// DatasetsListResponse is an alias for DatasetsPage (kept for backward compatibility)
type DatasetsListResponse = DatasetsPage

// DatasetStatsResponse is an alias for DatasetStats (kept for backward compatibility)
type DatasetStatsResponse = DatasetStats

// GetDatasetsParams holds parameters for listing datasets
type GetDatasetsParams struct {
	States          []string // slice header: 24 bytes
	ProblemDomainID *openapi_types.UUID
	SeriesID        *openapi_types.UUID
	DestinationID   *int
	DestinationName *string
	Name            *string
	SearchQuery     *string
	NeedsAttention  *bool
	OrderBy         string
	Page            int
	Limit           int // Chunk size per request
	Reverse         bool
}

// ResolveID resolves a user-provided name or integer ID string to a dataset ID.
// If the input is an integer, it's used as the ID directly.
// Otherwise, it's treated as a name and looked up via the list API.
func (c *Client) ResolveID(accessToken, input string) (int, error) {
	// Fast path: integer ID
	if id, err := strconv.Atoi(input); err == nil {
		return id, nil
	}

	resolved, err := c.ResolveByName(accessToken, input, "")
	if err != nil {
		return 0, err
	}

	return resolved.ID, nil
}

// ResolveByName resolves an exact table name and suggests nearby names on a miss.
// idFlag is the caller's by-ID flag, or empty for a positional selector.
func (c *Client) ResolveByName(accessToken, name, idFlag string) (*DatasetTiny, error) {
	if name == "" {
		return nil, qerrors.NewBadUsage("table identifier cannot be empty")
	}
	result, err := c.GetDatasets(accessToken, GetDatasetsParams{Name: &name, Limit: 2})
	if err != nil {
		if apierror.IsNoVisibleRows(err) {
			return nil, formatProviderDatasetNoMatchError(name, nil, c.organizationID.String(), idFlag, c.thirdParty)
		}
		return nil, fmt.Errorf("failed to look up table by name: %w", err)
	}
	resolved, err := resolveDatasetName(result.Results, name, idFlag)
	if resolved != nil || err != nil {
		return resolved, err
	}

	// Suggestions are advisory: a failed search leaves the exact-name miss intact.
	suggestions, err := c.GetDatasets(accessToken, GetDatasetsParams{SearchQuery: &name, Limit: 10})
	if err != nil {
		return nil, formatProviderDatasetNoMatchError(name, nil, c.organizationID.String(), idFlag, c.thirdParty)
	}
	return nil, formatProviderDatasetNoMatchError(name, suggestions.Results, c.organizationID.String(), idFlag, c.thirdParty)
}

// GetDatasets retrieves the list of datasets using the generated client
func (c *Client) GetDatasets(accessToken string, params GetDatasetsParams) (*DatasetsPage, error) {
	page, _, err := c.GetDatasetsWithBody(accessToken, params)
	return page, err
}

// GetDatasetsWithBody returns a table page and its original response body.
func (c *Client) GetDatasetsWithBody(accessToken string, params GetDatasetsParams) (*DatasetsPage, []byte, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Convert params to generated type
	apiParams := &api.GetDatasetsParams{
		ProblemDomainID: params.ProblemDomainID,
		SeriesID:        params.SeriesID,
		DestinationID:   params.DestinationID,
		DestinationName: params.DestinationName,
		Name:            params.Name,
		SearchQuery:     params.SearchQuery,
		NeedsAttention:  params.NeedsAttention,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.DataSetsOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	// Convert states to generated type
	if len(params.States) > 0 {
		states := make([]api.DataSetState, len(params.States))
		for i, s := range params.States {
			states[i] = api.DataSetState(s)
		}
		apiParams.States = &states
	}

	var resp *api.GetDatasetsResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.GetThirdPartyDatasets(context.Background(), c.organizationID, &api.GetThirdPartyDatasetsParams{
			Page:            apiParams.Page,
			Limit:           apiParams.Limit,
			Name:            apiParams.Name,
			SearchQuery:     apiParams.SearchQuery,
			States:          apiParams.States,
			OrderBy:         apiParams.OrderBy,
			Reverse:         apiParams.Reverse,
			ProblemDomainID: apiParams.ProblemDomainID,
			SeriesID:        apiParams.SeriesID,
			DestinationID:   apiParams.DestinationID,
			DestinationName: apiParams.DestinationName,
			NeedsAttention:  apiParams.NeedsAttention,
		})
		err = requestErr
		if err == nil {
			resp, err = api.ParseGetDatasetsResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.GetDatasetsWithResponse(context.Background(), c.organizationID, apiParams)
	}
	if err != nil {
		return nil, nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, resp.Body, nil
}

// GetDataset retrieves a single dataset by ID. Numbers in its interface{}
// fields decode as jsonnum.Unmarshal decodes them.
func (c *Client) GetDataset(accessToken string, datasetID int) (*DatasetFull, error) {
	return c.GetDatasetContext(context.Background(), accessToken, datasetID)
}

// GetDatasetContext is GetDataset under ctx.
func (c *Client) GetDatasetContext(ctx context.Context, accessToken string, datasetID int) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetWithResponse(ctx, c.organizationID, datasetID, &api.GetDatasetParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	var dataset DatasetFull
	if err := jsonnum.Unmarshal(resp.Body, &dataset); err != nil {
		return nil, fmt.Errorf("failed to decode table response: %w", err)
	}
	return &dataset, nil
}

// GetThirdPartyDataset retrieves a table shared from another organization.
func (c *Client) GetThirdPartyDataset(accessToken string, datasetID int) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetThirdPartyDatasetWithResponse(context.Background(), c.organizationID, datasetID, &api.GetThirdPartyDatasetParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	var dataset DatasetFull
	if err := jsonnum.Unmarshal(resp.Body, &dataset); err != nil {
		return nil, fmt.Errorf("failed to decode table response: %w", err)
	}
	return &dataset, nil
}

// GetDatasetSmall retrieves the narrow dataset view.
//
// It is fetched for exactly one field: DataSetSmallSchema.Users, "Users that
// have access to this dataset". DataSetSchemaFull -- what GetDataset returns
// -- has no users field, so this is the only per-table read-back for sharing.
// (The list rows carry the same data on DataSetTinySchema.Users, but callers
// holding an integer id never fetch a list row: ResolveID short-circuits.)
//
// Like the detail route, this answers 404 for a deleted table.
func (c *Client) GetDatasetSmall(accessToken string, datasetID int) (*DatasetSmall, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetSmallWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetDatasetSmallParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// ResolveDeletedByName finds a soft-deleted table by exact name.
//
// It deliberately does not go through GetDataset. The detail route answers 404
// for a deleted table (atlas capi/capi/views/settings_view_core.py) and so
// does .../small (settings_view.py), so the list is the only view of one.
// The list's `name` filter is exact equality (settings_view.py), but the
// active/disabled-only unique index does not cover deleted rows. A name can
// therefore match several deleted tables. Limit 2 is enough to distinguish a
// unique match from ambiguity without fetching an unbounded duplicate set.
// The states filter must be set explicitly: the server defaults it to
// [active, disabled] (settings_view.py), which can never match a deleted
// row.
//
// There is no id equivalent of this method: the list has no id filter, only
// start/end/page. Callers holding an integer pass it to RecoverDataset
// directly and let the server answer.
func (c *Client) ResolveDeletedByName(accessToken, name string, yes bool) (*DatasetTiny, error) {
	result, err := c.GetDatasets(accessToken, GetDatasetsParams{
		Name:   &name,
		States: []string{string(api.DataSetStateDeleted)},
		Limit:  2,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to look up deleted table by name: %w", err)
	}
	// Re-assert client-side what the two filters were supposed to guarantee,
	// the way ResolveDataset re-checks the name against the listed rows
	// (resolver.go) instead of trusting the server's filter. Both
	// filters hold today -- `name` is exact equality and `states` is sent
	// explicitly -- so the second branch is unreachable. It is here because
	// the failure it guards is silent and irreversible in the wrong
	// direction: if `name` ever became a prefix or fuzzy match,
	// `undelete table orders` would recover some *other* deleted table and
	// report success.
	if len(result.Results) == 0 {
		return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
			"no deleted table named %q\n\n"+
				"Deleted tables are invisible to 'qctl get table' and 'qctl describe table' (both 404).\n"+
				"Run 'qctl get tables --state deleted' to see what can be recovered.", name)))
	}

	ids := make([]string, 0, len(result.Results))
	for _, dataset := range result.Results {
		if dataset.Name != name || dataset.State != api.DataSetStateDeleted {
			return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
				"no deleted table named %q\n\n"+
					"Deleted tables are invisible to 'qctl get table' and 'qctl describe table' (both 404).\n"+
					"Run 'qctl get tables --state deleted' to see what can be recovered.", name)))
		}
		ids = append(ids, strconv.Itoa(dataset.ID))
	}
	if len(result.Results) > 1 {
		return nil, qerrors.NewBadUsagef(
			"deleted table name %q is ambiguous; matches IDs: %s\n\nRun 'qctl undelete table <id>%s' with one of those IDs.",
			name, strings.Join(ids, ", "), prompt.YesFlag(yes))
	}
	return &result.Results[0], nil
}

// RecoverDataset restores a soft-deleted table.
//
// newName is optional and is sent only when non-empty: the server treats a
// present new_name as the name to restore under, so sending "" would be a
// rename request rather than an omission.
func (c *Client) RecoverDataset(accessToken string, datasetID int, newName string) (*DatasetRecoverResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	body := api.RecoverDatasetJSONRequestBody{}
	if newName != "" {
		body.NewName = &newName
	}

	resp, err := apiClient.API.RecoverDatasetWithResponse(
		context.Background(), c.organizationID, datasetID, &api.RecoverDatasetParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDatasetStats retrieves statistics for a dataset using the generated client
func (c *Client) GetDatasetStats(accessToken string, datasetID int) (*DatasetStats, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetStatsWithResponse(context.Background(), c.organizationID, datasetID, &api.GetDatasetStatsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDatasetJobActivity retrieves job activity for a dataset using the generated client
func (c *Client) GetDatasetJobActivity(accessToken string, datasetID int, lastUpdate *int) (*JobRunningCountResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.JobRunningCountRequestSchema{
		LastUpdate: lastUpdate,
	}

	resp, err := apiClient.API.GetDatasetJobActivityWithResponse(
		context.Background(),
		c.organizationID,
		datasetID,
		&api.GetDatasetJobActivityParams{},
		reqBody,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetJobActivity retrieves job activity globally using the generated client
func (c *Client) GetJobActivity(accessToken string, lastUpdate *int) (*JobRunningCountResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.JobRunningCountRequestSchema{
		LastUpdate: lastUpdate,
	}

	resp, err := apiClient.API.GetJobActivityWithResponse(
		context.Background(),
		c.organizationID,
		&api.GetJobActivityParams{},
		reqBody,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetSchemaDetails retrieves the latest schema state for a dataset: field
// definitions, draft-vs-current status, and per-field diffs.
func (c *Client) GetSchemaDetails(accessToken string, datasetID int) (*TableSchemaDetail, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetMigrationDetailsWithResponse(context.Background(), c.organizationID, datasetID, &api.GetDatasetMigrationDetailsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetSchemaFields retrieves the schema's column names. Server-side cached, and
// the union of the current (applied) and draft field names — nothing in the
// response marks which are pending, so a caller that must tell them apart needs
// GetSchemaDetails.
func (c *Client) GetSchemaFields(accessToken string, datasetID int) ([]string, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetMigrationFieldsWithResponse(context.Background(), c.organizationID, datasetID, &api.GetDatasetMigrationFieldsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200.Fields, nil
}

// ShareDatasetsRequest selects a recipient, tables and grant level.
type ShareDatasetsRequest struct {
	Email           string
	UserID          string
	DatasetIDs      []int
	PermissionLevel api.DataSetPermission
}

// ShareDatasets grants access through the bulk route, preserving existing grants.
func (c *Client) ShareDatasets(accessToken string, req ShareDatasetsRequest) (*ShareDatasetsResult, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	level := req.PermissionLevel
	if level == "" {
		level = api.DataSetPermissionReadOnly
	}
	body := api.AddUserToDatasetsJSONRequestBody{DatasetIds: req.DatasetIDs, PermissionLevel: &level}
	if req.Email != "" {
		email := openapi_types.Email(req.Email)
		body.Email = &email
	}
	if req.UserID != "" {
		parsed, parseErr := uuid.Parse(req.UserID)
		if parseErr != nil {
			return nil, qerrors.NewBadUsagef("invalid user id %q: must be a UUID", req.UserID)
		}
		body.UserID = &parsed
	}

	resp, err := apiClient.API.AddUserToDatasetsWithResponse(
		context.Background(), c.organizationID, &api.AddUserToDatasetsParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	switch resp.StatusCode() {
	case http.StatusOK:
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		return resp.JSON200, nil
	case http.StatusMultiStatus:
		if resp.JSON207 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		return resp.JSON207, nil
	default:
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
}
