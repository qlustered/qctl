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
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupTableKindsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewTableKindsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetKindTiny() dataset_kinds.DatasetKindTiny {
	return dataset_kinds.DatasetKindTiny{
		ID:        uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Slug:      "car-policy-bordereau",
		Name:      "Car Policy Bordereau",
		IsBuiltin: false,
		UpdatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

func TestGetTableKindsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockKinds          []dataset_kinds.DatasetKindTiny
		wantErr            bool
		wantOutputContains []string
		wantOutputExcludes []string
	}{
		{
			name:               "successful list with default output",
			args:               []string{},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"car-policy-bordereau"},
		},
		{
			name:               "with json output",
			args:               []string{"--output", "json"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{`"slug": "car-policy-bordereau"`},
		},
		{
			name:      "empty results",
			args:      []string{},
			mockKinds: []dataset_kinds.DatasetKindTiny{},
			wantErr:   false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "invalid_field"},
			wantErr: true,
		},
		{
			name:               "with search filter",
			args:               []string{"--search", "car"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"car-policy-bordereau"},
		},
		{
			name:               "json output carries the raw full id and RFC3339 updated_at",
			args:               []string{"--output", "json"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{`"id": "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"`, `"updated_at": "2025-06-15T10:00:00Z"`},
			wantOutputExcludes: []string{`"short_id"`, `"tags"`},
		},
		{
			name:               "default table keeps SHORT-ID and does not print the full id",
			args:               []string{},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"aaaa0000"},
			wantOutputExcludes: []string{"aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"},
		},
		{
			name:               "--columns id is accepted",
			args:               []string{"--columns", "slug,id"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockKinds)
				page := 1
				response := dataset_kinds.DatasetKindsPage{
					Results:   tt.mockKinds,
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupTableKindsTestCommand()
			args := append([]string{"get", "table-kinds"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()

			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
			}

			if !tt.wantErr {
				output := buf.String()
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(output, want) {
						t.Errorf("Output should contain %q, got: %s", want, output)
					}
				}

				for _, want := range tt.wantOutputExcludes {
					if strings.Contains(output, want) {
						t.Errorf("output should not contain %q, got:\n%s", want, output)
					}
				}
			}
		})
	}
}

func TestGetTableKindsCommand_ReverseAndBuiltinFilterOnWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var query string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		totalRows := 0
		testutil.RespondJSON(w, http.StatusOK, dataset_kinds.DatasetKindsPage{
			Results: []dataset_kinds.DatasetKindTiny{}, TotalRows: &totalRows,
		})
	})
	cmd := setupTableKindsTestCommand()
	cmd.SetArgs([]string{"get", "table-kinds", "--reverse", "--include-builtin=false"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	if values.Get("reverse") != "true" || values.Get("include_builtin") != "false" {
		t.Errorf("query = %q, want reverse=true and include_builtin=false", query)
	}
}

func TestGetTableKindsCommand_CustomizedBuiltinTag(t *testing.T) {
	kind := sampleDatasetKindTiny()
	kind.IsBuiltin = true
	kind.IsCustomized = true
	display := dataset_kinds.ToDisplayList([]dataset_kinds.DatasetKindTiny{kind})
	if display[0].Tags != "Built-in, Customized" {
		t.Errorf("tags = %q, want Built-in, Customized", display[0].Tags)
	}
}

// TestGetTableKindsCommand_EmptySearchStillSendsParam pins the deliberate
// exception documented in parseTableKindsParams: unlike its eleven siblings,
// get table-kinds forwards an explicit --search "" as an empty search_query
// on the wire. Has (not Get) is what proves the parameter still exists --
// Get returns "" for both an empty value and a missing key.
func TestGetTableKindsCommand_EmptySearchStillSendsParam(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var searchQueryPresent bool
	var capturedSearchQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		searchQueryPresent = r.URL.Query().Has("search_query")
		capturedSearchQuery = r.URL.Query().Get("search_query")

		totalRows := 0
		page := 1
		response := dataset_kinds.DatasetKindsPage{
			Results:   []dataset_kinds.DatasetKindTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableKindsTestCommand()
	cmd.SetArgs([]string{"get", "table-kinds", "--search", ""})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !searchQueryPresent {
		t.Error("expected search_query query parameter to be present for --search \"\", but it was absent")
	}
	if capturedSearchQuery != "" {
		t.Errorf("expected empty search_query value, got %q", capturedSearchQuery)
	}
}

// newTableKindsTestEnv returns a mock server with a config and credential
// pointing at it.
func newTableKindsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// pagedTableKind is table kind n of a paged listing.
func pagedTableKind(n int) interface{} {
	kind := sampleDatasetKindTiny()
	kind.ID = uuid.MustParse(fmt.Sprintf("aaaa0000-bbbb-cccc-dddd-%012d", n))
	kind.Slug = fmt.Sprintf("kind-%d", n)
	return kind
}

// assertCatalogListPaging fails unless list is a List whose page, limit, total and
// next_page are the given values.
func assertCatalogListPaging(t *testing.T, list output.List, page, limit, total int, next *int) {
	t.Helper()
	if list.Kind != "List" {
		t.Errorf("kind = %q, want List", list.Kind)
	}
	for name, pair := range map[string][2]*int{
		"page": {list.Page, &page}, "limit": {list.Limit, &limit},
		"total": {list.Total, &total}, "next_page": {list.NextPage, next},
	} {
		if got, want := pair[0], pair[1]; (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%s = %v, want %v", name, deref(got), deref(want))
		}
	}
}

// TestGetTableKindsCommand_Pagination serves pages the way atlas's OFFSET path
// does: total_rows and no next. The total alone has to say a page follows.
func TestGetTableKindsCommand_Pagination(t *testing.T) {
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
			wantHint: "Showing 1-2 of 5 table kinds (page 1 of 3). More available: use --page 2 or add filters.\n"},
		{name: "name hints on stderr", format: "name", page: "1",
			wantHint: "Showing 1-2 of 5 table kinds (page 1 of 3). More available: use --page 2 or add filters.\n"},
		{name: "table last page has no hint", format: "table", page: "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newTableKindsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", testutil.MockOffsetPagedHandler(5, pagedTableKind))

			cmd := setupTableKindsTestCommand()
			cmd.SetArgs([]string{"get", "table-kinds", "--limit", "2", "--page", tt.page, "-o", tt.format})
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

// TestGetTableKindsCommand_JSONItemsAreRaw pins that -o json items are the API
// rows: every key the server sent and none of the table's projection.
func TestGetTableKindsCommand_JSONItemsAreRaw(t *testing.T) {
	mock := newTableKindsTestEnv(t)
	kind := sampleDatasetKindTiny()
	kind.IsBuiltin, kind.IsCustomized = true, true
	kind.OrganizationID = uuid.MustParse(testOrgID)
	kind.DatasetCount = 12
	kind.Concepts = []api.ConceptRefSchema{}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, dataset_kinds.DatasetKindsPage{
			Results: []dataset_kinds.DatasetKindTiny{kind}, TotalRows: &total, Page: &page,
		})
	})

	cmd := setupTableKindsTestCommand()
	cmd.SetArgs([]string{"get", "table-kinds", "-o", "json"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var items []map[string]interface{}
	decodeListItems(t, stdout.Bytes(), &items)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1:\n%s", len(items), stdout.String())
	}
	want := map[string]interface{}{
		"id":              "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001",
		"organization_id": testOrgID,
		"slug":            "car-policy-bordereau",
		"name":            "Car Policy Bordereau",
		"is_builtin":      true,
		"is_customized":   true,
		"updated_at":      "2025-06-15T10:00:00Z",
		"concepts":        []interface{}{},
		"dataset_count":   float64(12),
	}
	if !reflect.DeepEqual(items[0], want) {
		t.Errorf("item = %v\nwant   %v", items[0], want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing under -o json", stderr.String())
	}
}

func TestGetTableKindsCommand_ServerError(t *testing.T) {
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
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tt.status, http.StatusText(tt.status))
			})

			cmd := setupTableKindsTestCommand()
			cmd.SetArgs([]string{"get", "table-kinds"})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			if code := qerrors.GetExitCode(cmd.Execute()); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}
