package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
)

// SignOffResult is the signed-off run and the original response body.
type SignOffResult struct {
	Run  *Run
	Body json.RawMessage
}

// SignOff requests approval of run runID with expectedAttemptID.
func (c *Client) SignOff(token string, runID, expectedAttemptID int) (*SignOffResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	body := api.SignOffRequest{ExpectedAttemptID: &expectedAttemptID}
	resp, err := apiClient.API.SignOffDeliverableRunWithResponse(context.Background(), c.orgID, runID, &api.SignOffDeliverableRunParams{}, body)
	if err != nil {
		return nil, signOffError(wrapAPIError(apiClient, err, "sign off deliverable run failed", "sign_off_deliverable_run"), runID)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, signOffError(handleError(resp.StatusCode(), resp.Body, "sign off deliverable run failed", "sign_off_deliverable_run"), runID)
	}
	var run Run
	if err := jsonnum.Unmarshal(resp.Body, &run); err != nil {
		return nil, fmt.Errorf("decode signed-off deliverable run: %w", err)
	}
	return &SignOffResult{Run: &run, Body: resp.Body}, nil
}

func signOffError(err error, runID int) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w\n\nEditor and agent roles cannot sign off; ask a member, admin or ops user to review this run", err)
	}
	switch apiErr.Code {
	case "STALE_ATTEMPT":
		return fmt.Errorf("%w\n\nRe-read the run and review its current render before retrying: qctl describe deliverable-run %d", err, runID)
	case "SIGNOFF_BLOCKED":
		var counts string
		if len(apiErr.Context) > 0 {
			if body, encodeErr := json.MarshalIndent(apiErr.Context, "", "  "); encodeErr == nil {
				counts = "\n\nSign-off refusal context:\n" + terminal.Sanitize(string(body))
			}
		}
		return fmt.Errorf("%w%s\n\nNext: qctl deliverable-run readiness %d", err, counts, runID)
	}
	return err
}

// FormatSignOffPreview renders the attempt, parameters, totals and readiness to review.
func FormatSignOffPreview(run *Run, readiness *ReadinessSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sign off deliverable-run/%d (issue #%d, %s)\n", run.ID, run.Sequence, cellText(RevisionModeText(run.RevisionMode)))
	fmt.Fprintf(&b, "Revision: %s\nRender attempt: %d\n", run.RevisionID, run.AttemptID)
	if run.RenderedAt != nil {
		fmt.Fprintf(&b, "Rendered: %s\n", run.RenderedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "Params: %s\n", cellText(RunParamsText(run.RunParamsTyped, run.RunParams)))
	if run.ComputedTotals == nil || len(*run.ComputedTotals) == 0 {
		b.WriteString("Totals (last render): none recorded\n")
	} else {
		writeRunTotals(&b, run.ComputedTotals)
	}
	leftOut := readiness.WaivedWithoutSheet
	for _, sheet := range readiness.Sheets {
		leftOut += sheet.Waived
	}
	for _, carried := range readiness.UnboundCarried {
		leftOut += carried.Waived
	}
	fmt.Fprintf(&b, "\nLeft out (waived): %d\n\n", leftOut)
	b.WriteString(FormatReadinessSummary(readiness))
	b.WriteString("\nSigning off locks this report.\n")
	return b.String()
}
