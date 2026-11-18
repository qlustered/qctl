package deliverables

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Issue is one error or warning of a revision's validation.
type Issue = api.BindingIssue

// FormatIssue renders an issue as "<loc>: <msg>", with loc joined by dots, or
// as msg alone when loc is empty.
func FormatIssue(issue Issue) string {
	if issue.Loc == nil || len(*issue.Loc) == 0 {
		return issue.Msg
	}
	return strings.Join(*issue.Loc, ".") + ": " + issue.Msg
}

// ValidateResult is a revision's validation and its original response body.
type ValidateResult struct {
	*api.RevisionValidateResponse
	Body json.RawMessage `json:"-"`
}

// Validate reads a revision's validation errors and warnings. An invalid
// revision is a result with Valid false, not an error.
func (c *Client) Validate(token string, revisionID uuid.UUID) (*ValidateResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ValidateDeliverableRevisionWithResponse(context.Background(), c.orgID, revisionID, &api.ValidateDeliverableRevisionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "validate deliverable revision failed", "validate_deliverable_revision")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "validate deliverable revision failed", "validate_deliverable_revision")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable revision validation response")
	}
	return &ValidateResult{RevisionValidateResponse: resp.JSON200, Body: resp.Body}, nil
}

// Enable enables a revision and returns its binding tree. A 422
// REVISION_INVALID refusal is a *RevisionInvalidError, and a 409 that lists
// runs is a *RunsBlockedError; each unwraps to the *APIError.
func (c *Client) Enable(token string, revisionID uuid.UUID) (*RevisionDetail, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.EnableDeliverableRevisionWithResponse(context.Background(), c.orgID, revisionID, &api.EnableDeliverableRevisionParams{})
	if err != nil {
		return nil, enableError(wrapAPIError(apiClient, err, enableFailed, "enable_deliverable_revision"))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, enableError(handleError(resp.StatusCode(), resp.Body, enableFailed, "enable_deliverable_revision"))
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty enable deliverable revision response")
	}
	tree, err := decodeBinding[api.DeliverableRevisionResponse](resp.Body)
	if err != nil {
		return nil, err
	}
	return &RevisionDetail{DeliverableRevisionResponse: tree, Body: resp.Body}, nil
}

// RevisionInvalidError is a revision action refused with REVISION_INVALID, or a
// binding write refused with BINDING_INVALID. Its text is a first line followed
// by one "  - " line per issue. The first line is the refusal's text for
// REVISION_INVALID, and "<action> failed: <n> binding issue(s):" for
// BINDING_INVALID.
type RevisionInvalidError struct {
	Issues      []Issue
	failed      string // the BINDING_INVALID first line's prefix; "" for REVISION_INVALID
	err         error
	formatIssue func(Issue) string
}

func (e *RevisionInvalidError) Error() string {
	format := e.formatIssue
	if format == nil {
		format = FormatIssue
	}
	lines := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		lines[i] = "  - " + format(issue)
	}
	head := e.err.Error()
	if e.failed != "" {
		head = fmt.Sprintf("%s: %d binding issue(s):", e.failed, len(e.Issues))
	}
	return head + "\n" + strings.Join(lines, "\n")
}

// Unwrap returns the refusal.
func (e *RevisionInvalidError) Unwrap() error { return e.err }

// RunProblem is one reason a run could not move to the revision being enabled.
type RunProblem struct {
	RunID  int    `json:"run_id"`
	Reason string `json:"reason"`
}

// enableFailed prefixes the text of Enable's API errors.
const enableFailed = "enable deliverable revision failed"

// RunsBlockedError is an enable refused with a 409 that lists runs. Its text
// counts the problems, followed by one "  - run <id>: <reason>" line each.
type RunsBlockedError struct {
	Runs []RunProblem
	err  error
}

// Error leaves out the refusal's own text, which joins the same reasons on one
// line.
func (e *RunsBlockedError) Error() string {
	lines := make([]string, len(e.Runs))
	for i, run := range e.Runs {
		lines[i] = fmt.Sprintf("  - run %d: %s", run.RunID, run.Reason)
	}
	return fmt.Sprintf("%s: %d following run problem(s), so nothing was enabled:\n%s",
		enableFailed, len(e.Runs), strings.Join(lines, "\n"))
}

// Unwrap returns the refusal.
func (e *RunsBlockedError) Unwrap() error { return e.err }

// enableError returns err as a *RevisionInvalidError or a *RunsBlockedError
// when its *APIError's context lists issues or runs, and unchanged otherwise.
func enableError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode == http.StatusConflict {
		var runs []RunProblem
		if decodeContext(apiErr.Context["runs"], &runs) && len(runs) > 0 {
			return &RunsBlockedError{Runs: runs, err: err}
		}
	}
	return revisionInvalidError(err)
}

func revisionInvalidError(err error) error {
	return issueListError(err, "REVISION_INVALID", "")
}

// issueListError returns err as a *RevisionInvalidError carrying failed when
// its *APIError is a 422 with code whose context lists issues, and unchanged
// otherwise.
func issueListError(err error, code, failed string) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity && apiErr.Code == code {
		var issues []Issue
		if decodeContext(apiErr.Context["errors"], &issues) && len(issues) > 0 {
			return &RevisionInvalidError{Issues: issues, failed: failed, err: err}
		}
	}
	return err
}

// decodeContext decodes one value of an *APIError's Context into out and
// reports whether it could.
func decodeContext(value interface{}, out interface{}) bool {
	if value == nil {
		return false
	}
	data, err := json.Marshal(value)
	return err == nil && jsonnum.Unmarshal(data, out) == nil
}

// followingRunsPageSize is the limit of each listing FollowingRuns makes.
const followingRunsPageSize = 100

var followingStates = []api.DeliverableRunState{
	api.DeliverableRunStateOpen, api.DeliverableRunStateRendered, api.DeliverableRunStateStale,
}

// FollowingRuns lists the deliverable's open, rendered and stale runs in
// new_period or reissue_enabled mode whose revision is not revisionID,
// ascending by id.
func (c *Client) FollowingRuns(token string, deliverableID, revisionID uuid.UUID) ([]Run, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	limit, orderBy, reverse := followingRunsPageSize, "id", false
	var runs []Run
	seen := map[int]bool{}
	for _, state := range followingStates {
		listed := 0
		for page := 1; ; page++ {
			params := &api.ListDeliverableRunsParams{Page: &page, Limit: &limit, OrderBy: &orderBy, Reverse: &reverse, State: &state}
			list, err := c.listRuns(apiClient, deliverableID, params)
			if err != nil {
				return nil, err
			}
			var results []Run
			if list.Results != nil {
				results = *list.Results
			}
			for _, run := range results {
				if !seen[run.ID] && follows(run, revisionID) {
					seen[run.ID] = true
					runs = append(runs, run)
				}
			}
			listed += len(results)
			if len(results) < limit || (list.TotalRows != nil && listed >= *list.TotalRows) {
				break
			}
		}
	}
	slices.SortFunc(runs, func(a, b Run) int { return cmp.Compare(a.ID, b.ID) })
	return runs, nil
}

func follows(run Run, revisionID uuid.UUID) bool {
	return slices.Contains(followingStates, run.State) &&
		(run.RevisionMode == api.NewPeriod || run.RevisionMode == api.ReissueEnabled) &&
		run.RevisionID != revisionID
}

// ResolveEnableTarget selects the revision of deliverableID to enable and
// returns it with every revision of the deliverable. A release or revision
// selects as ResolveRevision does; with neither, the only draft revision is
// selected: no revisions is a not-found error, and revisions with no draft or
// several drafts are a bad-usage error.
func (c *Client) ResolveEnableTarget(token string, deliverableID uuid.UUID, release, revision string) (target Revision, all []Revision, err error) {
	return c.resolveTarget(token, deliverableID, release, revision,
		"name the release to enable with --release (a disabled release can be enabled again)")
}

// ResolveTemplateTarget selects a revision of deliverableID as
// ResolveEnableTarget does, and returns it with every revision of the
// deliverable. Its refusal of revisions with no draft advises uploading a
// template without attaching it.
func (c *Client) ResolveTemplateTarget(token string, deliverableID uuid.UUID, release, revision string) (target Revision, all []Revision, err error) {
	return c.resolveTarget(token, deliverableID, release, revision,
		"a template attaches only to a draft, so upload it without --deliverable and attach it with --template-id once a draft exists")
}

// resolveTarget is ResolveEnableTarget with noDraft as the advice in its
// refusal of revisions with no draft.
func (c *Client) resolveTarget(token string, deliverableID uuid.UUID, release, revision, noDraft string) (target Revision, all []Revision, err error) {
	if err := ValidateRevisionSelector(release, revision); err != nil {
		return Revision{}, nil, err
	}
	items, err := c.ListRevisions(token, deliverableID)
	if err != nil {
		return Revision{}, nil, err
	}
	if release != "" || revision != "" {
		item, err := matchRevision(items, deliverableID, release, revision)
		if err != nil {
			return Revision{}, nil, err
		}
		return item, items, nil
	}
	if len(items) == 0 {
		return Revision{}, nil, noRevisions(deliverableID)
	}
	var drafts []string
	for _, item := range items {
		if item.State == api.DeliverableRevisionStateDraft {
			target = item
			drafts = append(drafts, fmt.Sprintf("%q", item.Release))
		}
	}
	switch len(drafts) {
	case 1:
		return target, items, nil
	case 0:
		return Revision{}, nil, qerrors.NewBadUsagef(
			"deliverable %s has no draft revision; %s; use 'qctl get deliverable-revisions %s'",
			deliverableID, noDraft, deliverableID)
	}
	return Revision{}, nil, qerrors.NewBadUsagef(
		"deliverable %s has %d draft revisions (%s); choose one with --release; use 'qctl get deliverable-revisions %s'",
		deliverableID, len(drafts), strings.Join(drafts, ", "), deliverableID)
}
