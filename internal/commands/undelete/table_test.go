package undelete

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupUndeleteTableTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")

	undeleteCmd := &cobra.Command{Use: "undelete"}
	undeleteCmd.AddCommand(NewTableCommand())
	rootCmd.AddCommand(undeleteCmd)
	return rootCmd
}

// A name must be resolved through the list with states=deleted. Registering no
// handler for GET /datasets/42 is the assertion: if the command reaches for
// the detail route, the mock transport 404s and this fails.
func TestUndeleteTableResolvesNameThroughDeletedList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotStates []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		gotStates = r.URL.Query()["states"]
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{ID: 42, Name: "orders", State: api.DataSetStateDeleted}},
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders", State: "active", DataSourcesRecovered: 2,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SetArgs([]string{"undelete", "table", "orders", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if len(gotStates) != 1 || gotStates[0] != "deleted" {
		t.Errorf("states query = %v, want [deleted]", gotStates)
	}
	// Assert the literal success line, not a loose substring. `Contains(out, "2")`
	// is satisfied by the banner's `orders (id=42)` and `Contains(out, "disabled")`
	// by recoverLossyNote, which the banner prints BEFORE the API call -- so both
	// hold whatever the server answered, and nothing would pin the count.
	if !strings.Contains(out, "Cloud sources restored: 2") {
		t.Errorf("success output should report the server's data_sources_recovered verbatim, got:\n%s", out)
	}
}

// Deleted names are not unique in atlas: the unique index covers active and
// disabled rows only, while deletion preserves the name. A name matching two
// deleted rows must stop before recovery even with --yes; otherwise the list's
// default order silently chooses which table comes back.
func TestUndeleteTableAmbiguousNameExitsTwoWithoutRecovering(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{Results: []datasets.DatasetTiny{
			{ID: 77, Name: "orders", State: api.DataSetStateDeleted},
			{ID: 42, Name: "orders", State: api.DataSetStateDeleted},
		}})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetArgs([]string{"undelete", "table", "orders", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err == nil {
		t.Fatalf("expected ambiguity refusal, got output:\n%s", out)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if out != "" {
		t.Errorf("ambiguity should fail before the recovery banner, got:\n%s", out)
	}
	for _, want := range []string{"77", "42", "qctl undelete table <id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

// An integer argument must go straight to recover: the detail route 404s on a
// deleted table, so any pre-fetch would break every valid invocation. No GET
// handler is registered -- reaching for one fails the test.
func TestUndeleteTableByIDDoesNotPreFetch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders", State: "active", DataSourcesRecovered: 0,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	var commandOut bytes.Buffer
	rootCmd.SetOut(&commandOut)
	rootCmd.SetArgs([]string{"undelete", "table", "42", "--yes"})

	processOut, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, commandOut.String())
	}
	if processOut != "" {
		t.Errorf("command bypassed cmd.SetOut and wrote to process stdout:\n%s", processOut)
	}
	out := commandOut.String()
	if !strings.Contains(out, "orders") {
		t.Errorf("output should name the recovered table from the response, got:\n%s", out)
	}
	if strings.Contains(out, "Cloud sources restored:") {
		t.Errorf("zero recovered cloud sources should not produce a count line, got:\n%s", out)
	}
}

func TestUndeleteTablePassesNewName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders-v2", State: "active", DataSourcesRecovered: 1,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SetArgs([]string{"undelete", "table", "42", "--new-name", "orders-v2", "--yes"})

	if _, err := testutil.CaptureStdout(t, rootCmd.Execute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["new_name"] != "orders-v2" {
		t.Errorf("new_name = %v, want orders-v2", gotBody["new_name"])
	}
}

// The 409 the server raises on a name collision is only actionable if the CLI
// names the flag that fixes it.
func TestUndeleteTableConflictNamesNewNameFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		// atlas raises APIError(409, msg) (recover_dataset,
		// capi/capi/views/settings_view.py), which serializes object-shaped as
		// {"detail": {"msg": ...}} -- APIError.to_dict,
		// common/common/api_exceptions.py. testutil.RespondError writes
		// {"error": ...}, a shape apierror.ParseErrorResponse does not read, so
		// the server's message would never reach the user and this test would
		// assert against a message the CLI can never see.
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{
			"detail": map[string]any{
				"msg": `The name "orders" is already used by another active/disabled dataset. Provide a new_name.`,
			},
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetArgs([]string{"undelete", "table", "42", "--yes"})

	_, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "--new-name") {
		t.Errorf("error should name --new-name, got: %v", err)
	}
}

// Without --yes and without a TTY the command must refuse before calling the
// API. No recover handler is registered: reaching it fails the test.
func TestUndeleteTableNonInteractiveWithoutYesExitsTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetArgs([]string{"undelete", "table", "42"})

	_, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err == nil {
		t.Fatal("expected a refusal with no TTY and no --yes")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The other two of the three renderings. The constant existing is not what
// stops the drift -- the banner and the success output actually rendering IT,
// rather than a copy of it, is. Asserting the captured stdout of a real run
// holds it exactly twice pins both: an inlined copy that drifts, or either
// rendering being dropped, moves the count off 2.
//
// It is deliberately stronger than `undelete file`'s
// TestUndeleteBannerAndSuccessUseTheSharedNote, which never runs the command:
// that one only checks that NewFileCommand().Long embeds the constant and that
// the success line says "re-queued", so it cannot see a rendering that was
// dropped or inlined. The Long half of it is what
// TestUndeleteTableLossyNoteIsSharedAndComplete below covers for `table`.
func TestUndeleteTableBannerAndSuccessRenderTheSharedNote(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders", State: "active", DataSourcesRecovered: 3,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SetArgs([]string{"undelete", "table", "42", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	// 1: the pre-confirmation banner (printRecoverContext). 2: the success
	// output. --help is the third and is pinned by the test below.
	if got := strings.Count(out, recoverLossyNote); got != 2 {
		t.Errorf("recoverLossyNote rendered %d time(s) in stdout, want 2 (banner + success output); a copy that drifted or a rendering that was dropped both look like this:\n%s", got, out)
	}
	if !strings.Contains(out, recoverSuccessPrefix) {
		t.Errorf("success output should open with %q, got:\n%s", recoverSuccessPrefix, out)
	}
}

// The lossy-recovery facts render in three places (--help, the banner, the
// success output). One constant is what keeps all three from drifting -- the
// exact failure undeleteIngestionNote was introduced to stop.
func TestUndeleteTableLossyNoteIsSharedAndComplete(t *testing.T) {
	if !strings.Contains(NewTableCommand().Long, recoverLossyNote) {
		t.Error("Long does not embed recoverLossyNote; the copies can drift apart")
	}
	lower := strings.ToLower(recoverLossyNote)
	for _, want := range []string{"disabled", "resolved", "killed"} {
		if !strings.Contains(lower, want) {
			t.Errorf("recoverLossyNote does not mention %q; recovery is lossy in three ways and all three must be stated:\n%s", want, recoverLossyNote)
		}
	}
	// A linked table's composite source comes back disabled too, and enable
	// refuses it, so "need re-enabling" alone sends a linked table's owner
	// into that refusal.
	if !strings.Contains(recoverLossyNote, "enable refuses a linked table's composite source") {
		t.Errorf("recoverLossyNote must qualify the re-enabling advice for a composite source:\n%s", recoverLossyNote)
	}
}

// The next-step hint is advice an agent runs literally, once per source. It
// must say up front that enable refuses a composite source.
func TestUndeleteTableEnableHintQualifiesCompositeSources(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders", State: "active", DataSourcesRecovered: 2,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"undelete", "table", "42", "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hint string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "qctl enable cloud-source <id>") {
			hint = line
		}
	}
	if hint == "" {
		t.Fatalf("output has no enable hint:\n%s", out.String())
	}
	if !strings.Contains(hint, "a composite source is refused") {
		t.Errorf("the enable hint must say a composite source is refused, got: %q", hint)
	}
}

// The recovered table's name and state come back from the server and are
// rendered into three separate human lines (banner, success, next-step hint).
// A crafted name must arrive as a visible escape in every one of them.
func TestUndeleteTableSanitizesServerName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{ID: 42, Name: "orders", State: api.DataSetStateDeleted}},
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetRecoverResponse{
			DatasetID: 42, Name: "orders\x1b[31mred", State: "active\x1b]0;pwned\x07",
			DataSourcesRecovered: 1,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteTableTestCommand()
	rootCmd.SetArgs([]string{"undelete", "table", "orders", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{`orders\x1b[31mred`, `active\x1b]0;pwned\x07`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}

// The banner's label carries the server's name whenever the argument was a
// name rather than an id.
func TestPrintRecoverContextSanitizesTheResolvedLabel(t *testing.T) {
	var buf bytes.Buffer
	printRecoverContext(&buf, "orders\x1b[2J (id=42)", "")

	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	if !strings.Contains(out, `orders\x1b[2J (id=42)`) {
		t.Errorf("output missing visible escape, got:\n%s", out)
	}
}

// -o json prints the server's recover answer, the prose and the lossy note
// move to stderr, and -o name prints table/<id>.
func TestUndeleteTableWritesTheRecoverAnswer(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"result": "ok", "dataset_id": 42, "name": "orders", "state": "active", "data_sources_recovered": 2,
		})
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	run := func(format string) (string, string) {
		t.Helper()
		rootCmd := setupUndeleteTableTestCommand()
		var stdout, stderr bytes.Buffer
		rootCmd.SetOut(&stdout)
		rootCmd.SetErr(&stderr)
		rootCmd.SetArgs([]string{"undelete", "table", "42", "--yes", "-o", format})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		return stdout.String(), stderr.String()
	}

	stdout, stderr := run("json")
	if !strings.Contains(stderr, recoverSuccessPrefix) || !strings.Contains(stderr, "Recovery is not a full undo") {
		t.Errorf("stderr missing the prose:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["dataset_id"] != float64(42) || got["state"] != "active" || got["data_sources_recovered"] != float64(2) ||
		got["result"] != "ok" || got["name"] != "orders" {
		t.Errorf("document = %v, want the recover answer", got)
	}

	if stdout, _ := run("name"); stdout != "table/42\n" {
		t.Errorf("-o name stdout = %q, want %q", stdout, "table/42\n")
	}
}
