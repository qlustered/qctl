package rows

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	orgPath = "/api/orgs/" + testOrgID

	// skippedGroupJSON is one repeated-key group as both the status-form
	// response and the 409's context list it.
	skippedGroupJSON = `{"group_id":"3f2a0000-0000-4000-8000-000000000001",` +
		`"key_values":{"region":null,"order_id":"1042"},` +
		`"members":[{"row_id":17,"initial_signature":"9f2a","table":"quarantine"},` +
		`{"row_id":18,"initial_signature":"1b7c","table":"clean"}]}`
)

// deleteScopes are the three scopes a --signature delete reaches, each with
// its route and the setup its scope resolution needs.
var deleteScopes = []struct {
	name  string
	flags []string
	path  string
	setup func(*testutil.MockAPIServer)
}{
	{"table", nil, orgPath + "/datasets/42/delete-rows", nil},
	{"cloud source", []string{"--cloud-source-id", "7"}, orgPath + "/data-sources/7/delete-rows", registerCloudSourceResolve},
	{"file", []string{"--file-id", "3"}, orgPath + "/stored-items/3/delete-rows", func(mock *testutil.MockAPIServer) {
		record(mock, "GET", orgPath+"/stored-items/3", `{"id":3,"dataset_id":42}`)
	}},
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A signature delete prints the server's count on stdout and each non-empty
// list on stderr, and the -o json document carries both lists.
func TestDeleteQuarantined_SignatureFormReportsUnmatchedAndNotExcluded(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", orgPath+"/datasets/42/delete-rows",
		`{"result":"ok","affected_rows":2,"unmatched_signatures":["cafe"],"not_excluded_signatures":["beef"]}`)

	args := []string{"rows", "delete-quarantined", "--reason", testReason, "orders",
		"--signature", "deadbeef", "--exclude-from-future-uploads", "--signature", "beef", "--signature", "cafe", "--yes"}
	stdout, stderr, err := runRows(t, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "delete-quarantined: 2 row(s) affected in table orders (id: 42)\n") {
		t.Errorf("stdout = %q, want the affected-row line", stdout)
	}
	for _, want := range []string{
		"Warning: 1 signature matched no quarantined row in table orders (id: 42); nothing was deleted for them:\n  cafe\n",
		"Warning: 1 signature deleted but not excluded: a live clean row of the same source shares the line's identity, or the row is a duplicate copy a Keep one decision did not load, so the line comes back if re-sent:\n  beef\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "Warning") {
		t.Errorf("a warning reached stdout: %q", stdout)
	}

	stdout, _, err = runRows(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeOne(t, stdout)
	if keys := keysOf(got); !reflect.DeepEqual(keys, []string{"affected_rows", "not_excluded_signatures", "unmatched_signatures"}) {
		t.Errorf("document keys = %v", keys)
	}
	if got["affected_rows"] != float64(2) ||
		!reflect.DeepEqual(got["unmatched_signatures"], []any{"cafe"}) ||
		!reflect.DeepEqual(got["not_excluded_signatures"], []any{"beef"}) {
		t.Errorf("document = %v", got)
	}
}

// Both lists are always in a signature delete's document, [] when the
// response omits them.
func TestDeleteQuarantined_SignatureDocumentAlwaysCarriesBothLists(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", orgPath+"/datasets/42/delete-rows", `{"result":"ok","affected_rows":1}`)

	stdout, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--signature", "deadbeef", "--exclude-from-future-uploads", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeOne(t, stdout)
	for _, k := range []string{"unmatched_signatures", "not_excluded_signatures"} {
		if v, ok := got[k]; !ok || !reflect.DeepEqual(v, []any{}) {
			t.Errorf("%s = %v (present %v), want []", k, v, ok)
		}
	}
	if strings.Contains(stderr, "Warning") {
		t.Errorf("stderr warns about empty lists: %q", stderr)
	}
}

// When no requested signature matched, the delete exits not-found at every
// scope, as send-to-quarantine does when every --row-id is missing.
func TestDeleteQuarantined_EverySignatureUnmatchedExitsNotFound(t *testing.T) {
	for _, sc := range deleteScopes {
		t.Run(sc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			if sc.setup != nil {
				sc.setup(mock)
			}
			record(mock, "POST", sc.path,
				`{"result":"ok","affected_rows":0,"unmatched_signatures":["deadbeef","cafe"],"not_excluded_signatures":[]}`)
			args := append([]string{"rows", "delete-quarantined", "--reason", testReason, "orders"}, sc.flags...)
			stdout, _, err := runRows(t, append(args, "--signature", "deadbeef", "--exclude-from-future-uploads", "--signature", "cafe", "--yes")...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("GetExitCode = %d (%v), want %d", code, err, qerrors.ExitNotFound)
			}
			if !strings.Contains(err.Error(), "  deadbeef\n  cafe") {
				t.Errorf("error does not list the signatures: %v", err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing for a delete that deleted nothing", stdout)
			}
		})
	}
}

// Some matched: exit 0, the unmatched ones are a warning.
func TestDeleteQuarantined_SomeSignaturesUnmatchedExitsZero(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", orgPath+"/datasets/42/delete-rows",
		`{"result":"ok","affected_rows":1,"unmatched_signatures":["cafe"],"not_excluded_signatures":[]}`)

	_, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--signature", "deadbeef", "--exclude-from-future-uploads", "--signature", "cafe", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "matched no quarantined row") {
		t.Errorf("stderr = %q", stderr)
	}
}

// The status form of the dataset delete-rows route is answered in
// DeleteRowsResponseSchema, which inherits skipped_repeated_keys, so the
// skipped groups reach the command.
func TestDeleteQuarantined_StatusFormReportsSkippedRepeatedKeys(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", orgPath+"/datasets/42/delete-rows",
		`{"result":"ok","affected_rows":5,"unmatched_signatures":[],"not_excluded_signatures":[],"skipped_repeated_keys":[`+skippedGroupJSON+`]}`)

	args := []string{"rows", "delete-quarantined", "--reason", testReason, "orders", "--status", "failed_validation", "--yes"}
	stdout, stderr, err := runRows(t, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "5 row(s) affected") {
		t.Errorf("stdout = %q", stdout)
	}
	for _, want := range []string{
		"Warning: 1 repeated-key group was left in place: the selection held some but not all copies of the key, so none of the group's rows were deleted:\n",
		"  key order_id=1042, region=null (group 3f2a0000-0000-4000-8000-000000000001):\n",
		"    quarantine 9f2a  row 17\n",
		"    clean      1b7c  row 18\n",
		"  qctl rows send-to-quarantine orders --row-id 18\n",
		"  qctl rows delete-quarantined orders --signature 9f2a --signature 1b7c --exclude-from-future-uploads=<true|false> --reason <reason>\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}

	stdout, _, err = runRows(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeOne(t, stdout)
	if keys := keysOf(got); !reflect.DeepEqual(keys, []string{"affected_rows", "skipped_repeated_keys"}) {
		t.Errorf("document keys = %v, want affected_rows and skipped_repeated_keys alone", keys)
	}
	groups, _ := got["skipped_repeated_keys"].([]any)
	if len(groups) != 1 {
		t.Fatalf("skipped_repeated_keys = %v", got["skipped_repeated_keys"])
	}
	var want any
	if err := json.Unmarshal([]byte(skippedGroupJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(groups[0], want) {
		t.Errorf("group = %v, want %v", groups[0], want)
	}
}

// A status delete that skipped nothing writes {affected_rows} alone.
func TestDeleteQuarantined_StatusDocumentOmitsAnEmptySkipList(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", orgPath+"/datasets/42/delete-all-bad-rows", `{"result":"ok","affected_rows":120}`)

	stdout, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--all", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys := keysOf(decodeOne(t, stdout)); !reflect.DeepEqual(keys, []string{"affected_rows"}) {
		t.Errorf("document keys = %v, want affected_rows alone", keys)
	}
	if strings.Contains(stderr, "Warning") {
		t.Errorf("stderr warns about nothing: %q", stderr)
	}
}

// --all at cloud-source scope is a status delete too.
func TestDeleteQuarantined_CloudSourceAllReportsSkippedRepeatedKeys(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	record(mock, "POST", orgPath+"/data-sources/7/delete-all-bad-rows",
		`{"result":"ok","affected_rows":9,"skipped_repeated_keys":[`+skippedGroupJSON+`]}`)

	stdout, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--cloud-source-id", "7", "--all", "--yes", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"affected_rows: 9", "skipped_repeated_keys:", "initial_signature: 1b7c"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{"--all excludes nothing", "1 repeated-key group was left in place", "9 row(s) affected"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// The other affected-row verbs keep writing {affected_rows} alone.
func TestNonDeleteVerbs_DocumentIsAffectedRowsAlone(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		args       []string
	}{
		{"reprocess-quarantined", orgPath + "/datasets/42/reprocess-bad-rows",
			[]string{"reprocess-quarantined", "orders", "--row-id", "7"}},
		{"hold", orgPath + "/datasets/42/update-keep-in-quarantine",
			[]string{"hold", "orders", "--row-id", "7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := record(mock, "POST", tc.path, `{"result":"ok","affected_rows":1}`)
			stdout, stderr, err := runRows(t, append(append([]string{"rows"}, tc.args...), "--yes", "-o", "json")...)
			if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, stderr)
			}
			if got.hits != 1 {
				t.Fatalf("hits = %d, want 1", got.hits)
			}
			if keys := keysOf(decodeOne(t, stdout)); !reflect.DeepEqual(keys, []string{"affected_rows"}) {
				t.Errorf("document keys = %v, want affected_rows alone", keys)
			}
		})
	}
}

// The prompt's consequence lines follow the selector: --signature names the
// files' lists it always writes and, per --exclude-from-future-uploads, the
// list a new upload reads; --status and --all exclude nothing.
func TestDeleteQuarantined_ConsequenceLinesFollowTheSelector(t *testing.T) {
	fileSetup := func(mock *testutil.MockAPIServer) {
		record(mock, "GET", orgPath+"/stored-items/3", `{"id":3,"dataset_id":42}`)
	}
	const reprocess = "--signature writes an exclusion entry for each deleted line, scoped to the line's source and identity, to "
	for _, tc := range []struct {
		name, path string
		args       []string
		setup      func(*testutil.MockAPIServer)
		want       []string
		absent     string
	}{
		{"signature at table scope", orgPath + "/datasets/42/delete-rows",
			[]string{"--signature", "deadbeef", "--exclude-from-future-uploads"}, nil,
			[]string{
				reprocess + "the exclusion list of each file a deleted row came from, so a reprocess of those files does not bring the line back.\n",
				"--exclude-from-future-uploads also writes them to the table's exclusion list, so a new upload drops a line with the same identity.\n",
				"A line whose entry would drop a live clean row is deleted but not excluded, and the result lists it.\n",
			}, "excludes nothing"},
		{"signature at table scope, false", orgPath + "/datasets/42/delete-rows",
			[]string{"--signature", "deadbeef", "--exclude-from-future-uploads=false"}, nil,
			[]string{
				reprocess + "the exclusion list of each file a deleted row came from",
				"--exclude-from-future-uploads=false: a new upload of these lines loads them again.\n",
			}, "also writes them"},
		{"signature at cloud-source scope", orgPath + "/data-sources/7/delete-rows",
			[]string{"--cloud-source-id", "7", "--signature", "deadbeef", "--exclude-from-future-uploads"}, registerCloudSourceResolve,
			[]string{"also writes them to the cloud source's exclusion list (the table's, for a table-series source), so"}, "excludes nothing"},
		{"signature at file scope", orgPath + "/stored-items/3/delete-rows",
			[]string{"--file-id", "3", "--signature", "deadbeef", "--exclude-from-future-uploads"}, fileSetup,
			[]string{
				reprocess + "the file's exclusion list, so",
				"also writes them to the exclusion list of the file's cloud source (the table's, for a table-series source), so",
			}, "excludes nothing"},
		{"signature at file scope, false", orgPath + "/stored-items/3/delete-rows",
			[]string{"--file-id", "3", "--signature", "deadbeef", "--exclude-from-future-uploads=false"}, fileSetup,
			[]string{reprocess + "the file's exclusion list, so", "a new upload of these lines loads them again"}, "also writes them"},
		{"status", orgPath + "/datasets/42/delete-rows",
			[]string{"--status", "failed_validation"}, nil,
			[]string{
				"--status excludes nothing: the rows are deleted with your reason and load again through the rules if the sender re-sends them.\n",
				"To keep specific lines from loading again, delete them with --signature.\n",
			}, "exclusion entry"},
		{"all", orgPath + "/datasets/42/delete-all-bad-rows",
			[]string{"--all"}, nil,
			[]string{"--all excludes nothing: "}, "exclusion entry"},
		{"all at cloud-source scope", orgPath + "/data-sources/7/delete-all-bad-rows",
			[]string{"--cloud-source-id", "7", "--all"}, registerCloudSourceResolve,
			[]string{"--all excludes nothing: "}, "exclusion entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			if tc.setup != nil {
				tc.setup(mock)
			}
			record(mock, "POST", tc.path, `{"result":"ok","affected_rows":1}`)
			args := append([]string{"rows", "delete-quarantined", "orders", "--reason", testReason}, tc.args...)
			_, stderr, err := runRows(t, append(args, "--yes")...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, tc.absent) {
				t.Errorf("stderr carries %q, which this selector does not do:\n%s", tc.absent, stderr)
			}
			if strings.Contains(stderr, "permanent") {
				t.Errorf("stderr repeats the retired permanent-ignore-list claim:\n%s", stderr)
			}
		})
	}
}

// A signature delete naming some copies of a repeated key is refused with 409
// before any write; the error lists every copy and the recipe, and keeps the
// exit code and error_code of the refusal.
func TestDeleteQuarantined_RepeatedKeyPartialDeleteHint(t *testing.T) {
	const body = `{"detail":{"msg":"This delete selects only some copies of a repeated key.",` +
		`"error_code":"REPEATED_KEY_PARTIAL_DELETE","context":{"repeated_key_copies":[` + skippedGroupJSON + `]}}}`
	for _, sc := range deleteScopes {
		t.Run(sc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			if sc.setup != nil {
				sc.setup(mock)
			}
			mock.RegisterHandler("POST", sc.path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusConflict, json.RawMessage(body))
			})
			args := append([]string{"rows", "delete-quarantined", "--reason", testReason, "orders"}, sc.flags...)
			stdout, _, err := runRows(t, append(args, "--signature", "9f2a", "--exclude-from-future-uploads", "--yes")...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("GetExitCode = %d (%v), want %d, the 409's", code, err, qerrors.ExitBadUsage)
			}
			if code := apierror.ErrorCodeOf(err); code != repeatedKeyPartialDelete {
				t.Errorf("error_code in the chain = %q, want %q", code, repeatedKeyPartialDelete)
			}
			for _, want := range []string{
				"Nothing was deleted. Every live copy of the key must be deleted together:\n",
				"  key order_id=1042, region=null (group 3f2a0000-0000-4000-8000-000000000001):\n",
				"    quarantine 9f2a  row 17\n",
				"    clean      1b7c  row 18\n",
				"Send the clean copies to quarantine, then delete every copy in one command:\n",
				"  qctl rows send-to-quarantine orders --row-id 18\n",
				"  qctl rows delete-quarantined orders --signature 9f2a --signature 1b7c --exclude-from-future-uploads=<true|false> --reason <reason>",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q:\n%v", want, err)
				}
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing for a refused delete", stdout)
			}
		})
	}
}

// Another 409 is returned unchanged.
func TestDeleteQuarantined_OtherConflictsGetNoRepeatedKeyHint(t *testing.T) {
	mock := newRowsTestEnv(t)
	mock.RegisterHandler("POST", orgPath+"/datasets/42/delete-rows", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, json.RawMessage(
			`{"detail":{"msg":"Period is locked","error_code":"PERIOD_LOCKED"}}`))
	})
	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--signature", "9f2a", "--exclude-from-future-uploads", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("GetExitCode = %d (%v)", code, err)
	}
	if strings.Contains(err.Error(), "send-to-quarantine") {
		t.Errorf("a PERIOD_LOCKED refusal got the repeated-key hint: %v", err)
	}
}

// A delete that removed rows names the listing that shows who deleted them,
// narrowed to the cloud source or file it acted on.
func TestDeleteQuarantined_DeletedRowsFollowUpNarrowsToTheScope(t *testing.T) {
	want := map[string]string{
		"table":        "  qctl get deleted-rows orders\n",
		"cloud source": "  qctl get deleted-rows orders --cloud-source-id 7\n",
		"file":         "  qctl get deleted-rows orders --file-id 3\n",
	}
	for _, sc := range deleteScopes {
		t.Run(sc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			if sc.setup != nil {
				sc.setup(mock)
			}
			record(mock, "POST", sc.path, `{"result":"ok","affected_rows":2}`)

			args := append([]string{"rows", "delete-quarantined", "--reason", testReason, "orders",
				"--signature", "deadbeef", "--exclude-from-future-uploads", "--yes"}, sc.flags...)
			stdout, _, err := runRows(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout, want[sc.name]) {
				t.Errorf("stdout = %q, want %q", stdout, want[sc.name])
			}
			words := shellWords(t, strings.TrimSpace(want[sc.name]))
			if words[1] != "get" || words[2] != "deleted-rows" {
				t.Errorf("follow-up line splits as %q", words)
			}
		})
	}
}

// Nothing deleted, nothing to list; and -o json keeps the line out of both
// streams.
func TestDeleteQuarantined_DeletedRowsFollowUpOnlyAfterADeleteInTableOutput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected string
		extra    []string
	}{
		{"nothing deleted", "0", nil},
		{"json", "2", []string{"-o", "json"}},
		{"yaml", "2", []string{"-o", "yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			record(mock, "POST", orgPath+"/datasets/42/delete-rows", `{"result":"ok","affected_rows":`+tc.affected+`}`)

			args := append([]string{"rows", "delete-quarantined", "--reason", testReason, "orders",
				"--status", "failed_validation", "--yes"}, tc.extra...)
			stdout, stderr, err := runRows(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(stdout+stderr, "get deleted-rows") {
				t.Errorf("a get deleted-rows line was printed:\nstdout %q\nstderr %q", stdout, stderr)
			}
		})
	}
}

// The listing has no third-party route, so a shared table gets no line.
func TestDeletedRowsLine_SharedTableHasNone(t *testing.T) {
	if got := (scope{thirdParty: true, kind: scopeTable, tableName: "orders"}).deletedRowsLine(); got != "" {
		t.Errorf("deletedRowsLine = %q, want none for a shared table", got)
	}
}
