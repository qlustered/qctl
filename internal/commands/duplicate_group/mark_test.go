package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	threadID  = "0190eeee-1111-4222-8333-444444444444"
	commentID = "0190ffff-1111-4222-8333-444444444444"
)

func markResponse(mode string, apply any) map[string]any {
	return map[string]any{
		"group_id": groupID, "comment_thread_id": threadID, "hold_mode": mode, "apply": apply,
		"members": []any{
			map[string]any{"id": 811, "row_id": 1001, "data_source_model_id": 5},
			map[string]any{"id": 812, "row_id": 1002, "data_source_model_id": 5},
		},
	}
}

func holdApply() map[string]any {
	return map[string]any{"decision_ids": []any{}, "periods": []any{map[string]any{
		"dataset_id": 42, "outcome": "applied", "reason": nil, "affected_group_ids": []any{groupID},
		"moved_row_ids": []any{1002}, "reset_row_ids": []any{}, "released_row_ids": []any{},
		"enqueued_row_ids": []any{1001, 1002},
	}}}
}

func TestMarkSendsTheDocumentedBody(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		apply any
		want  []string
	}{
		{"hold", "hold", holdApply(), []string{"mode: hold", "table/42 applied: moved 1, released 0, reset 0, enqueued 2"}},
		{"flag", "FLAG", nil, []string{"mode: flag"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			mode := strings.ToLower(tc.mode)
			e.respond("POST", basePath, http.StatusCreated, markResponse(mode, tc.apply))
			out, _, err := e.run("mark", "--table-id", "42", "--row-id", "1001,1002", "--mode", tc.mode,
				"--comment", "same order twice", "--yes")
			assertExit(t, err, 0)
			want := map[string]any{"row_ids": []any{float64(1001), float64(1002)}, "mode": mode, "comment": "same order twice"}
			if got := e.postBody(basePath); !reflect.DeepEqual(got, want) {
				t.Errorf("body = %v, want %v", got, want)
			}
			e.assertRequests("POST " + basePath)
			for _, w := range append(tc.want, "duplicate-group/"+groupID+" marked 2 row(s)",
				"member 811: row 1001 (cloud-source/5)", "qctl duplicate-group retract "+groupID+" --table-id 42",
				"Next: qctl describe duplicate-group "+groupID+" --table-id 42") {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestMarkLocalValidationSendsNothing(t *testing.T) {
	base := []string{"mark", "--table-id", "42", "--yes"}
	cases := map[string][]string{
		"one row":       {"--row-id", "1001", "--mode", "hold", "--comment", "x"},
		"no rows":       {"--mode", "hold", "--comment", "x"},
		"repeated row":  {"--row-id", "1001,1001", "--mode", "hold", "--comment", "x"},
		"zero row":      {"--row-id", "0,1001", "--mode", "hold", "--comment", "x"},
		"no mode":       {"--row-id", "1001,1002", "--comment", "x"},
		"unknown mode":  {"--row-id", "1001,1002", "--mode", "quarantine", "--comment", "x"},
		"no comment":    {"--row-id", "1001,1002", "--mode", "flag"},
		"blank comment": {"--row-id", "1001,1002", "--mode", "flag", "--comment", "  "},
		"long comment":  {"--row-id", "1001,1002", "--mode", "flag", "--comment", strings.Repeat("x", 10001)},
		"positional":    {"--row-id", "1001,1002", "--mode", "flag", "--comment", "x", groupID},
		"too many rows": {"--row-id", tooManyRows(), "--mode", "flag", "--comment", "x"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			args := append(append([]string{}, base...), extra...)
			_, _, err := e.run(args...)
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests()
		})
	}
}

func TestMarkWithoutATableSendsNothing(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.run("mark", "--row-id", "1001,1002", "--mode", "flag", "--comment", "x", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	e.assertRequests()
}

func tooManyRows() string {
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	return strings.Join(ids, ",")
}

func TestMarkWithoutYesOffATerminalExitsTwo(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.run("mark", "--table-id", "42", "--row-id", "1001,1002", "--mode", "hold", "--comment", "x")
	assertExit(t, err, qerrors.ExitBadUsage)
	e.assertRequests()
}

func TestMarkRefusalsMapToExitCodes(t *testing.T) {
	const other = "0190abab-1111-4222-8333-444444444444"
	cases := []struct {
		name   string
		status int
		body   map[string]any
		exit   int
		want   []string
	}{
		{"already marked", http.StatusConflict,
			apiError("DUPLICATE_ROW_ALREADY_MARKED", "Rows [1001] are already marked as duplicates.",
				map[string]any{"group_ids": []any{other}, "row_ids": []any{1001}}),
			qerrors.ExitBadUsage, []string{"rows 1001 are in marked group(s) " + other,
				"'qctl duplicate-group retract " + other + " --table-id 42'"}},
		{"undecided group", http.StatusConflict,
			apiError("DUPLICATE_ROW_IN_UNDECIDED_GROUP", "Rows [1001, 1002] are in duplicate groups awaiting a decision.",
				map[string]any{"group_ids": []any{other, groupID}, "row_ids": []any{1001, 1002}}),
			qerrors.ExitBadUsage, []string{"rows 1001, 1002 are held by group(s) " + other + ", " + groupID,
				"'qctl describe duplicate-group <group-id> --table-id 42'"}},
		{"rows not in the table", http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "Rows [9] are not rows of table 42.", "error_code": "ERR_VALIDATION",
				"loc": []any{"body", "row_ids"}, "context": map[string]any{"row_ids": []any{9}}}},
			qerrors.ExitBadUsage, []string{"rows 9 are not rows of table/42"}},
		{"no permission", http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "denied"}},
			qerrors.ExitUnauthorized, []string{"SUBMIT_ALERT", "SOURCE_DATA_WRITE"}},
		{"policy denied", http.StatusForbidden,
			apiError("CHANGE_REVIEW_POLICY_DENIED", "does not let the member role resolve this change",
				map[string]any{"operation": "user_alert_submit", "verb": "resolve", "denied_keys": []any{[]any{"rows"}}}),
			qerrors.ExitUnauthorized, nil},
		{"period locked", http.StatusConflict, apiError("PERIOD_LOCKED", "locked", nil), qerrors.ExitBadUsage, nil},
		{"not a mirror table", http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{"msg": "not mirror"}},
			qerrors.ExitBadUsage, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.respond("POST", basePath, tc.status, tc.body)
			_, _, err := e.run("mark", "--table-id", "42", "--row-id", "1001,1002", "--mode", "hold",
				"--comment", "x", "--yes")
			assertExit(t, err, tc.exit)
			for _, w := range tc.want {
				if err != nil && !strings.Contains(err.Error(), w) {
					t.Errorf("err lacks %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestMarkJSONOutputIsTheServerResponse(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath, http.StatusCreated, markResponse("hold", holdApply()))
	out, errOut, err := e.run("mark", "--table-id", "42", "--row-id", "1001,1002", "--mode", "hold",
		"--comment", "x", "--yes", "-o", "json")
	assertExit(t, err, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["group_id"] != groupID || doc["hold_mode"] != "hold" || doc["apply"] == nil {
		t.Errorf("doc = %v", doc)
	}
	if !strings.Contains(errOut, "marked 2 row(s)") {
		t.Errorf("stderr lacks the result line: %q", errOut)
	}
}

func TestMarkNameOutput(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", basePath, http.StatusCreated, markResponse("flag", nil))
	out, _, err := e.run("mark", "--table-id", "42", "--row-id", "1001,1002", "--mode", "flag",
		"--comment", "x", "--yes", "-o", "name")
	assertExit(t, err, 0)
	if strings.TrimSpace(out) != "duplicate-group/"+groupID {
		t.Errorf("stdout = %q", out)
	}
}

func markedGroup(mutate ...func(map[string]any)) map[string]any {
	return groupJSON("person_marked", append([]func(map[string]any){func(g map[string]any) {
		g["hold_mode"] = "hold"
		g["caller"] = map[string]any{"can_decide": false, "reason": "excluded", "caller_approved": false,
			"can_retract": true}
	}}, mutate...)...)
}

func retractResponse(comment any, enqueued []any) map[string]any {
	return map[string]any{
		"group_id": groupID, "ended_at": "2026-11-12T10:00:00Z", "end_reason": "retracted",
		"comment_id": comment, "enqueued_row_ids": enqueued,
	}
}

func TestRetract(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		response map[string]any
		body     map[string]any
		want     []string
	}{
		{"with a comment", []string{"--comment", "two orders"}, retractResponse(commentID, []any{1001, 1002}),
			map[string]any{"comment": "two orders"},
			[]string{"duplicate-group/" + groupID + " retracted (ended 2026-11-12 10:00 UTC, reason: retracted)",
				"comment added [c: " + commentID + "]", "2 quarantined row(s) queued for a reprocess: 1001, 1002"}},
		{"without a comment", nil, retractResponse(nil, []any{}),
			map[string]any{"comment": nil}, []string{"no quarantined row queued"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(markedGroup(pending("allow")))
			e.respond("POST", groupPath+"/retract", http.StatusOK, tc.response)
			args := append([]string{"retract", "duplicate-group/" + groupID, "--table-id", "42", "--yes"}, tc.args...)
			out, _, err := e.run(args...)
			assertExit(t, err, 0)
			if got := e.postBody(groupPath + "/retract"); !reflect.DeepEqual(got, tc.body) {
				t.Errorf("body = %v, want %v", got, tc.body)
			}
			e.assertRequests("GET "+groupPath, "POST "+groupPath+"/retract")
			for _, w := range append(tc.want, "Next: qctl describe duplicate-group "+groupID+" --table-id 42") {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestRetractListsAtMostTenRows(t *testing.T) {
	rows := make([]any, 12)
	for i := range rows {
		rows[i] = 1001 + i
	}
	e := newEnv(t)
	e.group(markedGroup())
	e.respond("POST", groupPath+"/retract", http.StatusOK, retractResponse(nil, rows))
	out, _, err := e.run("retract", groupID, "--table-id", "42", "--yes")
	assertExit(t, err, 0)
	if !strings.Contains(out, "12 quarantined row(s) queued for a reprocess: 1001, ") ||
		!strings.Contains(out, "1010, and 2 more") {
		t.Errorf("stdout = %s", out)
	}
}

// The states every caller is refused in cost one read and exit 2.
func TestRetractRefusesAGroupNobodyMayRetract(t *testing.T) {
	cases := []struct {
		name  string
		group map[string]any
		want  string
	}{
		{"load-formed group", groupJSON("repeated_key"), "only a person_marked group"},
		{"ended group", markedGroup(func(g map[string]any) { g["state"] = "ended" }), "has ended"},
		{"effective decision", markedGroup(func(g map[string]any) {
			g["decision"] = decisionJSON(decisionID, "allow", "effective")
		}), "duplicate-group/" + groupID + "'s own allow is effective, so its mark can no longer be retracted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(tc.group)
			_, _, err := e.run("retract", groupID, "--table-id", "42", "--yes")
			assertExit(t, err, qerrors.ExitBadUsage)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
			e.assertRequests("GET " + groupPath)
		})
	}
}

func TestRetractLocalValidationSendsNothing(t *testing.T) {
	cases := map[string][]string{
		"short group id": {"retract", "0190aaaa", "--table-id", "42", "--yes"},
		"no table":       {"retract", groupID, "--yes"},
		"blank comment":  {"retract", groupID, "--table-id", "42", "--comment", " ", "--yes"},
		"no group":       {"retract", "--table-id", "42", "--yes"},
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

func TestRetractRefusalsMapToExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   map[string]any
		exit   int
		want   string
	}{
		{"not person marked", http.StatusUnprocessableEntity,
			apiError("DUPLICATE_GROUP_NOT_PERSON_MARKED", "Only a duplicate group marked by a person can be retracted.", nil),
			qerrors.ExitBadUsage, "only a person_marked group can be retracted"},
		{"not the marker", http.StatusForbidden, apiError("DUPLICATE_GROUP_NOT_MARKER", "not the marker", nil),
			qerrors.ExitUnauthorized, "only the user who marked the group"},
		{"no permission", http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "denied"}},
			qerrors.ExitUnauthorized, "only the group's marker may retract it"},
		{"decided meanwhile", http.StatusConflict, apiError("DUPLICATE_DECISION_EXISTS", "effective", nil),
			qerrors.ExitBadUsage, "the group's own decision is effective, so its mark can no longer be retracted"},
		{"ended meanwhile", http.StatusConflict, apiError("DUPLICATE_GROUP_ENDED", "ended", nil), qerrors.ExitBadUsage, ""},
		{"period locked", http.StatusConflict, apiError("PERIOD_LOCKED", "locked", nil), qerrors.ExitBadUsage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(markedGroup())
			e.respond("POST", groupPath+"/retract", tc.status, tc.body)
			_, _, err := e.run("retract", groupID, "--table-id", "42", "--yes")
			assertExit(t, err, tc.exit)
			if err != nil && tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
		})
	}
}

func TestRetractJSONOutputIsTheServerResponse(t *testing.T) {
	e := newEnv(t)
	e.group(markedGroup())
	e.respond("POST", groupPath+"/retract", http.StatusOK, retractResponse(nil, []any{1001}))
	out, errOut, err := e.run("retract", groupID, "--table-id", "42", "--yes", "-o", "json")
	assertExit(t, err, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["end_reason"] != "retracted" {
		t.Errorf("doc = %v", doc)
	}
	if !strings.Contains(errOut, "retracted (ended") {
		t.Errorf("stderr lacks the result line: %q", errOut)
	}
}

func TestRetractQuestionNamesThePendingDecisionItWithdraws(t *testing.T) {
	two := 2
	group := duplicate_groups.Group{ID: uuid.MustParse(groupID)}
	if q := retractQuestion(&group, 42); strings.Contains(q, "withdrawn") {
		t.Errorf("question without a pending decision = %q", q)
	}
	group.PendingDecision = &duplicate_groups.Decision{ID: uuid.MustParse(decisionID), Kind: duplicate_groups.KindAllow,
		AllowedCount: &two}
	want := " Its pending allow 2 [d: " + decisionID + "] is withdrawn as the group ends."
	if q := retractQuestion(&group, 42); !strings.HasSuffix(q, want) {
		t.Errorf("question = %q, want it to end %q", q, want)
	}
}
