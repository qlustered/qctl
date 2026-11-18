package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/row_filters"
)

// readinessTimeout is the deadline Readiness gives its request before
// client.ResolveTimeout applies --request-timeout.
const readinessTimeout = 45 * time.Second

// ReadinessBuckets are the readiness row buckets, in the server's order.
var ReadinessBuckets = []string{
	"included", "carried_forward", "blocked_awaiting_review", "blocked_failing_rules",
	"blocked_indeterminate", "waived", "carried_unbound",
}

var readinessBucketLabels = map[string]string{
	"included":                "Included",
	"carried_forward":         "Carried forward",
	"blocked_awaiting_review": "Needs review",
	"blocked_failing_rules":   "Failing rules",
	"blocked_indeterminate":   "Unreadable value",
	"waived":                  "Left out",
	"carried_unbound":         "Carried, sheet not in this setup",
}

// ReadinessBucketLabel returns the web app's words for bucket, or bucket
// itself when it has none.
func ReadinessBucketLabel(bucket string) string {
	if label, ok := readinessBucketLabels[bucket]; ok {
		return label
	}
	return bucket
}

// ReadinessParams selects a run's readiness rows. A zero value sends nothing.
type ReadinessParams struct {
	Bucket string
	Page   int
	Limit  int
}

func (p ReadinessParams) apiParams() *api.ReadDeliverableRunReadinessParams {
	out := &api.ReadDeliverableRunReadinessParams{}
	if p.Bucket != "" {
		bucket := api.ReadDeliverableRunReadinessParamsBucket(p.Bucket)
		out.Bucket = &bucket
	}
	if p.Page > 0 {
		out.Page = &p.Page
	}
	if p.Limit > 0 {
		out.Limit = &p.Limit
	}
	return out
}

// SheetReadiness is one sheet's readiness counts and row filter report.
// RowFilterReport is nil when the server sends null.
type SheetReadiness struct {
	SheetBindingID        uuid.UUID           `json:"sheet_binding_id"`
	SheetName             string              `json:"sheet_name"`
	MemberDatasetID       int                 `json:"member_dataset_id"`
	Included              int                 `json:"included"`
	CarriedForward        int                 `json:"carried_forward"`
	BlockedAwaitingReview int                 `json:"blocked_awaiting_review"`
	BlockedFailingRules   int                 `json:"blocked_failing_rules"`
	BlockedIndeterminate  int                 `json:"blocked_indeterminate"`
	Waived                int                 `json:"waived"`
	OutOfScope            int                 `json:"out_of_scope"`
	RowFilterReport       *row_filters.Report `json:"row_filter_report"`
}

// WaivableRow identifies a row the server permits this run to waive.
type WaivableRow = api.WaivableRowItem

// ReadinessIssue is a readiness error with an optional verified waiver target.
type ReadinessIssue struct {
	Issue
	WaivableRow *WaivableRow `json:"waivable_row,omitempty"`
}

func (i ReadinessIssue) waivableCarriedRow() *WaivableRow {
	if i.BindingKind != "carried_rows" || i.WaivableRow == nil ||
		i.WaivableRow.MemberDatasetID <= 0 || i.WaivableRow.RowID <= 0 {
		return nil
	}
	return i.WaivableRow
}

// ReadinessSummary is a run's readiness without its rows page and sources.
type ReadinessSummary struct {
	RunID              int                        `json:"run_id"`
	State              api.DeliverableRunState    `json:"state"`
	IsLive             bool                       `json:"is_live"`
	IsSignoffBlocked   bool                       `json:"is_signoff_blocked"`
	Note               string                     `json:"note"`
	Sheets             []SheetReadiness           `json:"sheets"`
	Errors             []ReadinessIssue           `json:"errors"`
	Warnings           []Issue                    `json:"warnings"`
	UnboundCarried     []api.UnboundCarriedCounts `json:"unbound_carried"`
	WaivedWithoutSheet int                        `json:"waived_without_sheet"`
}

func (s *ReadinessSummary) canChangeWaivers() bool {
	if !s.IsLive {
		return false
	}
	switch s.State {
	case api.DeliverableRunStateOpen, api.DeliverableRunStateRendered, api.DeliverableRunStateStale:
		return true
	}
	return false
}

// Readiness is a run's readiness as the server sends it.
type Readiness struct {
	ReadinessSummary
	Rows       []api.ReadinessRowItem  `json:"rows"`
	RowsBucket *string                 `json:"rows_bucket"`
	RowsPage   int                     `json:"rows_page"`
	RowsLimit  int                     `json:"rows_limit"`
	RowsTotal  int                     `json:"rows_total"`
	Sources    []api.RunSourceResponse `json:"sources"`
}

// ReadinessResult is a run's readiness and the original response body.
type ReadinessResult struct {
	*Readiness
	Body json.RawMessage `json:"-"`
}

// Readiness reads a run's readiness and one page of its rows.
func (c *Client) Readiness(token string, runID int, params ReadinessParams) (*ReadinessResult, error) {
	apiClient, err := c.newAPIClientWithin(token, readinessTimeout)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReadDeliverableRunReadinessWithResponse(context.Background(), c.orgID, runID, params.apiParams())
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "read deliverable run readiness failed", "read_deliverable_run_readiness")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "read deliverable run readiness failed", "read_deliverable_run_readiness")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run readiness response")
	}
	var readiness Readiness
	if err := jsonnum.Unmarshal(resp.Body, &readiness); err != nil {
		return nil, fmt.Errorf("decode deliverable run readiness: %w", err)
	}
	return &ReadinessResult{Readiness: &readiness, Body: resp.Body}, nil
}
