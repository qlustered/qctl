package file

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const questionPath = "/api/orgs/" + testOrgID + "/stored-items/123/replacement-question"

func execFile(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"file"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// registerAnswer answers the POST with file 123's question answered with the
// answer it was sent, and records the request body.
func registerAnswer(t *testing.T, mock *testutil.MockAPIServer) *string {
	t.Helper()
	body := new(string)
	mock.RegisterHandler(http.MethodPost, questionPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*body = string(raw)
		var sent struct{ Answer string }
		_ = json.Unmarshal(raw, &sent)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"new_stored_item_id":123,"older_stored_item_id":99,`+
			`"new_file_name":"june.csv","old_file_name":"june-draft.csv","old_file_sent_at":"2026-10-01T09:00:00Z",`+
			`"old_period_label":null,"new_period_label":"June 2026","answer":"`+sent.Answer+`",`+
			`"answered_at":"2026-10-02T09:00:00Z"}`))
	})
	return body
}

func TestReplacementVerbsSendTheirAnswer(t *testing.T) {
	for _, tc := range []struct {
		verb, wantBody, wantLine string
	}{
		{"replace-older", `{"answer":"discard_old"}`, "file/123 replaces file 99 (june-draft.csv)"},
		{"keep-both", `{"answer":"keep_both"}`, "file/123 does not replace file 99 (june-draft.csv); both files are kept"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			mock := newTestEnv(t)
			body := registerAnswer(t, mock)

			out, _, err := execFile(t, tc.verb, "file/123")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *body != tc.wantBody {
				t.Errorf("request body = %s, want %s", *body, tc.wantBody)
			}
			for _, want := range []string{tc.wantLine, "Next: qctl describe file 99"} {
				if !strings.Contains(out, want) {
					t.Errorf("output %q does not contain %q", out, want)
				}
			}
		})
	}
}

// -o json prints the answered question on stdout and moves the prose to
// stderr; -o name prints the new file's name.
func TestReplacementVerbsStructuredOutput(t *testing.T) {
	mock := newTestEnv(t)
	registerAnswer(t, mock)

	out, errOut, err := execFile(t, "keep-both", "123", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["answer"] != "keep_both" || doc["older_stored_item_id"] != float64(99) {
		t.Errorf("document = %v", doc)
	}
	if !strings.Contains(errOut, "both files are kept") {
		t.Errorf("stderr %q lacks the prose", errOut)
	}

	out, _, err = execFile(t, "keep-both", "123", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "file/123" {
		t.Errorf("-o name = %q, want file/123", out)
	}
}

// Each 409 exits 2 with its Hint:, and a 404 keeps exit 3.
func TestReplacementVerbsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantHint string
	}{
		{"settled question", http.StatusConflict,
			`{"detail":{"msg":"This question was already answered.","error_code":"REPLACEMENT_QUESTION_SETTLED"}}`,
			qerrors.ExitBadUsage, "Hint: file 123 has no open replacement question"},
		{"decided older file", http.StatusConflict,
			`{"detail":{"msg":"This held file was turned down.","error_code":"HELD_FILE_SETTLED"}}`,
			qerrors.ExitBadUsage, "Hint: the older file was already decided"},
		{"unknown file", http.StatusNotFound,
			`{"detail":{"msg":"file id 123 can not be found."}}`, qerrors.ExitNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler(http.MethodPost, questionPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})

			_, _, err := execFile(t, "replace-older", "123")
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (err %v)", code, tc.wantCode, err)
			}
			if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q lacks %q", err, tc.wantHint)
			}
		})
	}
}

func TestReplacementVerbsRefuseABadIDBeforeAnyRequest(t *testing.T) {
	mock := newTestEnv(t)
	posts := 0
	mock.RegisterHandler(http.MethodPost, questionPath, func(http.ResponseWriter, *http.Request) { posts++ })

	for _, arg := range []string{"abc", "0", "-5"} {
		_, _, err := execFile(t, "keep-both", "--", arg)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit code = %d, want %d (err %v)", arg, code, qerrors.ExitBadUsage, err)
		}
	}
	if posts != 0 {
		t.Errorf("%d request(s) sent for a bad id", posts)
	}
}
