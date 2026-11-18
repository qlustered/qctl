package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testKindID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"

func setupTableKindGetTestCommand() *cobra.Command {
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
	// The real root's --columns check; this command's RunE does not repeat it.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateDeclaredColumns(cmd)
	}

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewTableKindGetCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetKindWithConcepts() dataset_kinds.DatasetKindWithConcepts {
	aliases := []string{"policynumber", "policy_number", "policy #"}
	fieldType := api.FieldTypeString
	concepts := []dataset_kinds.DatasetConceptFull{
		{
			ID:                uuid.MustParse("cccc0000-dddd-eeee-ffff-000000000001"),
			OrganizationID:    uuid.MustParse(testOrgID),
			Slug:              "policy_number",
			Name:              "Policy Number",
			Aliases:           &aliases,
			ExpectedFieldType: &fieldType,
			CreatedAt:         time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:         time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		},
	}

	return dataset_kinds.DatasetKindWithConcepts{
		ID:        uuid.MustParse(testKindID),
		Slug:      "car-policy-bordereau",
		Name:      "Car Policy Bordereau",
		IsBuiltin: false,
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		Concepts:  &concepts,
	}
}

func registerKindListHandler(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_kinds.DatasetKindsPage{
			Results: []dataset_kinds.DatasetKindTiny{
				{
					ID:        uuid.MustParse(testKindID),
					Slug:      "car-policy-bordereau",
					Name:      "Car Policy Bordereau",
					IsBuiltin: false,
					UpdatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				},
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})
}

func registerKindDetailHandler(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+testKindID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetKindWithConcepts())
	})
}

func TestGetTableKindYAMLExportsApplyManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	registerKindDetailHandler(mock)
	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-o", "yaml"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	m, err := dataset_kinds.ParseManifest(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Slug != "car-policy-bordereau" || m.Spec.Name != "Car Policy Bordereau" || len(m.Spec.StandardFields) != 1 || m.Spec.StandardFields[0] != "policy_number" || m.Status == nil || m.Status.ID.String() != testKindID {
		t.Errorf("manifest = %#v", m)
	}
}

func TestGetTableKindCommand_CustomizedBuiltinHeader(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+testKindID, func(w http.ResponseWriter, _ *http.Request) {
		kind := sampleDatasetKindWithConcepts()
		kind.IsBuiltin = true
		kind.IsCustomized = true
		testutil.RespondJSON(w, http.StatusOK, kind)
	})
	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Built-in:   yes") || !strings.Contains(out.String(), "Customized: yes") {
		t.Errorf("header should show customized built-in, got: %s", out.String())
	}
}

func TestGetTableKindCommand_BySlug(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindListHandler(mock)
	registerKindDetailHandler(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", "car-policy-bordereau"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Car Policy Bordereau") {
		t.Errorf("Output should contain kind name, got: %s", output)
	}
	if !strings.Contains(output, "policy_number") {
		t.Errorf("Output should contain concept slug, got: %s", output)
	}
	if !strings.Contains(output, "EXPECTED-FIELD-TYPE") || !strings.Contains(output, "String") {
		t.Errorf("Output should show the concept's expected field type, got: %s", output)
	}
}

func TestGetTableKindCommand_ByUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Car Policy Bordereau") {
		t.Errorf("Output should contain kind name, got: %s", output)
	}
}

func TestGetTableKindCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"slug": "car-policy-bordereau"`) {
		t.Errorf("JSON output should contain slug, got: %s", output)
	}
	if !strings.Contains(output, `"policy_number"`) {
		t.Errorf("JSON output should contain concept slug, got: %s", output)
	}
}

func TestGetTableKindCommand_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "slug: car-policy-bordereau") {
		t.Errorf("YAML output should contain slug, got: %s", output)
	}
}

// registerKindDetailHandlerNoConcepts serves a table kind whose concept
// list is empty — the path that short-circuits before the printer runs.
func registerKindDetailHandlerNoConcepts(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+testKindID, func(w http.ResponseWriter, r *http.Request) {
		kind := sampleDatasetKindWithConcepts()
		empty := []dataset_kinds.DatasetConceptFull{}
		kind.Concepts = &empty
		testutil.RespondJSON(w, http.StatusOK, kind)
	})
}

func runTableKindWithNoConcepts(t *testing.T, args ...string) (string, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandlerNoConcepts(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs(append([]string{"get", "table-kind", testKindID}, args...))

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	return buf.String(), err
}

// A kind with no concepts returned early without ever building the printer,
// so an unknown --columns name exited 0 and said nothing.
func TestGetTableKindCommand_NoConceptsRejectsUnknownColumn(t *testing.T) {
	out, err := runTableKindWithNoConcepts(t, "--columns", "bogus_column")
	if err == nil || !strings.Contains(err.Error(), `unknown column "bogus_column"`) {
		t.Fatalf("expected an unknown-column error, got err=%v output=%q", err, out)
	}
	if strings.Contains(out, "No concepts linked to this table kind.") {
		t.Errorf("must not claim success after rejecting --columns, got: %s", out)
	}
}

func TestGetTableKindCommand_NoConceptsStillReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no columns flag", nil},
		{"valid columns", []string{"--columns", "slug,name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runTableKindWithNoConcepts(t, tc.args...)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(out, "Car Policy Bordereau") {
				t.Errorf("header should still print, got: %s", out)
			}
			if !strings.Contains(out, "No standard fields linked to this table kind.") {
				t.Errorf("empty message should still print, got: %s", out)
			}
		})
	}
}

// runTableKindWithConcepts is runTableKindWithNoConcepts' twin: same
// command, same args, but a kind that does have concepts, so the run
// reaches the printer instead of the empty short-circuit.
func runTableKindWithConcepts(t *testing.T, args ...string) (string, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs(append([]string{"get", "table-kind", testKindID}, args...))

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	return buf.String(), err
}

// TestGetTableKindYAMLReadsAsJSON: -o yaml is the tree -o json prints.
func TestGetTableKindYAMLReadsAsJSON(t *testing.T) {
	yamlOut, yamlErr := runTableKindWithConcepts(t, "-o", "yaml")
	jsonOut, jsonErr := runTableKindWithConcepts(t, "-o", "json")
	if yamlErr != nil || jsonErr != nil {
		t.Fatalf("yaml err=%v, json err=%v", yamlErr, jsonErr)
	}
	testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)
}

// Whether the kind has concepts picks between the empty short-circuit and the
// printer, and that must not decide whether a --columns typo is an error, in
// any format the command declares.
func TestGetTableKindCommand_UnknownColumnRejectedOnBothPaths(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			_, emptyErr := runTableKindWithNoConcepts(t, "-o", format, "--columns", "bogus_column")
			_, fullErr := runTableKindWithConcepts(t, "-o", format, "--columns", "bogus_column")
			for path, err := range map[string]error{"empty kind": emptyErr, "populated kind": fullErr} {
				if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
					t.Errorf("-o %s, %s: want exit %d, got err=%v (exit %d)", format, path, qerrors.ExitBadUsage, err, code)
				}
			}
		})
	}
}

// The narrowing itself: -o name is not in this command's vocabulary, so the
// shipped binary exits 2 on it. Asserted on the declaration rather than
// through the synthetic root above, which has no PersistentPreRunE and so
// cannot see central validation at all.
func TestGetTableKindCommand_RejectsNameFormat(t *testing.T) {
	err := cmdutil.ValidateOutputFormatValue("name", cmdutil.SupportedOutputFormats(NewTableKindGetCommand()))
	if err == nil {
		t.Fatal("get table-kind -o name was accepted; it prints a header block before any names")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestGetTableKindCommand_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list handler that returns empty results
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 0
		page := 1
		response := dataset_kinds.DatasetKindsPage{
			Results:   []dataset_kinds.DatasetKindTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", "nonexistent"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for non-existent table kind")
	}
}

// The plain-text header renders the server's name, slug and description
// directly; only the concept table below it goes through tableui, which
// sanitizes its own cells. -v 2 is needed for the description line.
func TestGetTableKindHeaderSanitizesServerText(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+testKindID, func(w http.ResponseWriter, _ *http.Request) {
		kind := sampleDatasetKindWithConcepts()
		kind.Name = "Car Policy\x1b[31mred"
		kind.Slug = "car-policy\x1b]0;pwned\x07"
		description := "Bordereau\x1b[2J"
		kind.Description = &description
		testutil.RespondJSON(w, http.StatusOK, kind)
	})

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-vv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if strings.ContainsRune(output, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", output)
	}
	for _, want := range []string{
		`Car Policy\x1b[31mred`,
		`car-policy\x1b]0;pwned\x07`,
		`Bordereau\x1b[2J`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, output)
		}
	}
}
