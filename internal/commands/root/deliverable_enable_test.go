package root

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// enabledDraftTree is the enable answer for the draft revision: its tree, now
// enabled.
var enabledDraftTree = strings.ReplaceAll(strings.ReplaceAll(revisionTreeRecord,
	enabledRevisionID, draftRevisionID), `"release":"2026.1"`, `"release":"2026.2"`)

const validRevisionWithWarning = `{"valid":true,"errors":[],"warnings":[` +
	`{"binding_kind":"column_binding","binding_id":"20000000-0000-4000-8000-000000000002",` +
	`"loc":["sheet_bindings","Claims","column_bindings","amount"],"msg":"Column amount is bound twice."}]}`

const invalidRevision = `{"valid":false,"errors":[` +
	`{"binding_kind":"cell_binding","binding_id":"30000000-0000-4000-8000-000000000001",` +
	`"loc":["cell_bindings","Summary!C4"],"msg":"Sheet Summary is not in the template."}],` +
	`"warnings":[{"binding_kind":"revision","binding_id":null,"loc":[],"msg":"Formulas in the data region of sheet Claims."}]}`

// The run lines a move of the fixture's following runs prints: 45 is
// reissue_as_delivered and 46 is already on the draft, so neither moves.
const movedRunLines = `  run 41 (new_period): rendered → stale (revision_superseded; re-render required)
  run 43 (reissue_enabled): open
  run 44 (new_period): stale
`

const (
	enableDraftPath   = "POST " + revisionTestPath + draftRevisionID + "/enable"
	validateDraftPath = "POST " + revisionTestPath + draftRevisionID + "/validate"
)

func enableRun(id int, state, mode, revision string) map[string]any {
	return map[string]any{"id": id, "deliverable_id": liveDeliverableID, "revision_id": revision,
		"revision_mode": mode, "state": state, "attempt_id": 1, "sequence": id, "allow_empty": false,
		"run_params": map[string]any{"period": "2026-09"}}
}

func resetEnableDeliverableFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"enable", "deliverable"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "dry-run"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// enableTestServer serves revisions as both deliverables' revision list, the
// fixture's runs by state, and fails the test on any validate or enable POST
// a case does not register itself.
func enableTestServer(t *testing.T, revisions string) *deliverableTestServer {
	t.Helper()
	s := revisionTestServer(t)
	resetEnableDeliverableFlags(t)
	runs := map[string][]map[string]any{
		"open": {enableRun(43, "open", "reissue_enabled", enabledRevisionID),
			enableRun(45, "open", "reissue_as_delivered", enabledRevisionID)},
		"rendered": {enableRun(41, "rendered", "new_period", enabledRevisionID),
			enableRun(46, "rendered", "new_period", draftRevisionID)},
		"stale": {enableRun(44, "stale", "new_period", enabledRevisionID)},
	}
	for _, id := range []string{liveDeliverableID, trashedDeliverableID} {
		s.RegisterHandler("GET", deliverableTestPath+"/"+id+"/revisions", func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(revisions))
		})
		s.RegisterHandler("GET", deliverableTestPath+"/"+id+"/runs", func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			q := r.URL.Query()
			if q.Get("limit") != "100" || q.Get("order_by") != "id" || q.Get("page") != "1" {
				t.Errorf("runs query = %q", r.URL.RawQuery)
			}
			results, ok := runs[q.Get("state")]
			if !ok {
				t.Errorf("runs state = %q", q.Get("state"))
			}
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
		})
	}
	for _, id := range []string{enabledRevisionID, draftRevisionID} {
		for _, action := range []string{"validate", "enable"} {
			path := revisionTestPath + id + "/" + action
			s.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
				t.Errorf("unexpected POST %s", path)
				w.WriteHeader(http.StatusInternalServerError)
			})
		}
	}
	return s
}

// answerPOST registers status and body as the answer to a POST on path, a
// request log entry such as enableDraftPath.
func answerPOST(t *testing.T, s *deliverableTestServer, path string, status int, body string) {
	t.Helper()
	s.RegisterHandler("POST", strings.TrimPrefix(path, "POST "), func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
}

// runEnable executes the real tree and returns stdout and stderr apart.
func runEnable(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"enable", "deliverable"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), errOut.String(), err
}

func runsReads(id string) []string {
	return []string{deliverableRequest(id, "runs"), deliverableRequest(id, "runs"), deliverableRequest(id, "runs")}
}

func TestEnableDeliverableEnablesTheOnlyDraft(t *testing.T) {
	s := enableTestServer(t, liveDeliverableRevisions)
	answerPOST(t, s, enableDraftPath, http.StatusOK, enabledDraftTree)
	out, errOut, err := runEnable(t, "monthly-bordereau")
	if err != nil {
		t.Fatal(err)
	}
	want := `deliverable/monthly-bordereau release "2026.2" enabled (replaces "2026.1")
Following runs moved to release "2026.2":
` + movedRunLines + `Next: qctl describe deliverable monthly-bordereau
`
	if out != want || errOut != "" {
		t.Errorf("stdout = %q, want %q; stderr = %q", out, want, errOut)
	}
	wantRequests := append([]string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")},
		append(runsReads(liveDeliverableID), enableDraftPath)...)
	if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	if got := s.listQueries[0].Get("include_archived"); got != "true" {
		t.Errorf("include_archived = %q; the resolver must reach the trash", got)
	}
}

func TestEnableDeliverableFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			answerPOST(t, s, enableDraftPath, http.StatusOK, enabledDraftTree)
			out, errOut, err := runEnable(t, liveDeliverableID, "--revision", "deliverable-revision/"+draftRevisionID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, enabledDraftTree)
				if !strings.Contains(out, "9007199254740993") {
					t.Errorf("number not kept as sent: %s", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, enabledDraftTree)
			case "name":
				if out != "deliverable-revision/"+draftRevisionID+"\n" {
					t.Errorf("stdout = %q", out)
				}
			}
			if !strings.Contains(errOut, `release "2026.2" enabled (replaces "2026.1")`) || !strings.Contains(errOut, movedRunLines) {
				t.Errorf("stderr = %q", errOut)
			}
			if strings.Contains(out, "run 41") || strings.Contains(out, "Next:") {
				t.Errorf("prose on stdout: %q", out)
			}
			if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{enableDraftPath}) {
				t.Errorf("writes = %v", got)
			}
		})
	}
}

func TestEnableDeliverableDefaultNeedsExactlyOneDraft(t *testing.T) {
	for _, tc := range []struct{ name, revisions, says string }{
		{"several drafts", strings.ReplaceAll(liveDeliverableRevisions, `"state":"enabled"`, `"state":"draft"`), "2 draft revisions"},
		{"no draft", strings.ReplaceAll(liveDeliverableRevisions, `"state":"draft"`, `"state":"disabled"`), "no draft revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := enableTestServer(t, tc.revisions)
			out, _, err := runEnable(t, "monthly-bordereau")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if out != "" || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "qctl get deliverable-revisions") {
				t.Errorf("stdout = %q, error = %v", out, err)
			}
			want := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestEnableDeliverableAlreadyEnabledSendsNothing(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			out, errOut, err := runEnable(t, liveDeliverableID, "--release", "2026.1", "-o", format)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit = %d; %v", code, err)
			}
			const line = `deliverable/monthly-bordereau release "2026.1" already enabled` + "\n"
			reads := []string{deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}
			switch format {
			case "table":
				if out != line || errOut != "" {
					t.Errorf("stdout = %q, stderr = %q", out, errOut)
				}
			case "json":
				assertSeriesJSON(t, out, revisionTreeRecord)
				reads = append(reads, "GET "+revisionTestPath+enabledRevisionID)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, revisionTreeRecord)
				reads = append(reads, "GET "+revisionTestPath+enabledRevisionID)
			case "name":
				if out != "deliverable-revision/"+enabledRevisionID+"\n" {
					t.Errorf("stdout = %q", out)
				}
			}
			if format != "table" && errOut != line {
				t.Errorf("stderr = %q, want %q", errOut, line)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, reads) {
				t.Errorf("requests = %v, want %v", got, reads)
			}
		})
	}
}

func TestEnableDeliverableInTheTrashIsRefusedLocally(t *testing.T) {
	// The trash check precedes revision selection, so a release that would
	// read as already enabled is refused too, without listing revisions.
	for _, args := range [][]string{{"weekly-claims"}, {"weekly-claims", "--release", "2026.1"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			out, _, err := runEnable(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "in the trash") || !strings.Contains(err.Error(), "qctl undelete deliverable weekly-claims") {
				t.Errorf("stdout = %q, error = %v", out, err)
			}
			want := []string{deliverableRequest(), deliverableRequest(trashedDeliverableID)}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestEnableDeliverableDryRunValidatesInTheTrash(t *testing.T) {
	s := enableTestServer(t, liveDeliverableRevisions)
	answerPOST(t, s, validateDraftPath, http.StatusOK, validRevisionWithWarning)
	out, errOut, err := runEnable(t, "weekly-claims", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	want := `deliverable/weekly-claims release "2026.2" is valid; enabling it would replace "2026.1"
Enabling it would move these following runs to release "2026.2":
` + movedRunLines + `deliverable/weekly-claims is in the trash; restore it before enabling a release
Next: qctl undelete deliverable weekly-claims
`
	if out != want || !strings.HasPrefix(errOut, "Warning: ") {
		t.Errorf("stdout = %q, want %q; stderr = %q", out, want, errOut)
	}
	wantRequests := append([]string{deliverableRequest(), deliverableRequest(trashedDeliverableID),
		deliverableRequest(trashedDeliverableID, "revisions"), validateDraftPath}, runsReads(trashedDeliverableID)...)
	if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
}

func TestEnableDeliverableDryRunValid(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			answerPOST(t, s, validateDraftPath, http.StatusOK, validRevisionWithWarning)
			out, errOut, err := runEnable(t, "monthly-bordereau", "--dry-run", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			prose := `deliverable/monthly-bordereau release "2026.2" is valid; enabling it would replace "2026.1"
Enabling it would move these following runs to release "2026.2":
` + movedRunLines + `Next: qctl enable deliverable monthly-bordereau --release 2026.2
`
			const warning = "Warning: sheet_bindings.Claims.column_bindings.amount: Column amount is bound twice.\n"
			switch format {
			case "table":
				if out != prose || errOut != warning {
					t.Errorf("stdout = %q, want %q; stderr = %q", out, prose, errOut)
				}
			case "json":
				assertSeriesJSON(t, out, validRevisionWithWarning)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, validRevisionWithWarning)
			case "name":
				if out != "deliverable-revision/"+draftRevisionID+"\n" {
					t.Errorf("stdout = %q", out)
				}
			}
			if format != "table" && errOut != warning+prose {
				t.Errorf("stderr = %q, want %q", errOut, warning+prose)
			}
			if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{validateDraftPath}) {
				t.Errorf("writes = %v; a dry run must never enable", got)
			}
			if got := len(s.Requests()); got != 7 {
				t.Errorf("requests = %v; want the resolve, the revisions, one validate and three run listings", s.Requests())
			}
		})
	}
}

func TestEnableDeliverableDryRunOfTheEnabledRevision(t *testing.T) {
	s := enableTestServer(t, liveDeliverableRevisions)
	answerPOST(t, s, "POST "+revisionTestPath+enabledRevisionID+"/validate", http.StatusOK, `{"valid":true,"errors":[],"warnings":[]}`)
	out, errOut, err := runEnable(t, liveDeliverableID, "--release", "2026.1", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if want := `deliverable/monthly-bordereau release "2026.1" is valid and already enabled` + "\n"; out != want || errOut != "" {
		t.Errorf("stdout = %q, want %q; stderr = %q", out, want, errOut)
	}
	want := []string{deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions"),
		"POST " + revisionTestPath + enabledRevisionID + "/validate"}
	if got := s.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestEnableDeliverableDryRunInvalid(t *testing.T) {
	for _, tc := range []struct{ format, body, says string }{
		{"table", invalidRevision, "\n  - cell_bindings.Summary!C4: Sheet Summary is not in the template."},
		{"json", invalidRevision, "\n  - cell_bindings.Summary!C4: Sheet Summary is not in the template."},
		{"name", invalidRevision, "\n  - cell_bindings.Summary!C4: Sheet Summary is not in the template."},
		{"table", `{"valid":false,"errors":[],"warnings":[]}`, "the server listed no errors"},
	} {
		t.Run(tc.format+"/"+tc.says, func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			answerPOST(t, s, validateDraftPath, http.StatusOK, tc.body)
			out, errOut, err := runEnable(t, "monthly-bordereau", "--dry-run", "-o", tc.format)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if !strings.Contains(err.Error(), `deliverable/monthly-bordereau release "2026.2" failed validation`) || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v", err)
			}
			if tc.format == "json" {
				assertSeriesJSON(t, out, tc.body)
			} else if out != "" {
				t.Errorf("stdout = %q", out)
			}
			if tc.body == invalidRevision && errOut != "Warning: Formulas in the data region of sheet Claims.\n" {
				t.Errorf("stderr = %q", errOut)
			}
			want := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions"), validateDraftPath}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestEnableDeliverableRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		says       []string
	}{
		{"revision invalid", `{"detail":{"msg":"Revision failed validation with 2 error(s).","error_code":"REVISION_INVALID","context":{"errors":[` +
			`{"binding_kind":"cell_binding","binding_id":null,"loc":["cell_bindings","Summary!C4"],"msg":"Sheet Summary is not in the template."},` +
			`{"binding_kind":"sheet_binding","binding_id":null,"loc":["sheet_bindings","Claims"],"msg":"Table 42 is deleted."}]}}}`,
			http.StatusUnprocessableEntity,
			[]string{"\n  - cell_bindings.Summary!C4: Sheet Summary is not in the template.", "\n  - sheet_bindings.Claims: Table 42 is deleted."}},
		{"runs blocked", `{"detail":{"msg":"Run 41: Sheet 'Claims' has an overridden table.","context":{"runs":[` +
			`{"run_id":41,"reason":"Sheet 'Claims' has an overridden table."}]}}}`,
			http.StatusConflict,
			[]string{"1 following run problem(s), so nothing was enabled", "\n  - run 41: Sheet 'Claims' has an overridden table."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := enableTestServer(t, liveDeliverableRevisions)
			answerPOST(t, s, enableDraftPath, tc.status, tc.body)
			out, errOut, err := runEnable(t, "monthly-bordereau")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if out != "" || errOut != "" {
				t.Errorf("stdout = %q, stderr = %q", out, errOut)
			}
			if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{enableDraftPath}) {
				t.Errorf("writes = %v", got)
			}
		})
	}
}

func TestEnableDeliverableStopsWhenTheRunsCannotBeRead(t *testing.T) {
	s := enableTestServer(t, liveDeliverableRevisions)
	s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID+"/runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "permission denied"}})
	})
	out, _, err := runEnable(t, liveDeliverableID)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized || out != "" {
		t.Fatalf("exit = %d, stdout = %q; %v", code, out, err)
	}
	if got := seriesMutationRequests(s.MockAPIServer); len(got) != 0 {
		t.Errorf("enabled after a failed read: %v", got)
	}
}

func TestEnableDeliverableSanitizesServerText(t *testing.T) {
	revisions := strings.Replace(liveDeliverableRevisions, `"release":"2026.2"`, `"release":"2026.2\u001b[31m"`, 1)
	t.Run("enable", func(t *testing.T) {
		s := enableTestServer(t, revisions)
		answerPOST(t, s, enableDraftPath, http.StatusOK, enabledDraftTree)
		out, _, err := runEnable(t, "monthly-bordereau")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "\x1b") || !strings.Contains(out, `release "2026.2\\x1b[31m" enabled`) {
			t.Errorf("stdout = %q", out)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		s := enableTestServer(t, revisions)
		answerPOST(t, s, validateDraftPath, http.StatusOK, strings.Replace(validRevisionWithWarning, "bound twice", `bound\u001b[2J twice`, 1))
		out, errOut, err := runEnable(t, "monthly-bordereau", "--dry-run")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out+errOut, "\x1b") {
			t.Errorf("terminal control survived: stdout = %q, stderr = %q", out, errOut)
		}
		if !strings.Contains(out, `--release '2026.2\x1b[31m'`) || !strings.Contains(errOut, `bound\x1b[2J twice`) {
			t.Errorf("stdout = %q, stderr = %q", out, errOut)
		}
	})
}

func TestEnableDeliverableValidationBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{}, {" "}, {"table/42"}, {"deliverable-revision/" + draftRevisionID},
		{liveDeliverableID, "--release", ""},
		{liveDeliverableID, "--revision", ""},
		{liveDeliverableID, "--release", "2026.2", "--revision", draftRevisionID},
		{liveDeliverableID, "--revision", "d2d2d2d2"},
		{liveDeliverableID, "--revision", "deliverable/" + draftRevisionID},
		{liveDeliverableID, "-o", "wide"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetEnableDeliverableFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"enable", "deliverable"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
		})
	}
	// The control: a well-formed invocation gets past validation and fails
	// for want of a context, with another code.
	t.Run("control", func(t *testing.T) {
		resetEnableDeliverableFlags(t)
		err := runOutsideGetWithoutContext(t, "enable", "deliverable", liveDeliverableID, "--release", "2026.2", "--dry-run")
		if code := qerrors.GetExitCode(err); err == nil || code == qerrors.ExitBadUsage {
			t.Fatalf("exit = %d; %v", code, err)
		}
	})
}

func TestEnableDeliverableIsNotDestructive(t *testing.T) {
	s := enableTestServer(t, liveDeliverableRevisions)
	answerPOST(t, s, enableDraftPath, http.StatusOK, enabledDraftTree)
	// newDeliverableTestServer's context has no protected key, so it is
	// protected; a destructive command would be refused before any request.
	if _, _, err := runEnable(t, liveDeliverableID); err != nil {
		t.Fatal(err)
	}
	if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{enableDraftPath}) {
		t.Errorf("writes = %v", got)
	}
}
