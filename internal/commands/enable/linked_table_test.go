package enable

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	ltLatestRevision = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	ltOlderRevision  = "7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b"
	ltGroup          = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"
	ltOtherGroup     = "e3f1a2b4-c5d6-4e7f-8091-a2b3c4d5e6f7"
)

var ltLatestPath = "/api/orgs/" + testOrgID + "/datasets/12/latest-composite-spec-revision"

func ltEnablePath(rev string) string {
	return "/api/orgs/" + testOrgID + "/composite-spec-revisions/" + rev + "/enable"
}

// enableLinkedTableEnv serves table 12's latest revision, v3, in state.
func enableLinkedTableEnv(t *testing.T, state string) *testutil.MockAPIServer {
	t.Helper()
	mock := enableEnv(t)
	mock.RegisterHandler(http.MethodGet, ltLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": ltLatestRevision,
			"composite_spec_group_id":    ltGroup,
			"state":                      state,
			"release":                    "v3",
			"description":                nil,
			"spec_sha256":                "ab12",
			"created_at":                 "2026-09-22T10:00:00Z",
			"updated_at":                 "2026-09-22T10:00:00Z",
		})
	})
	return mock
}

// registerEnable answers every enable of rev the way the backend does on
// success, and counts the calls.
func registerEnable(mock *testutil.MockAPIServer, rev string) *int {
	calls := 0
	mock.RegisterHandler(http.MethodPost, ltEnablePath(rev), func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": rev,
			"composite_spec_group_id":    ltGroup,
			"state":                      "enabled",
		})
	})
	return &calls
}

// registerLinkedTableRevision serves GET /composite-spec-revisions/{rev} for a
// revision of group.
func registerLinkedTableRevision(mock *testutil.MockAPIServer, rev, group, release, state string) {
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/composite-spec-revisions/"+rev, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": rev,
			"composite_spec_group_id":    group,
			"state":                      state,
			"spec_sha256":                "cd34",
			"created_at":                 "2026-09-20T10:00:00Z",
			"created_by_user_id":         nil,
			"is_rerunnable":              true,
			"revision": map[string]interface{}{
				"release": release, "description": nil,
				"sources": map[string]interface{}{"orders": map[string]interface{}{"dataset_id": 11}},
				"output":  map[string]interface{}{"order_id": map[string]interface{}{"source": "orders", "field": "order_id"}},
			},
			"group": map[string]interface{}{
				"name": "orders_enriched", "slug": "orders-enriched", "spine_dataset_id": 11,
				"natural_key_columns": []string{"order_id"}, "natural_key_normalization": map[string]interface{}{},
			},
		})
	})
}

// execEnable runs the command with stdout and stderr kept apart.
func execEnable(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := enableCloudSourceRoot()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// posts lists the POST requests the mock received.
func posts(mock *testutil.MockAPIServer) []string {
	var out []string
	for _, req := range mock.Requests() {
		if strings.HasPrefix(req, "POST ") {
			out = append(out, req)
		}
	}
	return out
}

// The default: the table's latest revision is enabled, and the output names
// it and points at the rebuild the enable queued.
func TestEnableLinkedTable_EnablesTheLatestRevision(t *testing.T) {
	mock := enableLinkedTableEnv(t, "draft")
	calls := registerEnable(mock, ltLatestRevision)

	stdout, stderr, err := execEnable(t, "enable", "linked-table", "12")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}

	want := `linked-table/12 enabled
  revision : v3 (enabled)
Next: qctl get composite-run-jobs --group-id ` + ltGroup + `
`
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if *calls != 1 {
		t.Errorf("enable POSTs = %d, want 1", *calls)
	}
}

// -o name prints the table's id however the table was named.
func TestEnableLinkedTable_NameIsTheTableID(t *testing.T) {
	mock := enableLinkedTableEnv(t, "draft")
	registerEnable(mock, ltLatestRevision)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":12,"name":"orders_enriched","state":"active"}]}`))
	})

	stdout, stderr, err := execEnable(t, "enable", "linked-table", "--table", "orders_enriched", "-o", "name")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "linked-table/12\n" {
		t.Errorf("stdout = %q, want %q", stdout, "linked-table/12\n")
	}
}

// The table is named in the output as it was typed: a --table name stays a
// name, a --table-id stays a number.
func TestEnableLinkedTable_NamesTheTableAsTyped(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"--table":    {[]string{"--table", "orders_enriched"}, "linked-table/orders_enriched enabled\n"},
		"--table-id": {[]string{"--table-id", "12"}, "linked-table/12 enabled\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := enableLinkedTableEnv(t, "draft")
			calls := registerEnable(mock, ltLatestRevision)
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
					`{"total_rows":1,"page":1,"results":[{"id":12,"name":"orders_enriched","state":"active"}]}`))
			})

			stdout, stderr, err := execEnable(t, append([]string{"enable", "linked-table"}, tc.args...)...)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if !strings.HasPrefix(stdout, tc.want) {
				t.Errorf("stdout should start %q, got:\n%s", tc.want, stdout)
			}
			if *calls != 1 {
				t.Errorf("enable POSTs = %d, want 1", *calls)
			}
		})
	}
}

// --revision of the same linked table is enabled in place of the latest --
// the rollback path -- and the output names it.
func TestEnableLinkedTable_EnablesTheNamedRevision(t *testing.T) {
	mock := enableLinkedTableEnv(t, "enabled")
	registerLinkedTableRevision(mock, ltOlderRevision, ltGroup, "v2", "disabled")
	older := registerEnable(mock, ltOlderRevision)
	latest := registerEnable(mock, ltLatestRevision)

	stdout, stderr, err := execEnable(t, "enable", "linked-table", "12", "--revision", ltOlderRevision)
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if *older != 1 || *latest != 0 {
		t.Fatalf("enable POSTs: named revision %d, latest %d; want 1 and 0", *older, *latest)
	}
	if !strings.Contains(stdout, "  revision : v2 (enabled)\n") {
		t.Errorf("the output should name the enabled revision, got:\n%s", stdout)
	}
}

// A --revision of another linked table is refused before the enable is sent:
// the enable would change a different table than the one named.
func TestEnableLinkedTable_RefusesARevisionOfAnotherTable(t *testing.T) {
	mock := enableLinkedTableEnv(t, "draft")
	registerLinkedTableRevision(mock, ltOlderRevision, ltOtherGroup, "v1", "draft")
	registerEnable(mock, ltOlderRevision)

	_, _, err := execEnable(t, "enable", "linked-table", "12", "--revision", ltOlderRevision)
	if err == nil {
		t.Fatal("expected a revision of another group to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
	}
	if p := posts(mock); len(p) != 0 {
		t.Errorf("enabled another table's revision: %v", p)
	}
}

// An enabled revision is reported without the POST the backend would refuse
// with a 400, under every output format, and exits 0.
func TestEnableLinkedTable_AlreadyEnabledSendsNothing(t *testing.T) {
	cases := map[string]string{
		"table": "linked-table/12 already enabled\n  revision : v3 (enabled)\n",
		"json": "{\n  \"composite_spec_group_id\": \"" + ltGroup + "\",\n" +
			"  \"composite_spec_revision_id\": \"" + ltLatestRevision + "\",\n" +
			"  \"state\": \"enabled\"\n}\n",
		"yaml": "composite_spec_group_id: " + ltGroup + "\n" +
			"composite_spec_revision_id: " + ltLatestRevision + "\n" +
			"state: enabled\n",
		"name": "linked-table/12\n",
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			mock := enableLinkedTableEnv(t, "enabled")
			calls := registerEnable(mock, ltLatestRevision)

			stdout, stderr, err := execEnable(t, "enable", "linked-table", "12", "-o", format)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			if *calls != 0 {
				t.Errorf("enable POSTs = %d, want 0 for an enabled revision", *calls)
			}
		})
	}
}

// A named revision that is already enabled is reported the same way, from
// the detail read.
func TestEnableLinkedTable_AlreadyEnabledNamedRevisionSendsNothing(t *testing.T) {
	mock := enableLinkedTableEnv(t, "draft")
	registerLinkedTableRevision(mock, ltOlderRevision, ltGroup, "v2", "enabled")
	calls := registerEnable(mock, ltOlderRevision)

	stdout, stderr, err := execEnable(t, "enable", "linked-table", "12", "--revision", ltOlderRevision)
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	want := "linked-table/12 already enabled\n  revision : v2 (enabled)\n"
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if *calls != 0 {
		t.Errorf("enable POSTs = %d, want 0 for an enabled revision", *calls)
	}
}

// -o json and -o yaml print the server's answer to the enable; -o name the
// table.
func TestEnableLinkedTable_OutputFormats(t *testing.T) {
	cases := map[string]string{
		"json": "{\n  \"composite_spec_group_id\": \"" + ltGroup + "\",\n" +
			"  \"composite_spec_revision_id\": \"" + ltLatestRevision + "\",\n" +
			"  \"state\": \"enabled\"\n}\n",
		"yaml": "composite_spec_group_id: " + ltGroup + "\n" +
			"composite_spec_revision_id: " + ltLatestRevision + "\n" +
			"state: enabled\n",
		"name": "linked-table/12\n",
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			mock := enableLinkedTableEnv(t, "draft")
			calls := registerEnable(mock, ltLatestRevision)

			stdout, stderr, err := execEnable(t, "enable", "linked-table", "12", "-o", format)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			if *calls != 1 {
				t.Errorf("enable POSTs = %d, want 1", *calls)
			}
		})
	}
}

// The enable gates answer 400 with the reason in detail.msg; the command
// exits 2 and the reason reaches the user.
func TestEnableLinkedTable_GateRefusalExitsTwoWithTheServersReason(t *testing.T) {
	mock := enableLinkedTableEnv(t, "draft")
	reason := "composite spec revision " + ltLatestRevision + ": activation blocked — no dry run on record. " +
		"Launch a composite dry run for this revision and wait for it to finish before enabling."
	mock.RegisterHandler(http.MethodPost, ltEnablePath(ltLatestRevision), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"detail": map[string]interface{}{"msg": reason, "error_id": "e-1"},
		})
	})

	stdout, _, err := execEnable(t, "enable", "linked-table", "12")
	if err == nil {
		t.Fatal("expected the gate's 400 to fail the command")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "activation blocked — no dry run on record") {
		t.Errorf("the server's reason should reach the user, got: %v", err)
	}
	if stdout != "" {
		t.Errorf("a refused enable should print nothing on stdout, got:\n%s", stdout)
	}
}

// A table with no composite spec revision is a 404 from the latest-revision
// route, and nothing is sent.
func TestEnableLinkedTable_TableWithoutARevisionIsNotFound(t *testing.T) {
	mock := enableEnv(t)
	mock.RegisterHandler(http.MethodGet, ltLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
			"detail": "latest composite spec revision for dataset 12 can not be found.",
		})
	})

	_, _, err := execEnable(t, "enable", "linked-table", "12")
	if err == nil {
		t.Fatal("expected an error for a table with no spec revision")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitNotFound, err)
	}
	if p := posts(mock); len(p) != 0 {
		t.Errorf("sent an enable although the table has no revision: %v", p)
	}
}

// Malformed flags exit 2 before any request.
func TestEnableLinkedTable_RejectsBadFlagsBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":           {},
		"table twice":        {"12", "--table-id", "12"},
		"malformed revision": {"12", "--revision", "5d0c2a4e"},
		"empty revision":     {"12", "--revision", ""},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			mock := enableLinkedTableEnv(t, "draft")
			registerEnable(mock, ltLatestRevision)

			_, _, err := execEnable(t, append([]string{"enable", "linked-table"}, flags...)...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("expected no request, got %v", reqs)
			}
		})
	}
}

// A table name and a release carrying terminal control sequences are printed
// escaped.
func TestEnableLinkedTable_EscapesControlCharacters(t *testing.T) {
	mock := enableEnv(t)
	mock.RegisterHandler(http.MethodGet, ltLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": ltLatestRevision,
			"composite_spec_group_id":    ltGroup,
			"state":                      "draft",
			"release":                    "v3\x1b[2J",
			"description":                nil,
			"spec_sha256":                "ab12",
			"created_at":                 "2026-09-22T10:00:00Z",
			"updated_at":                 "2026-09-22T10:00:00Z",
		})
	})
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []map[string]interface{}{{"id": 12, "name": "orders\x1b[31m", "state": "active"}},
		})
	})
	registerEnable(mock, ltLatestRevision)

	stdout, stderr, err := execEnable(t, "enable", "linked-table", "--table", "orders\x1b[31m")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if strings.ContainsRune(stdout, '\x1b') {
		t.Errorf("stdout carries a raw escape sequence:\n%q", stdout)
	}
	for _, want := range []string{"linked-table/orders", "revision : v3"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout should contain %q, got:\n%q", want, stdout)
		}
	}
}
