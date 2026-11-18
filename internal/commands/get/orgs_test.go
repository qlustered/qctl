package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/orgs"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestGetOrgsExplainsOperationsEndpointRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			key, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupConfigWithOrg(mock.URL(), "", orgAID)
			env.SetupCredential(key, orgAID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, status, map[string]string{"detail": "Not authenticated"})
			})
			cmd := setupOrgsTestCommand()
			cmd.SetArgs([]string{"get", "orgs"})
			err := cmd.Execute()
			wantExit := qerrors.ExitUnauthorized
			if status == http.StatusNotFound {
				wantExit = qerrors.ExitNotFound
			}
			if qerrors.GetExitCode(err) != wantExit {
				t.Fatalf("error = %v, want exit %d", err, wantExit)
			}
			for _, want := range []string{"operations staff", "browser cookie", "qctl auth me", "Active Organizations"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q: %v", want, err)
				}
			}
			for _, unwanted := range []string{"qctl auth login", "Scoped to organization"} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("misleading advice: %v", err)
				}
			}
		})
	}
}

const (
	orgAID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	orgBID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func setupOrgsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.PersistentFlags().String("org", "", "Organization")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewOrgsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleOrgItem(id, name string) orgs.OrgItem {
	return orgs.OrgItem{
		ID:        uuid.MustParse(id),
		Name:      name,
		IsActive:  true,
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func TestGetOrgsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockOrgs           []orgs.OrgItem
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:               "successful list with default output",
			args:               []string{},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme"), sampleOrgItem(orgBID, "widgets")},
			wantOutputContains: []string{"acme", "widgets", "CURRENT"},
		},
		{
			name:               "json output",
			args:               []string{"--output", "json"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{`"name": "acme"`, `"is_active": true`},
		},
		{
			name:               "yaml output",
			args:               []string{"--output", "yaml"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"name: acme", "is_active: true"},
		},
		{
			name:               "custom columns",
			args:               []string{"--columns", "name,id"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"NAME", "ID", "acme"},
		},
		{
			name:               "search query",
			args:               []string{"--search", "acme"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"acme"},
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "bogus"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", orgAID)
			env.SetupCredential(endpointKey, orgAID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockOrgs)
				page := 1
				response := orgs.OrgList{
					Results:   tt.mockOrgs,
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupOrgsTestCommand()
			args := append([]string{"get", "orgs"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			output := buf.String()
			for _, want := range tt.wantOutputContains {
				if !strings.Contains(output, want) {
					t.Errorf("Output should contain %q, got: %s", want, output)
				}
			}
		})
	}
}

// newOrgsTestEnv returns a mock server with a config whose org is orgAID and
// a credential pointing at it.
func newOrgsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "", orgAID)
	env.SetupCredential(endpointKey, orgAID, "test-token")
	return mock
}

// orgsServeTwo serves acme (orgAID, the current org) and widgets (orgBID).
func orgsServeTwo(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, _ *http.Request) {
		totalRows := 2
		page := 1
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{sampleOrgItem(orgAID, "acme"), sampleOrgItem(orgBID, "widgets")},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})
}

// orgsRun runs qctl with args and returns its stdout and stderr.
func orgsRun(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := setupOrgsTestCommand()
	cmd.SetArgs(args)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.String(), errOut.String()
}

// TestGetOrgsCommand_CurrentMarker pins that the table marks the current org
// with "*" under CURRENT and leaves the others blank.
func TestGetOrgsCommand_CurrentMarker(t *testing.T) {
	orgsServeTwo(newOrgsTestEnv(t))

	stdout, _ := orgsRun(t, "get", "orgs", "--columns", "current,name")

	current := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "CURRENT" {
			continue
		}
		current[fields[len(fields)-1]] = strings.Join(fields[:len(fields)-1], " ")
	}
	if want := map[string]string{"acme": "*", "widgets": ""}; !reflect.DeepEqual(current, want) {
		t.Errorf("CURRENT by name = %q, want %q\n%s", current, want, stdout)
	}
}

// TestGetOrgsCommand_JSONItemsAreRaw pins that -o json items are the API rows:
// the full id, an RFC3339 created_at, updated_at, and no current marker.
func TestGetOrgsCommand_JSONItemsAreRaw(t *testing.T) {
	orgsServeTwo(newOrgsTestEnv(t))

	stdout, stderr := orgsRun(t, "get", "orgs", "-o", "json")

	var list output.List
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout)
	}
	assertCatalogListPaging(t, list, 1, 1000, 2, nil)
	var items []map[string]interface{}
	decodeListItems(t, []byte(stdout), &items)
	want := []map[string]interface{}{
		{"id": orgAID, "name": "acme", "is_active": true, "created_at": "2025-06-15T10:00:00Z", "updated_at": "2025-06-20T14:00:00Z"},
		{"id": orgBID, "name": "widgets", "is_active": true, "created_at": "2025-06-15T10:00:00Z", "updated_at": "2025-06-20T14:00:00Z"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %v\nwant    %v", items, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing under -o json", stderr)
	}
}

// TestGetOrgsCommand_NamePrintsOrgNames pins -o name: one org name a line.
func TestGetOrgsCommand_NamePrintsFullOrgIDs(t *testing.T) {
	orgsServeTwo(newOrgsTestEnv(t))

	stdout, stderr := orgsRun(t, "get", "orgs", "-o", "name")

	if want := "org/b2c3d4e5-f6a7-8901-bcde-f23456789012\norg/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing when every org is shown", stderr)
	}
}

// TestGetOrgsCommand_EmptyJSONIsAList pins that no orgs under -o json is a List
// with no items.
func TestGetOrgsCommand_EmptyJSONIsAList(t *testing.T) {
	mock := newOrgsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", testutil.MockOffsetPagedHandler(0, orgsPagedItem))

	stdout, stderr := orgsRun(t, "get", "orgs", "-o", "json")

	var list output.List
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout)
	}
	assertCatalogListPaging(t, list, 1, 1000, 0, nil)
	var items []map[string]interface{}
	decodeListItems(t, []byte(stdout), &items)
	if items == nil || len(items) != 0 {
		t.Errorf("items = %v, want []", items)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing under -o json", stderr)
	}
}

func TestGetOrgsCommand_ServerMessages(t *testing.T) {
	message := `**"missing"** was not used<br>because it did not match any values in the columns we are searching in.`
	emptyMessage := ""
	unsafeMessage := "**notice**<br>server\x1b[31m text\r"
	items := []orgs.OrgItem{sampleOrgItem(orgAID, "acme"), sampleOrgItem(orgBID, "widgets")}
	tests := []struct {
		name    string
		search  string
		message *string
		items   []orgs.OrgItem
		wantMsg string
	}{
		{name: "search notice with rows", search: "missing", message: &message, items: items,
			wantMsg: "\"missing\" was not used\nbecause it did not match any values in the columns we are searching in.\n"},
		{name: "notice without search", message: &message, items: items,
			wantMsg: "\"missing\" was not used\nbecause it did not match any values in the columns we are searching in.\n"},
		{name: "notice with empty page", search: "missing", message: &message, items: []orgs.OrgItem{},
			wantMsg: "\"missing\" was not used\nbecause it did not match any values in the columns we are searching in.\n"},
		{name: "ordinary no match", search: "missing", items: []orgs.OrgItem{}},
		{name: "empty message", message: &emptyMessage, items: items},
		{name: "terminal controls", message: &unsafeMessage, items: items, wantMsg: "notice\nserver\\x1b[31m text\\x0d\n"},
	}
	for _, tt := range tests {
		for _, format := range []string{"table", "name", "json", "yaml"} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				mock := newOrgsTestEnv(t)
				total := 0
				if len(tt.items) > 0 {
					total = 3
				}
				mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
					if got := r.URL.Query().Get("search_query"); got != tt.search {
						t.Errorf("search_query = %q, want %q", got, tt.search)
					}
					testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{Msg: tt.message, Results: tt.items, TotalRows: &total})
				})
				args := []string{"get", "orgs", "--limit", "2", "-o", format}
				if tt.search != "" {
					args = append(args, "--search", tt.search)
				}
				stdout, stderr := orgsRun(t, args...)
				wantErr := tt.wantMsg
				if format == "table" && total == 0 {
					wantErr += fmt.Sprintf("No orgs matched --search %q in org %q (context: \"default\").\n", tt.search, orgAID)
				} else if (format == "table" || format == "name") && total > 0 {
					wantErr += "Showing 1-2 of 3 orgs (page 1 of 2). More available: use --page 2 or add filters.\n"
				}
				if stderr != wantErr {
					t.Errorf("stderr = %q, want %q", stderr, wantErr)
				}
				if total == 0 && (format == "table" || format == "name") {
					if stdout != "" {
						t.Errorf("stdout = %q, want empty", stdout)
					}
					return
				}
				switch format {
				case "table":
					for _, item := range tt.items {
						if !strings.Contains(stdout, item.Name) {
							t.Errorf("stdout omitted %q: %s", item.Name, stdout)
						}
					}
				case "name":
					if want := "org/" + orgAID + "\norg/" + orgBID + "\n"; stdout != want {
						t.Errorf("stdout = %q, want %q", stdout, want)
					}
				default:
					var list output.List
					if format == "json" {
						if err := json.Unmarshal([]byte(stdout), &list); err != nil {
							t.Fatalf("invalid JSON stdout: %v\n%s", err, stdout)
						}
					} else if err := yaml.Unmarshal([]byte(stdout), &list); err != nil {
						t.Fatalf("invalid YAML stdout: %v\n%s", err, stdout)
					}
					var next *int
					if total > 0 {
						next = intPtr(2)
					}
					assertCatalogListPaging(t, list, 1, 2, total, next)
					got, err := json.Marshal(list.Items)
					if err != nil {
						t.Fatal(err)
					}
					var gotItems []orgs.OrgItem
					if err := json.Unmarshal(got, &gotItems); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(gotItems, tt.items) {
						t.Errorf("items = %#v, want %#v", gotItems, tt.items)
					}
				}
			})
		}
	}
}

// useContextOverride does what root's initializer does with --context.
func useContextOverride(t *testing.T, name string) {
	t.Helper()
	config.SetContextOverride(name)
	t.Cleanup(func() { config.SetContextOverride("") })
}

func TestGetOrgsCommand_ContextOverride(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mockPrimary := testutil.NewMockAPIServer()
	defer mockPrimary.Close()

	mockOther := testutil.NewMockAPIServer()
	defer mockOther.Close()

	// Primary context — should NOT be queried when --context override is used.
	mockPrimary.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("primary context was queried despite --context override")
		testutil.RespondError(w, http.StatusInternalServerError, "should not be called")
	})

	// Other context — this is the one we expect to hit.
	var otherHit bool
	mockOther.RegisterHandler("GET", "/api/orgs/"+orgBID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		otherHit = true
		totalRows := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{sampleOrgItem(orgBID, "widgets")},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	primaryKey, _ := config.NormalizeEndpointKey(mockPrimary.Server.URL)
	otherKey, _ := config.NormalizeEndpointKey(mockOther.Server.URL)
	env.SetupCredential(primaryKey, orgAID, "primary-token")
	env.SetupCredential(otherKey, orgBID, "other-token")

	env.SetupMultipleContexts(map[string]*config.Context{
		"primary": {Server: mockPrimary.Server.URL, Organization: orgAID},
		"other":   {Server: mockOther.Server.URL, Organization: orgBID},
	}, "primary")

	useContextOverride(t, "other")
	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !otherHit {
		t.Error("expected --context override to query the other context's API")
	}
	if !strings.Contains(buf.String(), "widgets") {
		t.Errorf("expected widgets in output, got: %s", buf.String())
	}

	// Confirm config on disk is NOT modified.
	config.SetContextOverride("")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CurrentContext != "primary" {
		t.Errorf("context override should not persist; current = %q", cfg.CurrentContext)
	}
}

func TestGetOrgsCommand_ContextOverride_Unknown(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.SetupConfigWithOrg("https://api.example.com", "", orgAID)

	useContextOverride(t, "does-not-exist")
	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("expected error to mention the unknown context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("expected error to list available contexts, got: %v", err)
	}
}

// orgsPagedItem is org n of a paged listing.
func orgsPagedItem(n int) interface{} {
	return sampleOrgItem(fmt.Sprintf("cccc0000-dddd-eeee-ffff-%012d", n), fmt.Sprintf("org-%d", n))
}

// TestGetOrgsCommand_Pagination serves pages the way atlas's OFFSET path does:
// total_rows and no next. The total alone has to say a page follows.
func TestGetOrgsCommand_Pagination(t *testing.T) {
	const hint = "Showing 1-2 of 5 orgs (page 1 of 3). More available: use --page 2 or add filters.\n"
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
		{name: "table hints on stderr", format: "table", page: "1", wantHint: hint},
		{name: "name hints on stderr", format: "name", page: "1", wantHint: hint},
		{name: "table last page has no hint", format: "table", page: "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newOrgsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", testutil.MockOffsetPagedHandler(5, orgsPagedItem))

			stdout, stderr := orgsRun(t, "get", "orgs", "--limit", "2", "--page", tt.page, "-o", tt.format)

			if stderr != tt.wantHint {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantHint)
			}
			var list output.List
			switch tt.format {
			case "json":
				if err := json.Unmarshal([]byte(stdout), &list); err != nil {
					t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout)
				}
			case "yaml":
				if err := yaml.Unmarshal([]byte(stdout), &list); err != nil {
					t.Fatalf("-o yaml did not decode as a List: %v\n%s", err, stdout)
				}
			default:
				return
			}
			page, _ := strconv.Atoi(tt.page)
			assertCatalogListPaging(t, list, page, 2, 5, tt.wantNext)
		})
	}
}

// TestGetOrgsCommand_BadUsageExitsTwo pins that a rejected flag exits 2
// without a request.
func TestGetOrgsCommand_BadUsageExitsTwo(t *testing.T) {
	for _, args := range [][]string{
		{"--order-by", "bogus"},
		{"--limit", "0"},
		{"--page", "0"},
		{"--search", "ab"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := newOrgsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, _ *http.Request) {
				t.Errorf("request sent for rejected flags %v", args)
				testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{})
			})
			cmd := setupOrgsTestCommand()
			cmd.SetArgs(append([]string{"get", "orgs"}, args...))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if code := qerrors.GetExitCode(cmd.Execute()); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestGetOrgsCommand_ServerErrorExitCode pins the exit code of a 500.
func TestGetOrgsCommand_ServerErrorExitCode(t *testing.T) {
	mock := newOrgsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", testutil.MockInternalServerErrorHandler())
	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "-o", "json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if code := qerrors.GetExitCode(cmd.Execute()); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
	}
}

// TestGetOrgsCommand_EmptyResultRejectsUnknownColumn pins the tableui print
// path end to end: before printEmptyResult validated --columns, a zero-row
// result short-circuited straight to "No orgs found..." without ever
// constructing the printer that would have caught an unknown column name.
func TestGetOrgsCommand_EmptyResultRejectsUnknownColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", orgAID)
	env.SetupCredential(endpointKey, orgAID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 0
		page := 1
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "--output", "table", "--columns", "bogus_column"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown column "bogus_column"`) {
		t.Fatalf("expected an unknown-column error on an empty result, got %v", err)
	}
	if strings.Contains(buf.String(), "No orgs found") {
		t.Errorf("must not print the empty-result message when --columns is invalid, got: %q", buf.String())
	}
}
