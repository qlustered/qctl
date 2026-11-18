// Package history reads the organization's change ledger: who changed what,
// when, on which table, and whether it has since been undone.
//
// It covers both halves of the ledger: reading it (GetHistory, behind
// `qctl get history`) and walking it backwards or forwards one object at a
// time (Step/Act/RunFlow, behind `qctl undo` and `qctl redo`).
package history

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Type aliases for generated types - use these directly.
type (
	Item       = api.HistoryTinySchema
	Page       = api.HistoryListSchema
	UserInfo   = api.UserInfoTinySchema
	Model      = api.ModelsWithHistory
	Operation  = api.SimpleHistoryAction
	UndoStatus = api.HistoryUndoStatus
	OrderBy    = api.HistoryOrderBy
)

// Client handles HTTP requests for history operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new history client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

// GetHistoryParams holds parameters for listing history items.
//
// There is no Start/End: the endpoint carries a second, id-cursor pagination
// scheme alongside page/limit, no qctl get command uses one, and the backend
// ignores both whenever page and order_by are present.
type GetHistoryParams struct {
	// Filtering (pointers first for alignment)
	DatasetID     *int
	ModelID       *int
	Model         *Model
	FieldName     *string
	Value         *string
	SearchQuery   *string
	WhodunitID    *openapi_types.UUID
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	UndoStatus    *UndoStatus
	Operation     []Operation

	// Pagination and sorting
	OrderBy string
	Page    int
	Limit   int
	Reverse bool
}

// GetHistory retrieves a page of history items using the generated client.
func (c *Client) GetHistory(accessToken string, params GetHistoryParams) (*Page, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetHistoryItemsParams{
		DatasetID:     params.DatasetID,
		Model:         params.Model,
		FieldName:     params.FieldName,
		Value:         params.Value,
		SearchQuery:   params.SearchQuery,
		WhodunitID:    params.WhodunitID,
		CreatedAfter:  params.CreatedAfter,
		CreatedBefore: params.CreatedBefore,
		UndoStatus:    params.UndoStatus,
	}

	if len(params.Operation) > 0 {
		ops := params.Operation
		apiParams.Operation = &ops
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := OrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	// Not a copy-paste bug: this endpoint declares `reverse: bool | None =
	// Query(True)` (get_history_items, atlas capi/capi/views/history_view.py),
	// so the listing is newest-first before qctl says anything. --reverse
	// therefore asks for the OPPOSITE of the server default, and its absence
	// must send nothing. Same idiom as internal/rule_versions/client.go, where
	// it is documented.
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetHistoryItemsWithResponse(context.Background(), c.organizationID, apiParams, func(_ context.Context, req *http.Request) error {
		if params.ModelID != nil {
			query := req.URL.Query()
			query.Set("model_id", strconv.Itoa(*params.ModelID))
			req.URL.RawQuery = query.Encode()
		}
		return nil
	})
	if err != nil {
		// Not a bare wrap: HandleError rebuilds the error from the transport's
		// captured body when the generated parser choked on it -- the 422
		// object-vs-list envelope mismatch -- which is what keeps the exit code
		// an unbroken Unwrap chain instead of collapsing to 1.
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
