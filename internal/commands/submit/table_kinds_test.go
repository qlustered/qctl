package submit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupSubmitTableKindsTestCommand() *cobra.Command {
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

	submitCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit definitions",
	}
	submitCmd.AddCommand(NewTableKindsCommand())
	rootCmd.AddCommand(submitCmd)

	return rootCmd
}

func TestSubmitTableKindsCommand_TOMLSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	submitCalled := false
	wantContent := "[problem_domain]\nname = \"Car Policy Bordereau\"\nconcepts = [\"policy_number\"]\n\n[[concepts]]\nname = \"Policy Number\"\nslug = \"policy_number\"\n"
	concepts := []dataset_kinds.DatasetConceptFull{
		{
			ID:             uuid.MustParse("cccc0000-dddd-eeee-ffff-000000000001"),
			OrganizationID: uuid.MustParse(testOrgID),
			Slug:           "policy_number",
			Name:           "Policy Number",
			CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		},
	}

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/problem-domains/import-config", func(w http.ResponseWriter, r *http.Request) {
		submitCalled = true
		var body dataset_kinds.DatasetKindImportRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Content != wantContent || body.Format != dataset_kinds.DatasetKindImportFormat("toml") {
			t.Errorf("request body = %+v, want content %q and format toml", body, wantContent)
		}
		testutil.RespondJSON(w, http.StatusCreated, dataset_kinds.DatasetKindWithConcepts{
			ID:        uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
			Slug:      "car-policy-bordereau",
			Name:      "Car Policy Bordereau",
			IsBuiltin: false,
			CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			Concepts:  &concepts,
		})
	})

	tmpDir := t.TempDir()
	tomlFile := filepath.Join(tmpDir, "kind.toml")
	if err := os.WriteFile(tomlFile, []byte(wantContent), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", tomlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !submitCalled {
		t.Error("Submit endpoint was not called")
	}
	if !strings.Contains(buf.String(), "Standard fields: 1") {
		t.Errorf("result should report linked standard field count, got: %s", buf.String())
	}
}

func TestSubmitTableKindsCommand_YAMLSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	wantContent := "problem_domain:\n  name: Car Policy Bordereau\n  concepts: [policy_number]\nconcepts:\n  - name: Policy Number\n    slug: policy_number\n"
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/problem-domains/import-config", func(w http.ResponseWriter, r *http.Request) {
		var body dataset_kinds.DatasetKindImportRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Content != wantContent || body.Format != dataset_kinds.DatasetKindImportFormat("yaml") {
			t.Errorf("request body = %+v, want content %q and format yaml", body, wantContent)
		}
		testutil.RespondJSON(w, http.StatusCreated, dataset_kinds.DatasetKindWithConcepts{
			ID:        uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
			Slug:      "car-policy-bordereau",
			Name:      "Car Policy Bordereau",
			IsBuiltin: false,
			CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		})
	})

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "kind.yaml")
	if err := os.WriteFile(yamlFile, []byte(wantContent), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", yamlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubmitTableKindsCommand_NoFiles(t *testing.T) {
	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when no files specified")
	}

	if !strings.Contains(err.Error(), "at least one file is required") {
		t.Errorf("Expected 'at least one file is required' error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestSubmitTableKindsCommand_InvalidExtension(t *testing.T) {
	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "kind.py")
	if err := os.WriteFile(pyFile, []byte("# python file"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", pyFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for unsupported file type")
	}

	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Errorf("Expected unsupported file type error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestSubmitTableKindsCommand_FileNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", "/nonexistent/kind.toml", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for nonexistent file")
	}

	if !strings.Contains(err.Error(), "failed to read file") {
		t.Errorf("Expected 'failed to read file' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

func TestSubmitTableKindsCommand_OversizeFileIsBadUsage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	filePath := filepath.Join(t.TempDir(), "oversize.toml")
	if err := os.WriteFile(filePath, []byte(strings.Repeat("x", maxFileTextLength+1)), 0600); err != nil {
		t.Fatalf("write oversize table-kind file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", filePath, "--yes"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for oversize file")
	}
	if want := fmt.Sprintf("file %s exceeds maximum size: %d characters (limit: %d)", filePath, maxFileTextLength+1, maxFileTextLength); err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// importConfigPath is the upsert endpoint: atlas answers 201 on create and 200
// on update (submit_problem_domain_from_file,
// capi/capi/views/problem_domain_view.py).
func importConfigPath() string {
	return "/api/orgs/" + testOrgID + "/problem-domains/import-config"
}

func writeTableKindFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	return path
}

func importedKind(name, slug string) dataset_kinds.DatasetKindWithConcepts {
	return dataset_kinds.DatasetKindWithConcepts{
		ID:        uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Slug:      slug,
		Name:      name,
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

// runSubmitTableKinds captures command output through Cobra's writer.
func runSubmitTableKinds(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.Execute()
	return buf.String(), err
}

// Re-submitting a file whose slug already exists is a committed update, which
// the server reports as 200. Calling that a failure is worse than cosmetic:
// the write already landed.
func TestSubmitTableKindsCommand_UpdateReturns200(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, importedKind("Car Policy Bordereau", "car-policy-bordereau"))
	})

	tomlFile := writeTableKindFile(t, "kind.toml", "[problem_domain]\nname = \"Car Policy Bordereau\"\n")

	output, err := runSubmitTableKinds(t, "submit", "table-kinds", "-f", tomlFile, "--yes")
	if err != nil {
		t.Fatalf("a 200 is a committed update, not a failure: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Updated table kind: Car Policy Bordereau (car-policy-bordereau)") {
		t.Errorf("output should name the update, got:\n%s", output)
	}
	if !strings.Contains(output, "Standard fields: 0") {
		t.Errorf("output should report zero linked standard fields, got:\n%s", output)
	}
}

// The create path prints the other verb, and must not regress with the widening.
func TestSubmitTableKindsCommand_CreateReturns201(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, importedKind("Car Policy Bordereau", "car-policy-bordereau"))
	})

	tomlFile := writeTableKindFile(t, "kind.toml", "[problem_domain]\nname = \"Car Policy Bordereau\"\n")

	output, err := runSubmitTableKinds(t, "submit", "table-kinds", "-f", tomlFile, "--yes")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Created table kind: Car Policy Bordereau (car-policy-bordereau)") {
		t.Errorf("output should name the create, got:\n%s", output)
	}
}

// The -f loop aborts on the first error, so treating an update as a failure
// silently skips every file after it.
func TestSubmitTableKindsCommand_UpdateDoesNotAbortRemainingFiles(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	submits := 0
	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		submits++
		if submits == 1 {
			testutil.RespondJSON(w, http.StatusOK, importedKind("Existing Kind", "existing-kind"))
			return
		}
		testutil.RespondJSON(w, http.StatusCreated, importedKind("Brand New Kind", "brand-new-kind"))
	})

	firstFile := writeTableKindFile(t, "existing.toml", "[problem_domain]\nname = \"Existing Kind\"\n")
	secondFile := writeTableKindFile(t, "new.toml", "[problem_domain]\nname = \"Brand New Kind\"\n")

	output, err := runSubmitTableKinds(t, "submit", "table-kinds", "-f", firstFile, "-f", secondFile, "--yes")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	if submits != 2 {
		t.Errorf("expected both files to be submitted, server saw %d request(s)\noutput:\n%s", submits, output)
	}
	if !strings.Contains(output, "Updated table kind: Existing Kind") {
		t.Errorf("first file should report an update, got:\n%s", output)
	}
	if !strings.Contains(output, "Created table kind: Brand New Kind") {
		t.Errorf("second file should report a create, got:\n%s", output)
	}
}

// Widening the accepted statuses must not swallow a real rejection. The 422
// body is object-shaped, as the backend actually sends it (see CLAUDE.md).
func TestSubmitTableKindsCommand_ValidationErrorStillFails(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": map[string]any{"msg": "invalid table kind definition", "error_code": "invalid_input"},
		})
	})

	tomlFile := writeTableKindFile(t, "kind.toml", "[problem_domain]\nname = \"Car Policy Bordereau\"\n")

	output, err := runSubmitTableKinds(t, "submit", "table-kinds", "-f", tomlFile, "--yes")
	if err == nil {
		t.Fatalf("a 422 must still fail, output:\n%s", output)
	}

	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("expected exit code 2 for a rejected submission, got %d (err: %v)", code, err)
	}
	for _, verb := range []string{"Created table kind", "Updated table kind"} {
		if strings.Contains(output, verb) {
			t.Errorf("a rejected submission must not report %q, got:\n%s", verb, output)
		}
	}
}

func TestSubmitTableKindsCommand_OldFieldKindsHint(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]any{
			"detail": map[string]any{"msg": "TOML content: unknown keys field_kinds"},
		})
	})
	file := writeTableKindFile(t, "old.toml", "[problem_domain]\nname = \"Old\"\n[[field_kinds]]\nname = \"Number\"\n")
	_, err := runSubmitTableKinds(t, "submit", "table-kinds", "-f", file, "--yes")
	if err == nil || !strings.Contains(err.Error(), "use [[concepts]] entries") {
		t.Fatalf("expected concept migration hint, got %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestSubmitTableKindsCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/problem-domains/import-config", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	tmpDir := t.TempDir()
	tomlFile := filepath.Join(tmpDir, "kind.toml")
	if err := os.WriteFile(tomlFile, []byte("[problem_domain]\nname = \"test\"\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", tomlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

// The submitted kind's name and slug come back from the server and are printed
// by the result line.
func TestDisplayTableKindImportResultSanitizesServerText(t *testing.T) {
	kind := dataset_kinds.DatasetKindWithConcepts{
		ID:   uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Name: "Car Policy\x1b[31mred",
		Slug: "car-policy\x1b]0;pwned\x07",
	}

	var buf strings.Builder
	displayTableKindImportResult(&buf, &dataset_kinds.ImportResult{Kind: &kind, Created: true})
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{`Car Policy\x1b[31mred`, `car-policy\x1b]0;pwned\x07`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}

// -o json prints {created, table_kind} with the get table-kind manifest, the
// prose moves to stderr, and -o name prints table-kind/<uuid>.
func TestSubmitTableKindsWritesTheManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("POST", importConfigPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, importedKind("Car Policy Bordereau", "car-policy-bordereau"))
	})
	tomlFile := writeTableKindFile(t, "kind.toml", "[problem_domain]\nname = \"Car Policy Bordereau\"\n")

	run := func(format string) (string, string) {
		t.Helper()
		cmd := setupSubmitTableKindsTestCommand()
		cmd.SetArgs([]string{"submit", "table-kinds", "-f", tomlFile, "--yes", "-o", format})
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		return stdout.String(), stderr.String()
	}

	stdout, stderr := run("json")
	if !strings.Contains(stderr, "Created table kind: Car Policy Bordereau") {
		t.Errorf("stderr missing the prose:\n%s", stderr)
	}
	var got struct {
		Created   bool `json:"created"`
		TableKind struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Slug string `json:"slug"`
			} `json:"metadata"`
			Status struct {
				ID string `json:"id"`
			} `json:"status"`
		} `json:"table_kind"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if !got.Created || got.TableKind.Kind != "TableKind" || got.TableKind.Metadata.Slug != "car-policy-bordereau" ||
		got.TableKind.Status.ID != "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001" {
		t.Errorf("document = %+v, want the created manifest", got)
	}

	if stdout, _ := run("name"); stdout != "table-kind/aaaa0000-bbbb-cccc-dddd-eeeeeeee0001\n" {
		t.Errorf("-o name stdout = %q", stdout)
	}
}
