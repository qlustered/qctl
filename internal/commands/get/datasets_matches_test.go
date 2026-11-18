package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

var (
	matchOldRevision = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	matchDefaultRev  = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
)

func setupDatasetMatchServer(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	revs := []api.RuleRevisionTinySchema{
		{ID: matchOldRevision, Name: "fleet", Slug: "fleet", Release: "1.0.0", IsDefault: false},
		{ID: matchDefaultRev, Name: "fleet", Slug: "fleet", Release: "2.0.0", IsDefault: true},
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{Results: revs, TotalRows: testutil.IntPtr(2)})
	})
	for _, rev := range revs {
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String(), func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionsListSchema{Name: "fleet", Results: revs})
		})
	}
}

func runDatasetMatchCommand(t *testing.T, mock *testutil.MockAPIServer, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	cmd := setupTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"get", "tables"}, args...))
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestDatasetMatchesDefaultAndPartial(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	setupDatasetMatchServer(t, mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+matchDefaultRev.String()+"/dataset-matches", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RawQuery; got != "limit=20&page=1" {
			t.Errorf("query = %q", got)
		}
		testutil.RespondJSON(w, http.StatusOK, api.DatasetMatchListSchema{
			Results: []api.DatasetMatchSchema{{
				DatasetID: 42, Name: "orders", MatchKind: "partial", MatchedConceptCount: 1,
				RequiredConceptCount: 2, MissingConcepts: []api.RuleRevisionConceptSchema{{Slug: "vehicle-id"}},
			}},
			TotalRows: testutil.IntPtr(1), Page: testutil.IntPtr(1), PartialCount: 1,
		})
	})
	out, errOut, err := runDatasetMatchCommand(t, mock, "--for-rule", "fleet")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"NAME", "MATCH", "STANDARD-FIELDS", "MISSING", "orders", "partial", "1/2", "vehicle-id"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
	if !strings.Contains(errOut, "Full: 0  Partial: 1") {
		t.Errorf("missing counts: %s", errOut)
	}
	jsonOut, jsonErr, err := runDatasetMatchCommand(t, mock, "--for-rule", "fleet", "-o", "json")
	if err != nil {
		t.Fatalf("JSON Execute: %v", err)
	}
	if !strings.Contains(jsonErr, "Match counts: Full:") {
		t.Errorf("-o json stderr = %q, want the match counts the envelope cannot carry", jsonErr)
	}
	var rows []map[string]interface{}
	decodeListItems(t, []byte(jsonOut), &rows)
	if len(rows) != 1 || rows[0]["dataset_id"] != float64(42) || rows[0]["matched_concept_count"] != float64(1) {
		t.Errorf("raw JSON rows = %#v", rows)
	}
	nameOut, _, err := runDatasetMatchCommand(t, mock, "--for-rule", "fleet", "-o", "name")
	if err != nil || nameOut != "table/42\n" {
		t.Errorf("name stdout = %q, err = %v", nameOut, err)
	}
}

func TestDatasetMatchesGenericAndNoGrants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		generic bool
		want    string
	}{
		{"generic", true, "requires no standard fields"},
		{"no grants", false, "Full: 0  Partial: 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+matchOldRevision.String()+"/dataset-matches", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DatasetMatchListSchema{
					IsGeneric: tc.generic, Results: []api.DatasetMatchSchema{}, TotalRows: testutil.IntPtr(0), Page: testutil.IntPtr(1),
				})
			})
			out, errOut, err := runDatasetMatchCommand(t, mock, "--for-rule", matchOldRevision.String())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if out != "" || !strings.Contains(errOut, tc.want) {
				t.Errorf("stdout = %q, stderr = %q", out, errOut)
			}
			jsonOut, jsonErr, err := runDatasetMatchCommand(t, mock, "--for-rule", matchOldRevision.String(), "-o", "json")
			if err != nil {
				t.Fatalf("JSON Execute: %v", err)
			}
			var items []any
			if decodeListItems(t, []byte(jsonOut), &items); len(items) != 0 {
				t.Errorf("JSON items = %v, want []", items)
			}
			if n := strings.Count(jsonErr, tc.want); n != 1 {
				t.Errorf("-o json stderr carries the footer %d times, want once: %q", n, jsonErr)
			}
		})
	}
}

func TestDatasetMatchReleaseAndFlagValidation(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	setupDatasetMatchServer(t, mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+matchOldRevision.String()+"/dataset-matches", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetMatchListSchema{Results: []api.DatasetMatchSchema{}, TotalRows: testutil.IntPtr(0)})
	})
	if _, _, err := runDatasetMatchCommand(t, mock, "--for-rule", "fleet", "--release", "1.0.0"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, _, err := runDatasetMatchCommand(t, mock, "--for-rule", "11111111"); err != nil {
		t.Fatalf("short revision ID: %v", err)
	}
	for _, args := range [][]string{
		{"--release", "1.0.0"},
		{"--for-rule", ""},
		{"--for-rule", "fleet", "--state", "active"},
		{"--for-rule", "fleet", "--search", "orders"},
		{"--for-rule", "fleet", "--order-by", "name"},
		{"--for-rule", "fleet", "--limit", "0"},
		{"--for-rule", "fleet", "--release", "9.0.0"},
	} {
		_, _, err := runDatasetMatchCommand(t, mock, args...)
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("args %q: exit = %d, err = %v", args, got, err)
		}
	}
}
