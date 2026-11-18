package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	responseID         = "0190eeee-1111-4222-8333-444444444444"
	sourceGroupPath    = "/api/orgs/" + testOrgID + "/datasets/42/source-duplicate-groups/" + groupID
	responsesPath      = sourceGroupPath + "/responses"
	withdrawResponseAt = responsesPath + "/" + responseID + "/withdraw"
)

func responseJSON(kind string, mutate ...func(map[string]any)) map[string]any {
	r := map[string]any{
		"id": responseID, "group_id": groupID, "data_source_model_id": 7, "kind": kind,
		"comment": "two shipments", "author_name": "Sam Sender", "created_at": "2026-11-12T10:00:00Z",
		"ended_at": nil, "end_reason": nil,
	}
	for _, m := range mutate {
		m(r)
	}
	return r
}

func TestRespondSendsTheBody(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"hyphenated kind, comment trimmed, no source",
			[]string{"--kind", "separate-rows", "--comment", "  two shipments  "},
			map[string]any{"kind": "separate_rows", "comment": "two shipments", "data_source_model_id": nil}},
		{"underscore kind with a cloud source ref",
			[]string{"--kind", "corrected_file_follows", "--comment", "resending", "--cloud-source-id", "cloud-source/7"},
			map[string]any{"kind": "corrected_file_follows", "comment": "resending", "data_source_model_id": float64(7)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.respond("POST", responsesPath, http.StatusOK, responseJSON(tc.want["kind"].(string)))
			_, _, err := e.run(append([]string{"respond", groupID, "--table-id", "42"}, tc.args...)...)
			assertExit(t, err, 0)
			if got := e.postBody(responsesPath); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %v, want %v", got, tc.want)
			}
			e.assertRequests("POST " + responsesPath)
		})
	}
}

func TestRespondHumanOutput(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", responsesPath, http.StatusOK, responseJSON("separate_rows"))
	out, _, err := e.run("respond", "duplicate-group/"+groupID, "--table-id", "42", "--kind", "separate-rows", "--comment", "x")
	assertExit(t, err, 0)
	for _, want := range []string{
		"duplicate-group/" + groupID + " responded separate_rows [r: " + responseID + "] (cloud-source/7)",
		"a request, not a decision",
		"Next: qctl describe duplicate-group " + groupID + " --sender --table-id 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestRespondCorrectedFileHasNoRequestReminder(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", responsesPath, http.StatusOK, responseJSON("corrected_file_follows"))
	out, _, err := e.run("respond", groupID, "--table-id", "42", "--kind", "corrected-file-follows", "--comment", "x")
	assertExit(t, err, 0)
	if strings.Contains(out, "not a decision") {
		t.Errorf("stdout carries the separate-rows reminder:\n%s", out)
	}
}

func TestRespondPreflightExitsTwoBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no kind", []string{"respond", groupID, "--table-id", "42", "--comment", "x"}},
		{"unknown kind", []string{"respond", groupID, "--table-id", "42", "--kind", "allow", "--comment", "x"}},
		{"no comment", []string{"respond", groupID, "--table-id", "42", "--kind", "separate-rows"}},
		{"blank comment", []string{"respond", groupID, "--table-id", "42", "--kind", "separate-rows", "--comment", " \t\n"}},
		{"zero cloud source", []string{"respond", groupID, "--table-id", "42", "--kind", "separate-rows", "--comment", "x",
			"--cloud-source-id", "0"}},
		{"non-numeric cloud source", []string{"respond", groupID, "--table-id", "42", "--kind", "separate-rows",
			"--comment", "x", "--cloud-source-id", "seven"}},
		{"short group id", []string{"respond", "0190aaaa", "--table-id", "42", "--kind", "separate-rows", "--comment", "x"}},
		{"no table", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x"}},
		{"withdraw: bad response id", []string{"withdraw-response", groupID, "0190eeee", "--table-id", "42"}},
		{"withdraw: bad group id", []string{"withdraw-response", "nope", responseID, "--table-id", "42"}},
		{"withdraw: one argument", []string{"withdraw-response", groupID, "--table-id", "42"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, _, err := e.run(tc.args...)
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests()
		})
	}
}

func TestWithdrawResponse(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", withdrawResponseAt, http.StatusOK, responseJSON("separate_rows", func(r map[string]any) {
		r["ended_at"] = "2026-11-12T11:00:00Z"
		r["end_reason"] = "withdrawn"
	}))
	out, _, err := e.run("withdraw-response", "duplicate-group/"+groupID, responseID, "--table-id", "42")
	assertExit(t, err, 0)
	e.assertRequests("POST " + withdrawResponseAt)
	for _, want := range []string{
		"duplicate-group/" + groupID + " withdrew separate_rows [r: " + responseID + "] (cloud-source/7)",
		"Next: qctl describe duplicate-group " + groupID + " --sender --table-id 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestRespondMachineOutput(t *testing.T) {
	e := newEnv(t)
	e.respond("POST", responsesPath, http.StatusOK, responseJSON("separate_rows"))
	out, errOut, err := e.run("respond", groupID, "--table-id", "42", "--kind", "separate-rows", "--comment", "x", "-o", "json")
	assertExit(t, err, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["id"] != responseID || doc["kind"] != "separate_rows" {
		t.Errorf("doc = %v", doc)
	}
	if !strings.Contains(errOut, "responded separate_rows") {
		t.Errorf("stderr lacks the result line: %q", errOut)
	}

	e = newEnv(t)
	e.respond("POST", withdrawResponseAt, http.StatusOK, responseJSON("separate_rows"))
	out, _, err = e.run("withdraw-response", groupID, responseID, "--table-id", "42", "-o", "name")
	assertExit(t, err, 0)
	if out != "duplicate-group/"+groupID+"\n" {
		t.Errorf("-o name stdout = %q", out)
	}
}

// Each refusal keeps the exit code its status maps to and gains the hint
// for its code; none suggests a reviewer verb.
func TestSourceRefusalsMapToExitCodes(t *testing.T) {
	describe := "qctl describe duplicate-group " + groupID + " --sender --table-id 42"
	cases := []struct {
		name   string
		verb   []string
		path   string
		status int
		body   map[string]any
		exit   int
		want   string
	}{
		{"response not live", []string{"withdraw-response", groupID, responseID}, withdrawResponseAt, http.StatusConflict,
			apiError("DUPLICATE_RESPONSE_NOT_LIVE", "This response is no longer live.", nil),
			qerrors.ExitBadUsage, "replaced, withdrawn or ended meanwhile; '" + describe},
		{"withdraw: group ended", []string{"withdraw-response", groupID, responseID}, withdrawResponseAt, http.StatusConflict,
			apiError("DUPLICATE_GROUP_ENDED", "This duplicate group has ended.", nil),
			qerrors.ExitBadUsage, "takes no responses; '" + describe},
		{"respond: group ended", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x"}, responsesPath,
			http.StatusConflict, apiError("DUPLICATE_GROUP_ENDED", "This duplicate group has ended.", nil),
			qerrors.ExitBadUsage, "takes no responses; '" + describe},
		{"period locked", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x"}, responsesPath,
			http.StatusConflict, apiError("PERIOD_LOCKED", "locked", nil),
			qerrors.ExitBadUsage, "period is locked; '" + describe},
		{"several sources", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x"}, responsesPath,
			http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "The group has members of several of your data sources; choose one.",
				"error_code": "ERR_VALIDATION", "loc": []any{"body", "data_source_model_id"}}},
			qerrors.ExitBadUsage, "with --cloud-source-id"},
		{"source without a copy", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x", "--cloud-source-id", "9"},
			responsesPath, http.StatusNotFound,
			map[string]any{"detail": map[string]any{"msg": "Data source 9 has no member in duplicate group " + groupID}},
			qerrors.ExitNotFound, "--cloud-source-id must name one of your cloud sources"},
		{"no permission", []string{"respond", groupID, "--kind", "separate-rows", "--comment", "x"}, responsesPath,
			http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "denied"}},
			qerrors.ExitUnauthorized, "DATASET_INGEST_UPLOAD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.respond("POST", tc.path, tc.status, tc.body)
			_, _, err := e.run(append(tc.verb, "--table-id", "42")...)
			assertExit(t, err, tc.exit)
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
			for _, reviewer := range []string{"DATA_REVIEW_APPROVE", "takes no decision", "alert propose"} {
				if strings.Contains(err.Error(), reviewer) {
					t.Errorf("err carries the reviewer hint %q:\n%v", reviewer, err)
				}
			}
		})
	}
}
