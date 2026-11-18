package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupStandardFieldCommand() *cobra.Command {
	root := setupTableKindsTestCommand()
	getCmd, _, _ := root.Find([]string{"get"})
	getCmd.AddCommand(NewStandardFieldsCommand(), NewStandardFieldCommand())
	return root
}

func TestGetStandardFieldsEmptyTableKindMakesNoRequests(t *testing.T) {
	mock := newTableKindsTestEnv(t)
	cmd := setupStandardFieldCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"get", "standard-fields", "--table-kind", ""})
	err := cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2; error: %v", code, err)
	}
	if !strings.Contains(err.Error(), "--table-kind cannot be empty") {
		t.Errorf("error does not name the empty filter: %v", err)
	}
	if count := len(mock.Requests()); count != 0 {
		t.Errorf("made %d requests, want 0", count)
	}
}

func TestGetConceptsFlagsOnWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		count := 0
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{}, TotalRows: &count})
	})
	cmd := setupStandardFieldCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "standard-fields", "--reverse", "--include-builtin=false", "--search", "policy", "--order-by", "slug", "--page", "2", "--limit", "12"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"reverse": "true", "include_builtin": "false", "search_query": "policy", "order_by": "slug", "page": "2", "limit": "12"} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// pagedConcept is concept n of a paged listing.
func pagedConcept(n int) interface{} {
	return concepts.Tiny{
		ID:             uuid.MustParse(fmt.Sprintf("bbbb0000-cccc-dddd-eeee-%012d", n)),
		OrganizationID: uuid.MustParse(testOrgID),
		Slug:           fmt.Sprintf("concept-%d", n),
		Name:           fmt.Sprintf("Concept %d", n),
		UpdatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

// TestGetConceptsPagination serves pages the way atlas's OFFSET path does:
// total_rows and no next. The total alone has to say a page follows.
func TestGetConceptsPagination(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		page     string
		wantNext *int
		wantHint string
	}{
		{name: "json carries the paging in the envelope", format: "json", page: "1", wantNext: intPtr(2)},
		{name: "yaml carries the paging in the envelope", format: "yaml", page: "1", wantNext: intPtr(2)},
		{name: "json last page has a null next_page", format: "json", page: "3"},
		{name: "table hints on stderr", format: "table", page: "1",
			wantHint: "Showing 1-2 of 5 standard fields (page 1 of 3). More available: use --page 2 or add filters.\n"},
		{name: "name hints on stderr", format: "name", page: "1",
			wantHint: "Showing 1-2 of 5 standard fields (page 1 of 3). More available: use --page 2 or add filters.\n"},
		{name: "table last page has no hint", format: "table", page: "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newTableKindsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", testutil.MockOffsetPagedHandler(5, pagedConcept))

			cmd := setupStandardFieldCommand()
			cmd.SetArgs([]string{"get", "standard-fields", "--limit", "2", "--page", tt.page, "-o", tt.format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if stderr.String() != tt.wantHint {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantHint)
			}
			var list output.List
			switch tt.format {
			case "json":
				if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
					t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout.String())
				}
			case "yaml":
				if err := yaml.Unmarshal(stdout.Bytes(), &list); err != nil {
					t.Fatalf("-o yaml did not decode as a List: %v\n%s", err, stdout.String())
				}
			default:
				return
			}
			page, _ := strconv.Atoi(tt.page)
			assertCatalogListPaging(t, list, page, 2, 5, tt.wantNext)
		})
	}
}

// TestGetConceptsJSONItemsAreRaw pins that -o json items are the API rows:
// every key the server sent and none of the table's projection.
func TestGetConceptsJSONItemsAreRaw(t *testing.T) {
	mock := newTableKindsTestEnv(t)
	fieldType := api.FieldTypeString
	typed := pagedConcept(1).(concepts.Tiny)
	typed.ExpectedFieldType = &fieldType
	typed.Aliases = []string{"Alternate name"}
	typed.ProblemDomains = []api.ProblemDomainRefSchema{}
	typed.IsBuiltin, typed.IsCustomized = true, true
	untyped := pagedConcept(2).(concepts.Tiny)
	untyped.Aliases = []string{}
	untyped.ProblemDomains = []api.ProblemDomainRefSchema{}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		total, page := 2, 1
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{typed, untyped}, TotalRows: &total, Page: &page})
	})

	cmd := setupStandardFieldCommand()
	cmd.SetArgs([]string{"get", "standard-fields", "-o", "json"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var list output.List
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout.String())
	}
	assertCatalogListPaging(t, list, 1, 50, 2, nil)
	var items []map[string]interface{}
	decodeListItems(t, stdout.Bytes(), &items)
	want := []map[string]interface{}{
		{
			"id": "bbbb0000-cccc-dddd-eeee-000000000001", "organization_id": testOrgID,
			"slug": "concept-1", "name": "Concept 1", "expected_field_type": "String",
			"aliases": []interface{}{"Alternate name"}, "problem_domains": []interface{}{},
			"is_builtin": true, "is_customized": true, "updated_at": "2025-06-15T10:00:00Z",
		},
		{
			"id": "bbbb0000-cccc-dddd-eeee-000000000002", "organization_id": testOrgID,
			"slug": "concept-2", "name": "Concept 2",
			"aliases": []interface{}{}, "problem_domains": []interface{}{},
			"is_builtin": false, "is_customized": false, "updated_at": "2025-06-15T10:00:00Z",
		},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %v\nwant    %v", items, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing under -o json", stderr.String())
	}
}

// TestGetConceptsTableKeepsTheProjection pins that the table still shows the
// display row: the joined tags and "-" for a concept with no field type.
func TestGetConceptsTableKeepsTheProjection(t *testing.T) {
	mock := newTableKindsTestEnv(t)
	builtin := pagedConcept(1).(concepts.Tiny)
	builtin.IsBuiltin, builtin.IsCustomized = true, true
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		total := 1
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{builtin}, TotalRows: &total})
	})

	cmd := setupStandardFieldCommand()
	cmd.SetArgs([]string{"get", "standard-fields", "--columns", "slug,expected_field_type,tags,short_id"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"concept-1", "Built-in, Customized", "bbbb0000"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "bbbb0000-cccc") {
		t.Errorf("table shows the full id under SHORT-ID:\n%s", stdout.String())
	}
}

func TestGetConceptsServerErrorExitCode(t *testing.T) {
	tests := []struct {
		status   int
		wantCode int
	}{
		{http.StatusInternalServerError, qerrors.ExitGenericError},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			mock := newTableKindsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tt.status, http.StatusText(tt.status))
			})

			cmd := setupStandardFieldCommand()
			cmd.SetArgs([]string{"get", "standard-fields"})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			if code := qerrors.GetExitCode(cmd.Execute()); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestGetConceptsShortSearchExitsTwo(t *testing.T) {
	cmd := setupStandardFieldCommand()
	cmd.SetArgs([]string{"get", "standard-fields", "--search", "ab"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if code := qerrors.GetExitCode(cmd.Execute()); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestGetConceptsResolvesTableKindFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	kindID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
		count := 1
		testutil.RespondJSON(w, http.StatusOK, dataset_kinds.DatasetKindsPage{Results: []dataset_kinds.DatasetKindTiny{{ID: kindID, Slug: "car-policy-bordereau", Name: "Car Policy Bordereau"}}, TotalRows: &count})
	})
	var filter string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("problem_domain_id")
		count := 0
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{}, TotalRows: &count})
	})
	cmd := setupStandardFieldCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"get", "standard-fields", "--table-kind", "car-policy-bordereau"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if filter != kindID.String() {
		t.Fatalf("problem_domain_id = %q, want %s", filter, kindID)
	}
}

func TestGetConceptByUUIDAndNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Full{ID: id, OrganizationID: uuid.MustParse(testOrgID), Slug: "policy-number", Name: "Policy Number", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	cmd := setupStandardFieldCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "standard-field", id.String(), "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"slug": "policy-number"`)) {
		t.Fatalf("output = %s", out.String())
	}
	if _, err := concepts.ParseManifest(out.Bytes()); err != nil {
		t.Fatalf("get standard-field JSON is not apply-compatible: %v\n%s", err, out.String())
	}

	other := setupStandardFieldCommand()
	other.SetOut(&bytes.Buffer{})
	other.SetErr(&bytes.Buffer{})
	other.SetArgs([]string{"get", "standard-field", "does-not-exist"})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		count := 0
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{}, TotalRows: &count})
	})
	if code := qerrors.GetExitCode(other.Execute()); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want 3", code)
	}
}
