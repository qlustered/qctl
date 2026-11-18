package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// CreateRunRequest is a run to create.
type CreateRunRequest struct {
	// Ref names the deliverable in the hints of a refusal, shell-quoted there;
	// "" names it by its UUID.
	Ref                string
	Params             map[string]api.RunParameterValue
	AllowEmpty         bool
	UseEnabledRevision bool
}

// CreateRunResult is a created run and its original response body.
type CreateRunResult struct {
	Run  *Run
	Body []byte
}

// createRunBody is the create-run request body: run_params_typed always, and
// each flag only when true.
type createRunBody struct {
	RunParamsTyped     map[string]api.RunParameterValue `json:"run_params_typed"`
	AllowEmpty         bool                             `json:"allow_empty,omitempty"`
	UseEnabledRevision bool                             `json:"use_enabled_revision,omitempty"`
}

// createRunFailed prefixes the text of CreateRun's API errors.
const createRunFailed = "create deliverable run failed"

// CreateRun creates a run of a deliverable. Numbers in the run's interface{}
// fields decode as jsonnum.Unmarshal decodes them. A 409 refusal coded
// NEW_PERIOD_IN_FLIGHT, REISSUE_NOT_LATEST or NO_ENABLED_REVISION ends with a
// hint naming the command to run next.
func (c *Client) CreateRun(token string, deliverableID uuid.UUID, req CreateRunRequest) (*CreateRunResult, error) {
	params := req.Params
	if params == nil {
		params = map[string]api.RunParameterValue{}
	}
	payload, err := json.Marshal(createRunBody{
		RunParamsTyped: params, AllowEmpty: req.AllowEmpty, UseEnabledRevision: req.UseEnabledRevision,
	})
	if err != nil {
		return nil, fmt.Errorf("encode deliverable run request: %w", err)
	}
	ref := req.Ref
	if ref == "" {
		ref = deliverableID.String()
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableRunWithBodyWithResponse(context.Background(), c.orgID, deliverableID,
		&api.CreateDeliverableRunParams{}, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, createRunError(wrapAPIError(apiClient, err, createRunFailed, "create_deliverable_run"), ref)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, createRunError(handleError(resp.StatusCode(), resp.Body, createRunFailed, "create_deliverable_run"), ref)
	}
	var run Run
	if err := jsonnum.Unmarshal(resp.Body, &run); err != nil {
		return nil, fmt.Errorf("decode deliverable run: %w", err)
	}
	return &CreateRunResult{Run: &run, Body: resp.Body}, nil
}

// createRunError returns err followed by a hint naming ref, shell-quoted, when
// its *APIError is a 409 with a code that has one, and unchanged otherwise.
func createRunError(err error, ref string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		return err
	}
	ref = cmdutil.ShellArg(ref)
	var hint string
	switch apiErr.Code {
	case "NEW_PERIOD_IN_FLIGHT":
		hint = "A new-period run is refused while another new-period run is open, rendered, stale or signed_off, " +
			"and a re-issue while a new-period run is signed_off.\n" +
			fmt.Sprintf("List them with: qctl get deliverable-runs %s --state open (likewise rendered, stale, signed_off)", ref)
	case "REISSUE_NOT_LATEST":
		hint = "Only the latest delivered period can be re-issued.\n" +
			fmt.Sprintf("List the delivered runs with: qctl get deliverable-runs %s --state delivered", ref)
	case "NO_ENABLED_REVISION":
		hint = fmt.Sprintf("Enable a revision first: qctl enable deliverable %s (its only draft), or choose one with --release <release>", ref)
	default:
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, hint)
}

// FormatCreatedRun renders a created run: its id, issue number, revision mode,
// release, parameters and the describe command to run next. A nil revision
// names the release by its revision id.
func FormatCreatedRun(run *Run, revision *RevisionDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "deliverable-run/%d created\n", run.ID)
	line := func(label, value string) { fmt.Fprintf(&b, "  %-7s : %s\n", label, value) }
	line("issue", fmt.Sprintf("#%d", run.Sequence))
	line("mode", cellText(RevisionModeText(run.RevisionMode)))
	line("release", runReleaseText(revision, run.RevisionID))
	line("params", cellText(RunParamsText(run.RunParamsTyped, run.RunParams)))
	fmt.Fprintf(&b, "Next: qctl describe deliverable-run %d\n", run.ID)
	return b.String()
}
