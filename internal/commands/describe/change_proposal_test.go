package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

func TestProposalExecutionExplainsStagingKind(t *testing.T) {
	for _, operation := range []api.ChangeReviewOperation{
		api.ChangeReviewOperationAlertResolve, api.ChangeReviewOperationWarningResolve, api.ChangeReviewOperationDatasetRulePatch,
	} {
		for _, kind := range []string{"draft_migration", "revalidation_candidate", "", "future_kind"} {
			t.Run(string(operation)+"/"+kind, func(t *testing.T) {
				p := &change_proposals.Proposal{Operation: operation, ExecutionResult: &map[string]interface{}{
					"outcome": "staged", "application": "pending",
				}}
				if kind != "" {
					(*p.ExecutionResult)["staged_as"] = kind
				}
				var b strings.Builder
				renderProposalExecution(&b, p)
				got := b.String()
				if kind == "" {
					if strings.Contains(got, "Staged as:") {
						t.Errorf("invented staging kind: %s", got)
					}
				} else if !strings.Contains(got, "Staged as: "+kind) || strings.Contains(got, "staged_as:") {
					t.Errorf("missing rendered staging kind: %s", got)
				}
				if kind == "revalidation_candidate" && !strings.Contains(got, "validation must pass and promote") {
					t.Errorf("missing promotion explanation: %s", got)
				}
			})
		}
	}
}

const cpOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	cpID       = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	cpOtherID  = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
	cpAliceID  = uuid.MustParse("0190dddd-0000-4000-8000-000000000001")
	cpBobID    = uuid.MustParse("0190dddd-0000-4000-8000-000000000002")
	cpCarolID  = uuid.MustParse("0190dddd-0000-4000-8000-000000000003")
	cpPath     = "/api/orgs/" + cpOrgID + "/change-proposals/" + cpID.String()
	cpAlert    = "/api/orgs/" + cpOrgID + "/alerts/77"
	cpWarning  = "/api/orgs/" + cpOrgID + "/warnings/55"
	cpUsers    = "/api/orgs/" + cpOrgID + "/users"
	cpCreated  = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	cpUpdated  = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	cpApproved = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
)

func cpIntPtr(i int) *int { return &i }

func cpPendingAlertProposal() api.ChangeProposalSchema {
	return api.ChangeProposalSchema{
		ID:                      cpID,
		OrganizationID:          uuid.MustParse(cpOrgID),
		State:                   api.ChangeProposalStatePendingReview,
		Operation:               api.ChangeReviewOperationAlertResolve,
		AlertItemID:             cpIntPtr(77),
		DatasetID:               cpIntPtr(7),
		DataSourceModelID:       cpIntPtr(12),
		AddressedAlertItemIds:   []int{78, 79},
		AddressedWarningItemIds: []int{},
		Payload: map[string]interface{}{
			"action": "map_value", "new_value": "Acme Corp", "map_value_once": false,
			"ids": nil, "another_field_new_value": nil,
		},
		PolicyKeys:  [][]string{{"map_value", "bad_value"}},
		AuthorIds:   []uuid.UUID{cpAliceID, cpBobID},
		CreatedByID: cpAliceID,
		CreatedAt:   cpCreated,
		UpdatedAt:   cpUpdated,
		VersionID:   3,
		AllowedActions: []api.ChangeProposalAction{
			api.ChangeProposalActionEdit, api.ChangeProposalActionWithdraw,
			api.ChangeProposalActionApprove, api.ChangeProposalActionReject,
			api.ChangeProposalActionRequestChanges,
		},
	}
}

func cpFailedWarningProposal() api.ChangeProposalSchema {
	reason := "values look right"
	note := "Broker confirmed the code"
	role := "admin"
	result := map[string]interface{}{
		"error":        map[string]interface{}{"status_code": 409, "error_code": "PERIOD_LOCKED", "message": "The period is locked."},
		"partial":      true,
		"target_state": map[string]interface{}{"resolved": false, "issue_type": "anomaly"},
	}
	return api.ChangeProposalSchema{
		ID:                      cpID,
		OrganizationID:          uuid.MustParse(cpOrgID),
		State:                   api.ChangeProposalStateExecutionFailed,
		Operation:               api.ChangeReviewOperationWarningResolve,
		WarningItemID:           cpIntPtr(55),
		DatasetID:               cpIntPtr(7),
		AddressedAlertItemIds:   []int{},
		AddressedWarningItemIds: []int{56},
		Payload:                 map[string]interface{}{"action": "overwrite_value", "new_value": "42", "ids": []interface{}{1, 2}},
		PolicyKeys:              [][]string{{"overwrite_value"}},
		AuthorIds:               []uuid.UUID{cpAliceID},
		CreatedByID:             cpAliceID,
		CreatedAt:               cpCreated,
		UpdatedAt:               cpUpdated,
		VersionID:               9,
		DecisionReason:          &reason,
		Note:                    &note,
		ApprovedByID:            &cpCarolID,
		ApproverRole:            &role,
		ApprovedAt:              &cpApproved,
		ExecutionStartedAt:      &cpApproved,
		ExecutedAt:              nil,
		ExecutionResult:         &result,
		AllowedActions:          []api.ChangeProposalAction{},
	}
}

type cpFixture struct {
	proposal     api.ChangeProposalSchema
	proposalCode int
	targetCode   int
	usersCode    int
	targetBody   map[string]interface{}
	detail       map[string]interface{} // review, impact_at_submit, impact_diff
}

// body is the GET response: the proposal with the detail keys added.
func (f *cpFixture) body() map[string]interface{} {
	raw, _ := json.Marshal(f.proposal)
	out := map[string]interface{}{}
	_ = json.Unmarshal(raw, &out)
	for k, v := range f.detail {
		out[k] = v
	}
	return out
}

func newCPFixture(p api.ChangeProposalSchema) *cpFixture {
	return &cpFixture{
		proposal: p, proposalCode: http.StatusOK, targetCode: http.StatusOK, usersCode: http.StatusOK,
		targetBody: map[string]interface{}{
			"id": 77, "resolved": false, "issue_type": "bad_value", "dataset_name": "orders",
			"change_proposal": map[string]interface{}{"id": cpID.String(), "state": "pending_review"},
		},
	}
}

func (f *cpFixture) serve(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", cpOrgID)
	env.SetupCredential(endpointKey, cpOrgID, "test-token")

	respond := func(status int, body interface{}) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status != http.StatusOK {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"detail": "nope"})
				return
			}
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	mock.RegisterHandler("GET", cpPath, respond(f.proposalCode, f.body()))
	mock.RegisterHandler("GET", cpAlert, respond(f.targetCode, f.targetBody))
	mock.RegisterHandler("GET", cpWarning, respond(f.targetCode, f.targetBody))
	mock.RegisterHandler("GET", cpUsers, respond(f.usersCode, map[string]interface{}{
		"results": []api.UserListItemSchema{
			{ID: cpAliceID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Author"},
			{ID: cpBobID, Email: "bob@corp.com", FirstName: "Bob", LastName: "Builder"},
			{ID: cpCarolID, Email: "carol@corp.com", FirstName: "Carol", LastName: "Checker"},
		},
		"next": nil,
	}))
	return mock
}

func execDescribeChangeProposal(args ...string) (string, string, error) {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewChangeProposalCommand())
	rootCmd.AddCommand(describeCmd)
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"describe", "change-proposal"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func assertContainsAll(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n---\n%s", want, out)
		}
	}
}

func TestDescribeChangeProposalRejectsANonUUIDWithoutARequest(t *testing.T) {
	mock := newCPFixture(cpPendingAlertProposal()).serve(t)
	_, _, err := execDescribeChangeProposal("42")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	assertRequests(t, mock)
}

func TestDescribeChangeProposalNotFoundExitsThree(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.proposalCode = http.StatusNotFound
	mock := f.serve(t)
	_, _, err := execDescribeChangeProposal(cpID.String())
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitNotFound, err)
	}
	assertRequests(t, mock, cpPath)
}

func TestDescribeChangeProposalPendingReviewPayload(t *testing.T) {
	mock := newCPFixture(cpPendingAlertProposal()).serve(t)
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("no warning expected, got %q", stderr)
	}
	assertRequests(t, mock, cpPath, cpAlert, cpUsers)
	id := cpID.String()
	assertOutputInOrder(t, out,
		"# change-proposal/"+id+"\n",
		"State: pending_review (live)\n",
		"Operation: alert_resolve\n",
		"Target: alert/77 — now: open (issue type bad_value)\n",
		"Held by: this proposal\n",
		"Table: orders (table-id 7)\n",
		"Cloud source: 12\n",
		"Addressed alerts: alert/78, alert/79\n",
		"Addressed warnings: (none)\n",
		"Proposer note: (none)\n",
		"Version: 3\n",
		"Created: 2026-10-20T09:00:00Z (",
		"by Alice Author\n",
		"Updated: 2026-10-21T09:00:00Z (",
		"Authors: Alice Author, Bob Builder\n",
		"Policy keys: map_value/bad_value\n",
		"## PAYLOAD\n",
		"The AlertResolution manifest it would resolve with (the form 'qctl alert resolve -f' reads):\n```yaml\n",
		"## DECISION\n",
		"Decision reason: (none)\n",
		"Approved: (not approved)\n",
		"## EXECUTION\n",
		"Started: (not started)\n",
		"Executed: (not executed)\n",
		"Result: (none)\n",
		"## NEXT ACTIONS\n",
		"- edit:            save the PAYLOAD manifest above, change it, then qctl change-proposal edit "+id+" -f <file.yaml>\n",
		"- withdraw:        qctl change-proposal withdraw "+id+"\n",
		"- approve:         qctl change-proposal approve "+id,
		"- reject:          qctl change-proposal reject "+id+" --reason \"...\"\n",
		"- request changes: qctl change-proposal request-changes "+id+" --reason \"...\"\n",
	)
	if strings.Contains(out, "submit:") {
		t.Errorf("submit is not allowed, yet a submit line was printed\n%s", out)
	}
	if strings.Contains(out, "obsolete") {
		t.Errorf("an open target must not carry the obsolete note\n%s", out)
	}
}

// cpPayloadBlock returns the first fenced YAML block after ## PAYLOAD.
func cpPayloadBlock(t *testing.T, out string) []byte {
	t.Helper()
	_, payload, _ := strings.Cut(out, "## PAYLOAD\n")
	_, rest, _ := strings.Cut(payload, "```yaml\n")
	block, _, ok := strings.Cut(rest, "```\n")
	if !ok {
		t.Fatalf("no fenced YAML block under PAYLOAD\n%s", out)
	}
	return []byte(block)
}

// cpWireJSON decodes v's JSON form with every null dropped, at any depth.
func cpWireJSON(t *testing.T, v interface{}) interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var drop func(interface{}) interface{}
	drop = func(x interface{}) interface{} {
		switch x := x.(type) {
		case map[string]interface{}:
			for k, val := range x {
				if val == nil {
					delete(x, k)
					continue
				}
				x[k] = drop(val)
			}
		case []interface{}:
			for i := range x {
				x[i] = drop(x[i])
			}
		}
		return x
	}
	return drop(decoded)
}

// The PAYLOAD block parses back through `qctl alert resolve -f`'s parser into
// the request body the proposal stores.
func TestDescribeChangeProposalPayloadIsAnAlertResolutionManifest(t *testing.T) {
	p := cpPendingAlertProposal()
	p.Payload = map[string]interface{}{
		"action": "map_value", "new_value": "Acme Corp", "another_field_new_value": "ACME",
		"new_datetime_format": "%Y-%m-%d", "sheet_name": "Sheet1", "file_type": "csv", "scope": "dataset",
		"map_value_once": false, "ids": []interface{}{1, 2}, "stored_item_id": 5,
		"initial_signatures": []interface{}{"sig"}, "field_name_to_value": map[string]interface{}{"a": "b"},
		"cant_sniff_header_params": map[string]interface{}{
			"raw_headers": []interface{}{"id", "name"}, "header_line_number_for_file": 2,
			"row_number_for_first_line_of_data": 3, "csv_delimiter": ";", "csv_quotechar": "\"",
			"csv_escapechar": "\\",
		},
		"field_names_resolution": []interface{}{map[string]interface{}{
			"incoming_field_name": "nm", "field_name": "name", "operation": "rename", "scope": "dataset",
			"expand": true,
		}},
	}
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc, err := alerts.ParseResolution(cpPayloadBlock(t, out))
	if err != nil {
		t.Fatalf("PAYLOAD block does not parse as an AlertResolution: %v\n%s", err, out)
	}
	if doc.APIVersion != "qluster.ai/v1" || doc.Kind != alerts.AlertResolutionKind {
		t.Errorf("envelope = %s %s, want qluster.ai/v1 %s", doc.APIVersion, doc.Kind, alerts.AlertResolutionKind)
	}
	if id, err := doc.AlertID(); err != nil || id != 77 {
		t.Errorf("AlertID() = %d, %v; want 77", id, err)
	}
	got, want := cpWireJSON(t, doc.Spec.ToRequest()), cpWireJSON(t, p.Payload)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request body from the manifest =\n  %v\nwant the stored payload\n  %v", got, want)
	}
}

// The warning half: the block parses back through `qctl warning resolve -f`'s
// parser into the stored request body.
func TestDescribeChangeProposalPayloadIsAWarningResolutionManifest(t *testing.T) {
	p := cpFailedWarningProposal()
	p.Payload = map[string]interface{}{
		"action": "map_value", "new_value": "42", "another_field_new_value": "43", "map_value_once": true,
		"ids": []interface{}{1, 2}, "initial_signatures": []interface{}{"sig"},
		"field_name_to_value": map[string]interface{}{"a": "b"},
	}
	f := newCPFixture(p)
	f.targetBody = map[string]interface{}{"id": 55, "resolved": false, "issue_type": "anomaly"}
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc, err := warnings.ParseResolution(cpPayloadBlock(t, out))
	if err != nil {
		t.Fatalf("PAYLOAD block does not parse as a WarningResolution: %v\n%s", err, out)
	}
	if doc.APIVersion != "qluster.ai/v1" || doc.Kind != warnings.WarningResolutionKind {
		t.Errorf("envelope = %s %s, want qluster.ai/v1 %s", doc.APIVersion, doc.Kind, warnings.WarningResolutionKind)
	}
	if id, err := doc.WarningID(); err != nil || id != 55 {
		t.Errorf("WarningID() = %d, %v; want 55", id, err)
	}
	got, want := cpWireJSON(t, doc.Spec.ToRequest()), cpWireJSON(t, p.Payload)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request body from the manifest =\n  %v\nwant the stored payload\n  %v", got, want)
	}
}

func TestDescribeChangeProposalUnmappablePayloadFallsBackToRawYAML(t *testing.T) {
	p := cpPendingAlertProposal()
	p.Payload = map[string]interface{}{"action": "map_value", "surprise": "key"}
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out,
		"The raw request body (it does not map onto an AlertResolution spec:",
		"```yaml\npayload:\n",
		"surprise: key\n",
	)
}

func TestDescribeChangeProposalExecutionFailedPartial(t *testing.T) {
	f := newCPFixture(cpFailedWarningProposal())
	f.targetBody = map[string]interface{}{"id": 55, "resolved": false, "issue_type": "anomaly", "dataset_name": "orders"}
	mock := f.serve(t)
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("no warning expected, got %q", stderr)
	}
	assertRequests(t, mock, cpPath, cpWarning, cpUsers)
	assertOutputInOrder(t, out,
		"State: execution_failed (terminal)\n",
		"Operation: warning_resolve\n",
		"Target: warning/55 — now: open (issue type anomaly)\n",
		"Addressed alerts: (none)\n",
		"Addressed warnings: warning/56\n",
		"Proposer note: \"Broker confirmed the code\"\n",
		"The WarningResolution manifest it would resolve with (the form 'qctl warning resolve -f' reads):\n",
		"kind: WarningResolution\n",
		"id: \"55\"\n",
		"action: overwrite_value\n",
		"## DECISION\n",
		"Decision reason: \"values look right\"\n",
		"Approved: by Carol Checker (role admin) at 2026-10-22T09:00:00Z (",
		"## EXECUTION\n",
		"Started: 2026-10-22T09:00:00Z (",
		"Executed: (not executed)\n",
		"Result: FAILED — 409 PERIOD_LOCKED: The period is locked.\n",
		"Partial: true — part of the change was committed before the failure; see what moved: qctl get history --table-id 7\n",
		"Target at settlement: issue_type: anomaly, resolved: false\n",
		"## NEXT ACTIONS\n",
		"(none — you may take no step on this proposal in its current state)\n",
	)
	if strings.Contains(out, "Held by:") {
		t.Errorf("a target with no holder printed a Held by line\n%s", out)
	}
}

func TestDescribeChangeProposalExecutedResponse(t *testing.T) {
	p := cpFailedWarningProposal()
	p.State = api.ChangeProposalStateExecuted
	p.ExecutedAt = &cpApproved
	result := map[string]interface{}{"response": map[string]interface{}{"resolved_count": 3}, "partial": false}
	p.ExecutionResult = &result
	f := newCPFixture(p)
	f.targetBody = map[string]interface{}{"id": 55, "resolved": true, "resolved_at": cpApproved, "issue_type": "anomaly"}
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"Target: warning/55 — now: resolved 2026-10-22T09:00:00Z (",
		"Result: succeeded — response:\n```yaml\nresolved_count: 3\n```\n",
		"Partial: false\n",
	)
	if strings.Contains(out, "obsolete") {
		t.Errorf("a terminal proposal must not carry the obsolete note\n%s", out)
	}
}

func TestDescribeChangeProposalSettledAfterTimeout(t *testing.T) {
	p := cpFailedWarningProposal()
	result := map[string]interface{}{"response": nil, "partial": "unknown", "settled_after_timeout": true}
	p.ExecutionResult = &result
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"Result: (no response recorded)\n",
		"Settled after timeout: true",
		"Partial: unknown — whether any of the change was committed is not known\n",
	)
}

func TestDescribeChangeProposalNextActionsFollowAllowedActions(t *testing.T) {
	p := cpPendingAlertProposal()
	p.State = api.ChangeProposalStateDraft
	p.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionSubmit}
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, actions, _ := strings.Cut(out, "## NEXT ACTIONS\n")
	if want := "- submit:          qctl change-proposal submit " + cpID.String() + "\n"; actions != want {
		t.Errorf("NEXT ACTIONS = %q, want %q", actions, want)
	}
}

func TestDescribeChangeProposalLiveOnAResolvedTargetWarnsOfObsolete(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.targetBody = map[string]interface{}{"id": 77, "resolved": true, "issue_type": "bad_value"}
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out,
		"Target: alert/77 — now: resolved (issue type bad_value)\n",
		"409 CHANGE_PROPOSAL_STATE_CONFLICT",
	)
}

func TestDescribeChangeProposalGoneTarget(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.targetCode = http.StatusNotFound
	f.serve(t)
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("a missing target must not fail the describe: %v", err)
	}
	if stderr != "" {
		t.Errorf("a gone target is an answer, not a warning; got %q", stderr)
	}
	assertContainsAll(t, out, "Target: alert/77 — now: gone\n", "turns it obsolete")
}

// A deleted target arrives as a null id (ON DELETE SET NULL), not a 404.
func TestDescribeChangeProposalDeletedTargetIsGone(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		proposal   func() api.ChangeProposalSchema
	}{
		{"alert", "alert", func() api.ChangeProposalSchema {
			p := cpPendingAlertProposal()
			p.AlertItemID = nil
			return p
		}},
		{"warning", "warning", func() api.ChangeProposalSchema {
			p := cpFailedWarningProposal()
			p.State = api.ChangeProposalStateDraft
			p.WarningItemID = nil
			p.ExecutionResult = nil
			p.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionEdit}
			return p
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newCPFixture(tc.proposal()).serve(t)
			out, stderr, err := execDescribeChangeProposal(cpID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stderr != "" {
				t.Errorf("a deleted target is an answer, not a warning; got %q", stderr)
			}
			assertRequests(t, mock, cpPath, cpUsers)
			id := cpID.String()
			assertOutputInOrder(t, out,
				"Target: a deleted "+tc.kind+" (the server cleared its id) — now: gone\n",
				"Note: the target is resolved or gone, so the next step taken on this proposal turns it obsolete",
				"The raw request body (its target "+tc.kind+" was deleted, so there is no "+tc.kind+" to resolve):\n",
				"- edit:            write the changed payload as a resolution manifest, then "+
					"qctl change-proposal edit "+id+" -f <file.yaml>\n",
			)
			if strings.Contains(out, "PAYLOAD manifest above") {
				t.Errorf("the edit line points at a manifest PAYLOAD does not show\n%s", out)
			}
		})
	}
}

// A proposal that names no target and has no operation that always names one
// records none.
func TestDescribeChangeProposalNoTargetForAnotherOperation(t *testing.T) {
	p := cpPendingAlertProposal()
	p.Operation = api.ChangeReviewOperation("settings_patch")
	p.AlertItemID = nil
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, "Target: (none recorded)\n")
	if strings.Contains(out, "Note:") {
		t.Errorf("no target to be gone, yet a Note was printed\n%s", out)
	}
}

func TestDescribeChangeProposalSettledWithTheTargetGone(t *testing.T) {
	p := cpFailedWarningProposal()
	result := map[string]interface{}{
		"response": nil, "partial": "unknown", "settled_after_timeout": true, "target_state": nil,
	}
	p.ExecutionResult = &result
	newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, "Target at settlement: gone\n")
	if strings.Contains(out, "Other result fields") {
		t.Errorf("a null target_state is not an unknown field\n%s", out)
	}
}

func TestDescribeChangeProposalTargetFetchFailureWarnsAndSucceeds(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.targetCode = http.StatusForbidden
	f.serve(t)
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("a target fetch failure must not fail the describe: %v", err)
	}
	if !strings.Contains(stderr, "Warning: could not read alert/77; its current state is unknown") {
		t.Errorf("stderr = %q, want the target warning", stderr)
	}
	assertContainsAll(t, out, "Target: alert/77 — now: unknown\n", "Table: table-id 7\n")
}

func TestDescribeChangeProposalNamesTheSessionsBehindEachAuthor(t *testing.T) {
	named, unnamed := "0190eeee-0000-4000-8000-000000000001", "0190eeee-0000-4000-8000-000000000002"
	session := func(id string, name interface{}, owner uuid.UUID) map[string]interface{} {
		return map[string]interface{}{"id": id, "name": name, "acting_for_user_id": owner.String()}
	}
	author := func(user uuid.UUID, sessions ...interface{}) map[string]interface{} {
		return map[string]interface{}{"user_id": user.String(), "delegations": append([]interface{}{}, sessions...)}
	}
	for _, tc := range []struct {
		name    string
		authors interface{}
		want    string
	}{
		{"named and unnamed sessions", []interface{}{
			author(cpAliceID, session(named, "ci-bot", cpAliceID), session(unnamed, nil, cpAliceID)), author(cpBobID),
		}, "Authors: Alice Author (via sessions ci-bot, " + unnamed + "), Bob Builder\n"},
		{"one session, one author left out", []interface{}{
			author(cpBobID, session(named, "laptop", cpBobID)),
		}, "Authors: Alice Author, Bob Builder (via session laptop)\n"},
		{"no authors key", nil, "Authors: Alice Author, Bob Builder\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCPFixture(cpPendingAlertProposal())
			if tc.authors != nil {
				f.detail = map[string]interface{}{"authors": tc.authors}
			}
			f.serve(t)
			out, _, err := execDescribeChangeProposal(cpID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertContainsAll(t, out, tc.want)
		})
	}
}

func TestDescribeChangeProposalDirectoryFailureFallsBackToUUIDs(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.usersCode = http.StatusForbidden
	f.serve(t)
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("a directory failure must not fail the describe: %v", err)
	}
	if !strings.Contains(stderr, "Warning: user directory unavailable; displaying user UUIDs") {
		t.Errorf("stderr = %q, want the directory warning", stderr)
	}
	assertContainsAll(t, out,
		"by "+cpAliceID.String()+"\n",
		"Authors: "+cpAliceID.String()+", "+cpBobID.String()+"\n",
	)
}

func TestDescribeChangeProposalJSONIsTheRawResponse(t *testing.T) {
	p := cpPendingAlertProposal()
	mock := newCPFixture(p).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String(), "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, cpPath)
	var got api.ChangeProposalSchema
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if got.ID != cpID || got.State != p.State || got.VersionID != p.VersionID || *got.AlertItemID != 77 {
		t.Errorf("-o json = %+v, want the proposal", got)
	}
	if got.Payload["new_value"] != "Acme Corp" {
		t.Errorf("payload.new_value = %v, want Acme Corp", got.Payload["new_value"])
	}
}

func TestDescribeChangeProposalHolderIsAnotherProposal(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.targetBody["change_proposal"] = map[string]interface{}{"id": cpOtherID.String(), "state": "draft"}
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, "Held by: change-proposal/"+cpOtherID.String()+" (draft)\n")
}

func cpReview() map[string]interface{} {
	return map[string]interface{}{
		"review": map[string]interface{}{
			"validity": "stale",
			"reasons":  []interface{}{map[string]interface{}{"reason": "linked_file_missing", "message": "The file 5 is deleted or ignored."}},
			"blockers": []interface{}{map[string]interface{}{
				"blocker": "period_locked", "status_code": 409, "error_code": "PERIOD_LOCKED", "message": "The period is locked.",
			}},
			"impact": map[string]interface{}{
				"bound": map[string]interface{}{
					"operation": "alert_resolve", "target": map[string]interface{}{"kind": "alert", "id": 77},
					"issue_type": "bad_value", "payload": map[string]interface{}{}, "addressed_ids": []interface{}{78},
					"keys": []interface{}{
						map[string]interface{}{"key": "bad_value/map_value", "scope": []interface{}{"cell"},
							"modifies_data": true, "follow_on": []interface{}{"resolves_matching_issues"}, "reversible": false},
						map[string]interface{}{"key": "bad_value/other"},
					},
				},
				"context": map[string]interface{}{
					"files":                []interface{}{map[string]interface{}{"id": 5, "key": "in/orders.csv"}},
					"same_issue_sweep_ids": []interface{}{80, 81},
				},
			},
			"impact_digest": "d1g35t",
		},
		"impact_at_submit": map[string]interface{}{"bound": map[string]interface{}{}, "context": map[string]interface{}{}},
		"impact_diff":      map[string]interface{}{"values_changed": map[string]interface{}{"root['addressed_ids'][0]": map[string]interface{}{"new_value": 78, "old_value": 79}}},
	}
}

func TestDescribeChangeProposalReviewSection(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.detail = cpReview()
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"## PAYLOAD\n",
		"## REVIEW\n",
		"Validity: stale — it needs an edit before it applies; approve is refused with 409 CHANGE_PROPOSAL_STALE\n",
		"Reasons:\n- linked_file_missing: The file 5 is deleted or ignored.\n",
		"Blockers (the operation would be refused; approve answers 409 CHANGE_PROPOSAL_BLOCKED):\n",
		"- period_locked (409 PERIOD_LOCKED): The period is locked.\n",
		"Issue type: bad_value\n",
		"Changes (by policy key):\n",
		"- bad_value/map_value: scope cell; modifies data: true; follow-on resolves_matching_issues; reversible: false\n",
		"- bad_value/other: (the action catalog does not describe it)\n",
		"Linked files: 5 (in/orders.csv)\n",
		"Same-issue sweep (also resolved): alert/80, alert/81\n",
		"Changed since submit (values_changed):\n```yaml\n",
		"Impact digest: d1g35t (approve sends it",
		"## DECISION\n",
	)
}

func TestDescribeChangeProposalSaysAMaskedViewerIsWithheldTheDigestAndDiff(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.detail = cpReview()
	f.detail["masked_fields"] = []string{"customer_email"}
	f.detail["review"].(map[string]interface{})["impact_digest"] = nil
	f.detail["impact_diff"] = nil
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"Changed since submit: (withheld — your view of the payload is masked)\n",
		"Impact digest: (withheld — your view of the payload is masked, so you cannot approve it)\n",
	)
}

func TestDescribeChangeProposalWithoutAReviewSaysWhy(t *testing.T) {
	newCPFixture(cpFailedWarningProposal()).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, "## REVIEW\n(none — the server reviews only a draft, pending_review or changes_requested proposal)\n")
}

func TestDescribeChangeProposalJSONCarriesTheReview(t *testing.T) {
	f := newCPFixture(cpPendingAlertProposal())
	f.detail = cpReview()
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String(), "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	review, _ := got["review"].(map[string]interface{})
	if review["impact_digest"] != "d1g35t" || got["impact_diff"] == nil || got["impact_at_submit"] == nil {
		t.Errorf("review/impact keys missing from -o json:\n%s", out)
	}
}

// cpDependencyReview is cpReview with a pending dependency's blocker,
// resource keys, and overlaps: cpOtherID (the dependency) and a warning
// proposal.
func cpDependencyReview() map[string]interface{} {
	detail := cpReview()
	review, _ := detail["review"].(map[string]interface{})
	review["validity"] = "current"
	review["reasons"] = []interface{}{}
	review["blockers"] = []interface{}{map[string]interface{}{
		"blocker": "dependency_not_executed", "status_code": 409, "error_code": "CHANGE_PROPOSAL_DEPENDENCY_PENDING",
		"message": "The change proposal " + cpOtherID.String() + " this one depends on is not executed yet.",
	}}
	review["resource_keys"] = []interface{}{"dataset:7/rows", "file:5"}
	detail["overlaps"] = []interface{}{
		map[string]interface{}{
			"id": cpOtherID.String(), "state": "pending_review", "operation": "alert_resolve",
			"alert_item_id": 70, "warning_item_id": nil, "dataset_rule_id": nil,
			"resource_keys": []interface{}{"dataset:7/rows"},
		},
		map[string]interface{}{
			"id": "0190cccc-0000-4000-8000-000000000003", "state": "draft", "operation": "warning_resolve",
			"alert_item_id": nil, "warning_item_id": 56, "dataset_rule_id": nil,
			"resource_keys": []interface{}{"dataset:7/rows", "file:5"},
		},
	}
	return detail
}

func TestDescribeChangeProposalPendingDependencyAndOverlaps(t *testing.T) {
	p := cpPendingAlertProposal()
	p.DependsOnID = &cpOtherID
	f := newCPFixture(p)
	f.detail = cpDependencyReview()
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"Depends on: change-proposal/"+cpOtherID.String()+" (approve is refused until it is executed or obsolete)\n",
		"## REVIEW\n",
		"- dependency_not_executed (409 CHANGE_PROPOSAL_DEPENDENCY_PENDING): The change proposal "+cpOtherID.String()+
			" this one depends on is not executed yet.\n"+
			"  it clears once the dependency is approved and executed:\n"+
			"  qctl describe change-proposal "+cpOtherID.String()+"\n",
		"Resource keys: dataset:7/rows, file:5\n",
		"## OVERLAPS\n",
		"- change-proposal/"+cpOtherID.String()+" (pending_review, alert_resolve on alert/70): dataset:7/rows — this proposal depends on it\n",
		"- change-proposal/0190cccc-0000-4000-8000-000000000003 (draft, warning_resolve on warning/56): dataset:7/rows, file:5\n",
		"To approve this one only after another: qctl change-proposal edit "+cpID.String()+" --depends-on <proposal-id>\n",
		"## DECISION\n",
	)
}

func TestDescribeChangeProposalFailedDependencyNamesTheEdit(t *testing.T) {
	p := cpPendingAlertProposal()
	p.DependsOnID = &cpOtherID
	f := newCPFixture(p)
	f.detail = cpReview()
	review, _ := f.detail["review"].(map[string]interface{})
	review["reasons"] = []interface{}{map[string]interface{}{
		"reason": "dependency_failed", "message": "The change proposal " + cpOtherID.String() + " this one depends on is rejected.",
	}}
	f.detail["overlaps"] = []interface{}{}
	f.serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOutputInOrder(t, out,
		"- dependency_failed: The change proposal "+cpOtherID.String()+" this one depends on is rejected.\n"+
			"  drop the dependency, or point it at another proposal with --depends-on:\n"+
			"  qctl change-proposal edit "+cpID.String()+" --remove-depends-on\n",
		"Resource keys: (none)\n",
		"## OVERLAPS\n(none — no other draft, pending_review or changes_requested proposal of this table that you can see acts on the same resources)\n",
	)
}

func TestDescribeChangeProposalWithoutAReviewHasNoOverlaps(t *testing.T) {
	newCPFixture(cpFailedWarningProposal()).serve(t)
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out,
		"Depends on: (none)\n",
		"## OVERLAPS\n(none — the server computes overlaps only for a draft, pending_review or changes_requested proposal)\n",
	)
}
