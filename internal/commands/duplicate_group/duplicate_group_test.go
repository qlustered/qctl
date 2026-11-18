package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrgID  = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	groupID    = "0190aaaa-1111-4222-8333-444444444444"
	decisionID = "0190bbbb-1111-4222-8333-444444444444"
	carriedID  = "0190cccc-1111-4222-8333-444444444444"
	basePath   = "/api/orgs/" + testOrgID + "/datasets/42/duplicate-groups"
	groupPath  = basePath + "/" + groupID
)

func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		return nil
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

type env struct {
	t      *testing.T
	mock   *testutil.MockAPIServer
	bodies map[string]map[string]any
}

func newEnv(t *testing.T) *env {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(endpointKey, testOrgID, "test-token")
	return &env{t: t, mock: mock, bodies: map[string]map[string]any{}}
}

func (e *env) run(args ...string) (string, string, error) {
	e.t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"duplicate-group"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// respond answers method path with status and payload, recording the body.
func (e *env) respond(method, path string, status int, payload any) {
	e.mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		if len(raw) > 0 {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				e.t.Errorf("%s %s body is not a JSON object: %s", method, path, raw)
			}
			e.bodies[method+" "+path] = body
		}
		testutil.RespondJSON(w, status, payload)
	})
}

func (e *env) group(g map[string]any) { e.respond("GET", groupPath, http.StatusOK, g) }

// postBody returns the JSON body recorded for a POST to path.
func (e *env) postBody(path string) map[string]any {
	e.t.Helper()
	b, ok := e.bodies["POST "+path]
	if !ok {
		e.t.Fatalf("no POST %s request body recorded; requests: %q", path, e.mock.Requests())
	}
	return b
}

func (e *env) assertRequests(want ...string) {
	e.t.Helper()
	if got := e.mock.Requests(); !reflect.DeepEqual(got, want) {
		e.t.Errorf("requests = %q, want %q", got, want)
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if want == 0 {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func member(id, row int, outcome any) map[string]any {
	return map[string]any{
		"id": id, "row_id": row, "data_source_model_id": 5, "location": "quarantine", "rank": nil,
		"outcome": outcome, "identical_at_load_to": []int{}, "deleted_at": nil, "deleted_by_id": nil,
	}
}

func groupJSON(origin string, mutate ...func(map[string]any)) map[string]any {
	g := map[string]any{
		"id": groupID, "origin": origin, "state": "needs_decision", "file_key": "f", "held_count": 2,
		"awaiting_approval": false, "awaiting_corrected_file": false, "unapplied": false,
		"created_at": "2026-11-10T12:00:00Z", "responses": []any{}, "differing_cells": []any{},
		"earlier_decisions": []any{}, "earlier_responses": []any{}, "decision": nil, "carried_decision": nil,
		"pending_decision": nil,
		"caller":           map[string]any{"can_decide": true, "caller_approved": false, "can_retract": false},
		"members":          []any{member(811, 1001, "held"), member(812, 1002, "held"), member(813, 1003, nil)},
	}
	for _, m := range mutate {
		m(g)
	}
	return g
}

func decisionJSON(id, kind, state string, mutate ...func(map[string]any)) map[string]any {
	d := map[string]any{
		"id": id, "kind": kind, "state": state, "scope": "table", "allowed_count": nil, "kept_member_id": nil,
		"reason": nil, "comment": nil, "period_key": nil, "carried": false, "required_approvals": 2,
		"designated_reviewer_ids": []any{}, "proposed_by_id": "0190dddd-1111-4222-8333-444444444444",
		"created_at": "2026-11-10T12:00:00Z",
		"approvals": []any{map[string]any{
			"reviewer_id": "0190dddd-1111-4222-8333-444444444444", "approved_at": "2026-11-10T12:00:00Z", "counted": true,
		}},
	}
	for _, m := range mutate {
		m(d)
	}
	return d
}

func pending(kind string, mutate ...func(map[string]any)) func(map[string]any) {
	return func(g map[string]any) {
		g["pending_decision"] = decisionJSON(decisionID, kind, "pending", mutate...)
		g["awaiting_approval"] = true
	}
}

func decisionResponse(d map[string]any) map[string]any {
	return map[string]any{
		"decision": d, "state": "decided",
		"apply": map[string]any{"decision_ids": []any{d["id"]}, "periods": []any{map[string]any{
			"dataset_id": 42, "outcome": "applied", "reason": nil, "affected_group_ids": []any{groupID},
			"moved_row_ids": []any{}, "reset_row_ids": []any{}, "released_row_ids": []any{1001, 1002},
			"enqueued_row_ids": []any{},
		}}},
	}
}

func apiError(code, msg string, context map[string]any) map[string]any {
	detail := map[string]any{"msg": msg, "error_code": code}
	if context != nil {
		detail["context"] = context
	}
	return map[string]any{"detail": detail}
}

var nullDecisionBody = map[string]any{
	"kind": nil, "allowed_count": nil, "kept_member_id": nil, "reason": nil, "comment": nil,
}

func wantBody(overrides map[string]any) map[string]any {
	b := map[string]any{}
	for k, v := range nullDecisionBody {
		b[k] = v
	}
	for k, v := range overrides {
		b[k] = v
	}
	return b
}

func TestDecisionVerbsSendTheDocumentedBody(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		args   []string
		want   map[string]any
	}{
		{"repeated_key allow without a count", "repeated_key",
			[]string{"allow"}, wantBody(map[string]any{"kind": "allow"})},
		{"repeated_key allow with a count and comment", "repeated_key",
			[]string{"allow", "--count", "3", "--comment", "two shipments"},
			wantBody(map[string]any{"kind": "allow", "allowed_count": float64(3), "comment": "two shipments"})},
		{"identical_rows allow", "identical_rows",
			[]string{"allow", "--count", "1"}, wantBody(map[string]any{"kind": "allow", "allowed_count": float64(1)})},
		{"person_marked allow", "person_marked",
			[]string{"allow"}, wantBody(map[string]any{"kind": "allow"})},
		{"repeated_key keep-one", "repeated_key",
			[]string{"keep-one", "--member", "812"}, wantBody(map[string]any{"kind": "keep_one", "kept_member_id": float64(812)})},
		{"identical_rows keep-one takes no member", "identical_rows",
			[]string{"keep-one"}, wantBody(map[string]any{"kind": "keep_one"})},
		{"person_marked not-duplicates", "person_marked",
			[]string{"not-duplicates", "--reason", "different customers"},
			wantBody(map[string]any{"kind": "not_duplicates", "reason": "different customers"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON(tc.origin))
			e.respond("POST", groupPath+"/decision", http.StatusOK,
				decisionResponse(decisionJSON(decisionID, tc.want["kind"].(string), "pending")))
			args := append(append([]string{}, tc.args...), "duplicate-group/"+groupID, "--table-id", "42", "--yes")
			out, _, err := e.run(args...)
			assertExit(t, err, 0)
			if got := e.postBody(groupPath + "/decision"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %v, want %v", got, tc.want)
			}
			e.assertRequests("GET "+groupPath, "POST "+groupPath+"/decision")
			for _, want := range []string{"duplicate-group/" + groupID + " proposed", "approvals: 1/2",
				"table/42 applied: moved 0, released 2", "Waiting for 1 more approval(s)",
				"Next: qctl describe duplicate-group " + groupID + " --table-id 42"} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// Every origin rule is checked against the group before the decision is
// sent, so a breach costs one read and exits 2.
func TestDecisionPreflightsExitTwoBeforeTheDecision(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		args   []string
	}{
		{"repeated_key count below 2", "repeated_key", []string{"allow", "--count", "1"}},
		{"identical_rows allow without a count", "identical_rows", []string{"allow"}},
		{"person_marked allow with a count", "person_marked", []string{"allow", "--count", "2"}},
		{"keep-one without a member", "repeated_key", []string{"keep-one"}},
		{"keep-one naming a member that left", "person_marked", []string{"keep-one", "--member", "813"}},
		{"keep-one naming no member of the group", "repeated_key", []string{"keep-one", "--member", "999"}},
		{"identical_rows keep-one with a member", "identical_rows", []string{"keep-one", "--member", "812"}},
		{"not-duplicates on a repeated_key group", "repeated_key", []string{"not-duplicates", "--reason", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON(tc.origin))
			args := append(append([]string{}, tc.args...), groupID, "--table-id", "42", "--yes")
			_, _, err := e.run(args...)
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests("GET " + groupPath)
		})
	}
}

func TestLocalValidationSendsNothing(t *testing.T) {
	cases := map[string][]string{
		"blank reason":           {"not-duplicates", groupID, "--table-id", "42", "--reason", "  ", "--yes"},
		"zero count":             {"allow", groupID, "--table-id", "42", "--count", "0", "--yes"},
		"short group id":         {"allow", "0190aaaa", "--table-id", "42", "--yes"},
		"no table":               {"allow", groupID, "--yes"},
		"blank comment":          {"allow", groupID, "--table-id", "42", "--comment", " ", "--yes"},
		"bad --decision":         {"withdraw", groupID, "--table-id", "42", "--decision", "nope", "--yes"},
		"bad approve --decision": {"approve", groupID, "--table-id", "42", "--decision", "nope", "--yes"},
		"blank disagree reason":  {"disagree", groupID, "--table-id", "42", "--reason", " ", "--yes"},
		"missing dismiss reason": {"dismiss", groupID, "--table-id", "42", "--yes"},
		"blank dismiss reason":   {"dismiss", groupID, "--table-id", "42", "--reason", "\t", "--yes"},
		"stop-series no table":   {"stop-series", groupID, "--yes"},
		"allow-all without file": {"allow-all", "--table-id", "42", "--yes"},
		"bad --reply-to":         {"comment", groupID, "--table-id", "42", "--body", "x", "--reply-to", "nope"},
		"empty comment body":     {"comment", groupID, "--table-id", "42", "--body", " "},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			_, _, err := e.run(args...)
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests()
		})
	}
}

func TestDecisionWithoutYesOffATerminalExitsTwo(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key"))
	_, _, err := e.run("allow", groupID, "--table-id", "42")
	assertExit(t, err, qerrors.ExitBadUsage)
	e.assertRequests("GET " + groupPath)
}

func TestAllowAll(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath+"/allow-all", http.StatusOK, map[string]any{
		"allowed": []any{groupID},
		"approved": []any{map[string]any{"group_id": carriedID, "decision_id": decisionID, "approvals": 1,
			"required_approvals": 2}},
		"skipped": []any{map[string]any{"group_id": decisionID, "reason": "pending_differs"}},
		"apply":   map[string]any{"decision_ids": []any{}, "periods": []any{}},
	})
	out, _, err := e.run("allow-all", "--table-id", "42", "--file-id", "file/311", "--comment", "re-sent", "--yes", "-o", "name")
	assertExit(t, err, 0)
	want := map[string]any{"stored_item_id": float64(311), "comment": "re-sent"}
	if got := e.postBody(basePath + "/allow-all"); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	if out != "duplicate-group/"+groupID+"\nduplicate-group/"+carriedID+"\n" {
		t.Errorf("-o name stdout = %q", out)
	}
}

func TestAllowAllHumanOutput(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath+"/allow-all", http.StatusOK, map[string]any{
		"allowed": []any{}, "approved": []any{},
		"skipped": []any{map[string]any{"group_id": groupID, "reason": "decided_here"}},
		"apply":   map[string]any{"decision_ids": []any{}, "periods": []any{}},
	})
	out, _, err := e.run("allow-all", "--table-id", "42", "--file-id", "311", "--yes")
	assertExit(t, err, 0)
	for _, want := range []string{"file/311: 0 allowed, 0 awaiting approval, 1 skipped",
		"duplicate-group/" + groupID + " skipped: decided_here"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestReapply(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath+"/apply", http.StatusOK, map[string]any{
		"decision_ids": []any{},
		"periods": []any{map[string]any{"dataset_id": 57, "outcome": "skipped_locked", "reason": "period locked",
			"affected_group_ids": []any{}, "moved_row_ids": []any{}, "reset_row_ids": []any{},
			"released_row_ids": []any{}, "enqueued_row_ids": []any{}}},
	})
	out, errOut, err := e.run("reapply", "--table-id", "42", "--yes")
	assertExit(t, err, 0)
	if !strings.Contains(out, "table/57 skipped_locked: moved 0, released 0, reset 0, enqueued 0 row(s) — period locked") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errOut, "warning: table/57 was not updated (skipped_locked)") {
		t.Errorf("stderr = %q", errOut)
	}
	e.assertRequests("POST " + basePath + "/apply")
}

func TestComment(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", groupPath+"/comments", http.StatusCreated, map[string]any{
		"comment":     map[string]any{"id": decisionID, "thread_id": carriedID, "body": "same order twice?"},
		"thread_id":   carriedID,
		"is_internal": true,
	})
	out, _, err := e.run("comment", groupID, "--table-id", "42", "--body", "same order twice?",
		"--reply-to", decisionID)
	assertExit(t, err, 0)
	want := map[string]any{"body": "same order twice?", "parent_id": decisionID, "linked_dry_run_job_id": nil}
	if got := e.postBody(groupPath + "/comments"); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	if !strings.Contains(out, "comment added [c: "+decisionID+"] (thread: "+carriedID+", internal)") {
		t.Errorf("stdout = %q", out)
	}
}

func TestCommentDepthExceededExitsTwo(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", groupPath+"/comments", http.StatusUnprocessableEntity,
		apiError("COMMENT_DEPTH_EXCEEDED", "too deep", map[string]any{"code": "COMMENT_DEPTH_EXCEEDED"}))
	_, _, err := e.run("comment", groupID, "--table-id", "42", "--body", "x")
	assertExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "max thread depth is 3") {
		t.Errorf("err = %v", err)
	}
}

func TestShare(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("person_marked"))
	e.respond("POST", groupPath+"/comments/share", http.StatusOK, map[string]any{
		"thread_id": carriedID, "shared_at": "2026-11-12T10:00:00Z", "shared_by_id": decisionID,
		"newly_shared": true, "mark_comment_exposed": true,
	})
	out, _, err := e.run("share", groupID, "--table-id", "42", "--comment", "please confirm", "--yes")
	assertExit(t, err, 0)
	if got := e.postBody(groupPath + "/comments/share"); !reflect.DeepEqual(got, map[string]any{"comment": "please confirm"}) {
		t.Errorf("body = %v", got)
	}
	for _, want := range []string{"discussion shared with its data source", "marked with is shown to the data source"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestShareRefusesACommentOnASharedDiscussion(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", func(g map[string]any) { g["shared_at"] = "2026-11-11T10:00:00Z" }))
	_, _, err := e.run("share", groupID, "--table-id", "42", "--comment", "late", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	e.assertRequests("GET " + groupPath)
}

// Each refusal keeps the exit code its status maps to and gains the hint
// for its code.
func TestRefusalsMapToExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   map[string]any
		exit   int
		want   string
		absent string
	}{
		{"not designated", http.StatusForbidden,
			apiError("APPROVER_NOT_DESIGNATED", "Only a designated reviewer may decide this duplicate group.", nil),
			qerrors.ExitUnauthorized, "get data-review-policy --table-id 42", ""},
		{"excluded", http.StatusForbidden,
			apiError("DUPLICATE_DECISION_REVIEWER_EXCLUDED", "You edited rows.", nil),
			qerrors.ExitUnauthorized, "another reviewer must", ""},
		{"no permission", http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "denied"}},
			qerrors.ExitUnauthorized, "DATA_REVIEW_APPROVE", ""},
		{"proposal required", http.StatusForbidden,
			apiError("CHANGE_PROPOSAL_REQUIRED", "requires a change proposal",
				map[string]any{"operation": "data_review_approve", "verb": "resolve", "denied_keys": []any{[]any{"rows"}}}),
			qerrors.ExitUnauthorized, "no proposal path", "alert propose"},
		{"another reviewer's pending", http.StatusConflict,
			apiError("DUPLICATE_DECISION_PENDING", "another reviewer's pending decision", nil),
			qerrors.ExitBadUsage, "qctl duplicate-group approve " + groupID + " --table-id 42", ""},
		{"another reviewer's pending names disagree", http.StatusConflict,
			apiError("DUPLICATE_DECISION_PENDING", "another reviewer's pending decision", nil),
			qerrors.ExitBadUsage, "qctl duplicate-group disagree " + groupID + " --table-id 42 --reason <text>", ""},
		{"exists is not a decide refusal", http.StatusConflict,
			apiError("DUPLICATE_DECISION_EXISTS", "has an effective decision", nil),
			qerrors.ExitBadUsage, "", "withdraw"},
		{"ended", http.StatusConflict, apiError("DUPLICATE_GROUP_ENDED", "This duplicate group has ended.", nil),
			qerrors.ExitBadUsage, "takes no decision", ""},
		{"period locked", http.StatusConflict, apiError("PERIOD_LOCKED", "locked", nil), qerrors.ExitBadUsage, "", ""},
		{"validation", http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "bad count", "error_code": "ERR_VALIDATION",
				"loc": []any{"body", "allowed_count"}}},
			qerrors.ExitBadUsage, "", ""},
		{"not found", http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "Duplicate group not found"}},
			qerrors.ExitNotFound, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key"))
			e.respond("POST", groupPath+"/decision", tc.status, tc.body)
			_, _, err := e.run("allow", groupID, "--table-id", "42", "--yes")
			assertExit(t, err, tc.exit)
			if err != nil && tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
			if err != nil && tc.absent != "" && strings.Contains(err.Error(), tc.absent) {
				t.Errorf("err carries %q:\n%v", tc.absent, err)
			}
		})
	}
}

func TestShareSpanningSourcesNamesThem(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key"))
	e.respond("POST", groupPath+"/comments/share", http.StatusConflict,
		apiError("DUPLICATE_THREAD_SPANS_SOURCES", "more than one data source",
			map[string]any{"data_source_model_ids": []any{5, 9}}))
	_, _, err := e.run("share", groupID, "--table-id", "42", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	if err != nil && !strings.Contains(err.Error(), "data sources: 5, 9") {
		t.Errorf("err = %v", err)
	}
}

func TestDecisionJSONOutputIsTheServerResponse(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key"))
	e.respond("POST", groupPath+"/decision", http.StatusOK, decisionResponse(decisionJSON(decisionID, "allow", "effective")))
	out, errOut, err := e.run("allow", groupID, "--table-id", "42", "--yes", "-o", "json")
	assertExit(t, err, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["state"] != "decided" || doc["decision"] == nil || doc["apply"] == nil {
		t.Errorf("doc = %v", doc)
	}
	if !strings.Contains(errOut, "decided allow") {
		t.Errorf("stderr lacks the result line: %q", errOut)
	}
}

// The result line names what the decision did: approved the pending decision
// read before it, decided or proposed; a change names the decision it
// replaces.
func TestDecideResultVerbAndReplacesLine(t *testing.T) {
	replacesCarried := func(d map[string]any) {
		d["allowed_count"] = 3
		d["replaces_decision_id"] = carriedID
	}
	ownAllow := func(g map[string]any) { g["decision"] = decisionJSON(carriedID, "allow", "effective", allowTwo) }
	carriedAllow := func(g map[string]any) {
		g["carried_decision"] = decisionJSON(carriedID, "allow", "effective", allowTwo, func(d map[string]any) {
			d["carried"] = true
		})
	}
	cases := []struct {
		name     string
		group    map[string]any
		count    string
		response map[string]any
		want     []string
		absent   string
	}{
		{"approves the pending decision", groupJSON("repeated_key", pending("allow", allowTwo)), "2",
			decisionJSON(decisionID, "allow", "effective", allowTwo),
			[]string{"approved allow 2 [d: " + decisionID + "] (decision: effective"}, "replaces"},
		{"decides an undecided group", groupJSON("repeated_key"), "2",
			decisionJSON(otherID, "allow", "effective", allowTwo),
			[]string{"decided allow 2 [d: " + otherID + "] (decision: effective"}, "replaces"},
		{"proposes a change", groupJSON("repeated_key", ownAllow), "3",
			decisionJSON(otherID, "allow", "pending", replacesCarried),
			[]string{"proposed allow 3 [d: " + otherID + "] (decision: pending",
				"replaces [d: " + carriedID + "] (allow 2), which stays in force until this takes effect"}, "superseded"},
		{"a change supersedes the group's own decision", groupJSON("repeated_key", ownAllow), "3",
			decisionJSON(otherID, "allow", "effective", replacesCarried),
			[]string{"decided allow 3 [d: " + otherID + "]", "superseded [d: " + carriedID + "] (allow 2)"}, "stays in force"},
		{"a change leaves a carried decision in force elsewhere", groupJSON("repeated_key", carriedAllow), "3",
			decisionJSON(otherID, "allow", "effective", replacesCarried),
			[]string{"replaces [d: " + carriedID + "] (allow 2) on this group; it stays in force wherever else it governs"},
			"superseded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(tc.group)
			e.respond("POST", groupPath+"/decision", http.StatusOK, decisionResponse(tc.response))
			out, _, err := e.run("allow", groupID, "--table-id", "42", "--count", tc.count, "--yes")
			assertExit(t, err, 0)
			for _, want := range tc.want {
				if !strings.Contains(out, "duplicate-group/"+groupID+" ") || !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, tc.absent) {
				t.Errorf("stdout carries %q:\n%s", tc.absent, out)
			}
		})
	}
}

func TestPendingNote(t *testing.T) {
	two, one, member := 2, 1, 812
	pendingID := uuid.MustParse(decisionID)
	governingID := uuid.MustParse(carriedID)
	allow2 := &duplicate_groups.Decision{ID: pendingID, Kind: duplicate_groups.KindAllow, AllowedCount: &two,
		RequiredApprovals: 2}
	allow1 := &duplicate_groups.Decision{ID: pendingID, Kind: duplicate_groups.KindAllow, AllowedCount: &one,
		RequiredApprovals: 2}
	effective := &duplicate_groups.Decision{ID: governingID, Kind: duplicate_groups.KindAllow, AllowedCount: &two}
	notProposer := refusalNotProposer
	periodLocked := duplicate_groups.DecideRefusal("period_locked")
	allowTwoBody := duplicate_groups.DecisionRequest{Kind: duplicate_groups.KindAllow, AllowedCount: &two}
	keepOneBody := duplicate_groups.DecisionRequest{Kind: duplicate_groups.KindKeepOne, KeptMemberID: &member}
	another := " Another reviewer's allow 2 is pending (approvals 0/2), and the server refuses a different decision " +
		"(409 DUPLICATE_DECISION_PENDING): approve, disagree with or (admin, ops) dismiss it instead."
	cases := []struct {
		name  string
		group duplicate_groups.Group
		body  duplicate_groups.DecisionRequest
		want  string
	}{
		{"nothing pending or governing", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey},
			allowTwoBody, ""},
		{"the group's own decision governs",
			duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey, Decision: effective}, keepOneBody,
			" This proposes a change to the group's effective allow 2 [d: " + carriedID + "]; it stays in force " +
				"until the change takes effect."},
		{"a carried decision governs",
			duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey, CarriedDecision: effective}, keepOneBody,
			" This proposes a change to the effective allow 2 [d: " + carriedID + "] carried from another group; " +
				"it stays in force until the change takes effect."},
		{"equal to the pending decision", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey,
			PendingDecision: allow2, Decision: effective}, allowTwoBody,
			" This approves the pending allow 2 (approvals 0/2)."},
		{"identical_rows keep-one is an allow of 1", duplicate_groups.Group{Origin: duplicate_groups.OriginIdenticalRows,
			PendingDecision: allow1}, duplicate_groups.DecisionRequest{Kind: duplicate_groups.KindKeepOne},
			" This approves the pending allow 1 (approvals 0/2)."},
		{"your own pending decision", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey,
			PendingDecision: allow2, Caller: duplicate_groups.CallerPermissions{CanWithdraw: true}}, keepOneBody,
			" This replaces your pending allow 2 (approvals 0/2) and restarts approvals."},
		{"another reviewer's pending decision", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey,
			PendingDecision: allow2, Caller: duplicate_groups.CallerPermissions{WithdrawReason: &notProposer}},
			keepOneBody, another},
		{"a proposer the caller flags do not name", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey,
			PendingDecision: allow2, Caller: duplicate_groups.CallerPermissions{WithdrawReason: &periodLocked}},
			keepOneBody, " This differs from the pending allow 2 (approvals 0/2): it replaces it when you proposed it, " +
				"and the server refuses it otherwise."},
		{"a repeated_key allow without a count", duplicate_groups.Group{Origin: duplicate_groups.OriginRepeatedKey,
			PendingDecision: allow2, Caller: duplicate_groups.CallerPermissions{CanWithdraw: true}},
			duplicate_groups.DecisionRequest{Kind: duplicate_groups.KindAllow},
			" If the group's current copies number the pending allow 2, this approves it; otherwise this replaces " +
				"your pending allow 2 (approvals 0/2) and restarts approvals."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingNote(&tc.group, tc.body); got != tc.want {
				t.Errorf("pendingNote =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestAllowAllPendingRefusalSaysToRunAgain(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath+"/allow-all", http.StatusConflict,
		apiError("DUPLICATE_DECISION_PENDING", "a group gained another reviewer's pending decision", nil))
	_, _, err := e.run("allow-all", "--table-id", "42", "--file-id", "311", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	if err != nil && !strings.Contains(err.Error(), "run allow-all again: it skips that group as pending_differs") {
		t.Errorf("err = %v", err)
	}
}
