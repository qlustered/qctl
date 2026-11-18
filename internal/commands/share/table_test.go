package share

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupShareTableTestCommand() *cobra.Command {
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

	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func okBody() map[string]any {
	return map[string]any{
		"result": "ok", "user_id": "11111111-2222-3333-4444-555555555555",
		"granted": []int{12}, "already_had_access": []any{}, "failed": []any{},
	}
}

// A single table still goes to the BULK route. This is the whole §5.4 decision
// made testable: registering no handler for /datasets/12/add-user means a
// regression to the per-table route 404s here.
func TestShareTableSingleTableUsesTheBulkRoute(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	called := false
	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		called = true
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !called {
		t.Error("the bulk route was not called")
	}
	// The URL alone does not pin the recipient: swapping Email and UserID in
	// the request struct keeps every route assertion green while granting
	// access to nobody -- or to the wrong person. Assert the wire.
	if ids := gotBody["dataset_ids"]; !reflect.DeepEqual(ids, []any{float64(12)}) {
		t.Errorf("dataset_ids = %#v, want [12]", ids)
	}
	if got := gotBody["email"]; got != "alice@example.com" {
		t.Errorf("email = %#v, want alice@example.com", got)
	}
	if got := gotBody["user_id"]; got != nil {
		t.Errorf("user_id = %#v, want null when --to was used", got)
	}
}

// Different selectors can resolve to the same table. The command must send
// one id, count one table, and keep the first label rather than letting a later
// numeric selector overwrite it.
func TestShareTableDeduplicatesResolvedTargetsAndKeepsFirstLabel(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":12,"name":"orders","state":"active"}]}`))
	})
	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	rootCmd.SetArgs([]string{"share", "table", "orders", "12", "--to", "alice@example.com", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if ids := gotBody["dataset_ids"]; !reflect.DeepEqual(ids, []any{float64(12)}) {
		t.Errorf("dataset_ids = %#v, want one [12] entry", ids)
	}
	if got := strings.Count(out, "orders (id=12)"); got != 2 {
		t.Errorf("first selector label rendered %d times, want banner and result once each:\n%s", got, out)
	}
}

// --to-id is the only path that fills UserID, and nothing anywhere else in the
// tree exercises that wire field: --to and --to-id are two adjacent string
// fields on one struct, so a transposition is invisible without this.
func TestShareTableToIDSendsUserIDAndNoEmail(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	const recipientID = "11111111-2222-3333-4444-555555555555"
	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	rootCmd.SetArgs([]string{"share", "table", "12", "--to-id", recipientID, "--yes"})

	if out, err := testutil.CaptureStdout(t, rootCmd.Execute); err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if got := gotBody["user_id"]; got != recipientID {
		t.Errorf("user_id = %#v, want %s", got, recipientID)
	}
	if got := gotBody["email"]; got != nil {
		t.Errorf("email = %#v, want null when --to-id was used", got)
	}
}

// Declining the prompt must commit nothing. Registering a handler that records
// Declining must send no grant request.
func TestShareTableDeclineGrantsNothing(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	rootCmd := setupShareTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com"})

	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error when the prompt is declined")
	}
	if called {
		t.Error("declining the prompt still called the grant route")
	}
	// The code is the contract; the word is the courtesy. A decline is exit 1
	// (the repo-wide convention for every ConfirmYesNo caller), and asserting
	// only that the message says "cancelled" passes against a decline that
	// exits 0 or 2 -- CLAUDE.md's "test the code, not the text".
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1 (declined)", code)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should say the share was cancelled, got: %v", err)
	}
	// The prompt is only worth showing because of what the banner says; this
	// is the one path where a human answers it.
	if !strings.Contains(strings.ToLower(out.String()), "revoke") {
		t.Errorf("the banner must explain how to revoke a grant before the prompt, got:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "Grant this access?") {
		t.Errorf("the confirmation prompt was never shown on stderr, got:\n%s", errOut.String())
	}
}

func TestShareTableStructuredWithoutYesKeepsPromptOffStdout(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, okBody())
			})

			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmdutil.SetReader(strings.NewReader("y\n"))
			defer cmdutil.ResetReader()

			rootCmd := setupShareTableTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SilenceErrors = true
			rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "-o", format})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr.String(), "Grant this access? [y/N]:") {
				t.Errorf("stderr does not contain the confirmation prompt: %q", stderr.String())
			}
			if format == "json" {
				var result datasets.ShareDatasetsResult
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Errorf("stdout is not clean JSON: %v\n%s", err, stdout.String())
				}
			} else if strings.Contains(stdout.String(), "Grant this access?") {
				t.Errorf("stdout contains the confirmation prompt: %q", stdout.String())
			}
		})
	}
}

// Under -o json the banner is not dropped, only moved: stderr carries the
// irreversibility warning while stdout stays a clean document. Asserting it
// lands on the command's error writer is what stops it silently regressing to
// "suppressed", which is how the warning went missing on the piped path.
func TestShareTableJSONWritesTheWarningToStderr(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--yes", "-o", "json"})

	if _, err := testutil.CaptureStdout(t, rootCmd.Execute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(errBuf.String()), "revoke") {
		t.Errorf("stderr must carry the access note, got:\n%s", errBuf.String())
	}
	if strings.Contains(strings.ToLower(outBuf.String()), "revoke") {
		t.Errorf("the banner leaked into the payload:\n%s", outBuf.String())
	}
}

// 207 means some grants landed and some did not. Exit 1 (something failed) but
// the output must still say which grants were committed, or a nonzero exit
// reads as "nothing happened".
func TestShareTablePartialExitsOneAndReportsBothSides(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusMultiStatus, map[string]any{
			"result": "partial", "user_id": "11111111-2222-3333-4444-555555555555",
			"granted": []int{12}, "already_had_access": []any{},
			"failed": []map[string]any{
				{"dataset_id": 19, "reason": "not_authorized", "msg": "You are not authorized to invite another person to access this table."},
			},
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"share", "table", "12", "12", "19", "--to", "alice@example.com", "--yes"})

	out, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err == nil {
		t.Fatal("expected a nonzero exit on a partial result")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if ids := gotBody["dataset_ids"]; !reflect.DeepEqual(ids, []any{float64(12), float64(19)}) {
		t.Errorf("dataset_ids = %#v, want deduplicated [12 19]", ids)
	}
	combined := out + err.Error()
	if !strings.Contains(combined, "12") || !strings.Contains(combined, "19") {
		t.Errorf("output should name both the granted and the refused table, got:\n%s", combined)
	}
	if !strings.Contains(strings.ToLower(combined), "not_authorized") {
		t.Errorf("output should carry the server's failure reason, got:\n%s", combined)
	}
	if !strings.Contains(strings.ToLower(combined), "committed") {
		t.Errorf("output must say the successful grants were committed, got:\n%s", combined)
	}
	if !strings.Contains(err.Error(), "1 of 2 tables were refused; the other 1 grant was committed") {
		t.Errorf("partial error must use unique totals and singular grant wording, got: %v", err)
	}
}

func TestShareTableRequiresExactlyOneRecipientFlag(t *testing.T) {
	for name, args := range map[string][]string{
		"neither": {"share", "table", "12", "--yes"},
		"both":    {"share", "table", "12", "--to", "a@b.com", "--to-id", "11111111-2222-3333-4444-555555555555", "--yes"},
	} {
		t.Run(name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			rootCmd := setupShareTableTestCommand()
			rootCmd.SilenceErrors = true
			rootCmd.SetArgs(args)

			_, err := testutil.CaptureStdout(t, rootCmd.Execute)
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
		})
	}
}

// The server caps a bulk grant at 200 ids. Rejecting locally turns a 422 with
// a wire-shaped message into a usage error that names the cap.
func TestShareTableRejectsMoreThanTheCapWithoutARequest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// No handler: any request fails the test.

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	args := []string{"share", "table"}
	for i := 1; i <= maxShareTables+1; i++ {
		args = append(args, strconv.Itoa(i))
	}
	args = append(args, "--to", "alice@example.com", "--yes")

	rootCmd := setupShareTableTestCommand()
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs(args)

	_, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err == nil {
		t.Fatal("expected a usage error above the cap")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "200") {
		t.Errorf("error should name the cap, got: %v", err)
	}
}

func TestShareTableExplainsManagingExistingGrants(t *testing.T) {
	for _, text := range []string{shareAccessNote, NewTableCommand().Long} {
		if !strings.Contains(text, "table-member set-permission") || !strings.Contains(text, "table-member remove") {
			t.Errorf("missing member management advice: %s", text)
		}
	}
}

func TestShareTableJSONOutputCarriesTheResponse(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	var buf, errBuf bytes.Buffer
	rootCmd.SetOut(&buf)
	// CaptureStdout does not touch os.Stderr, and under -o json the banner goes
	// to cmd.ErrOrStderr(); without this it prints through the test binary.
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--yes", "-o", "json"})

	if _, err := testutil.CaptureStdout(t, rootCmd.Execute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded datasets.ShareDatasetsResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not the response object: %v\n%s", err, buf.String())
	}
	if len(decoded.Granted) != 1 || decoded.Granted[0] != 12 {
		t.Errorf("granted = %v, want [12]", decoded.Granted)
	}
}

// TestShareTableYAMLReadsAsJSON: -o yaml is the tree -o json prints.
func TestShareTableYAMLReadsAsJSON(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	run := func(format string) string {
		t.Helper()
		rootCmd := setupShareTableTestCommand()
		var buf bytes.Buffer
		rootCmd.SetOut(&buf)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--yes", "-o", format})
		if _, err := testutil.CaptureStdout(t, rootCmd.Execute); err != nil {
			t.Fatalf("share table -o %s: %v", format, err)
		}
		return buf.String()
	}

	testutil.AssertYAMLReadsAsJSON(t, run("yaml"), run("json"))
}

// The test above cannot catch a banner leaking into the payload: its harness
// sends cmd.OutOrStdout() to a buffer and process stdout to a separate capture,
// so the two are separate here and merged in production. This asserts on the
// process-stdout half directly -- under -o json nothing human may be written
// there, or `qctl share table ... -o json | jq` gets prose in front of the
// document.
func TestShareTableJSONWritesNothingHumanToStdout(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, okBody())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupShareTableTestCommand()
	var buf, errBuf bytes.Buffer
	rootCmd.SetOut(&buf)
	// CaptureStdout does not touch os.Stderr, and under -o json the banner goes
	// to cmd.ErrOrStderr(); without this it prints through the test binary.
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--yes", "-o", "json"})

	stdout, err := testutil.CaptureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("-o json wrote human output to stdout, which would corrupt the payload:\n%s", stdout)
	}
}

// The refusal reason and message are the server's own text, rendered straight
// into the human result. A crafted message must arrive as a visible escape.
func TestPrintShareResultSanitizesTheServersRefusal(t *testing.T) {
	result := &datasets.ShareDatasetsResult{
		Granted:          []int{12},
		AlreadyHadAccess: []int{},
		Failed: []api.DataSetGrantFailureSchema{{
			DatasetID: 19,
			Reason:    "not_authorized\x1b[31m",
			Msg:       "You are not authorized\x1b]0;pwned\x07",
		}},
	}

	var buf bytes.Buffer
	printShareResult(&buf, result, map[int]string{12: "orders", 19: "shipments"})

	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{`not_authorized\x1b[31m`, `You are not authorized\x1b]0;pwned\x07`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}

func TestShareTablePermissionAppliesOnlyToNewGrants(t *testing.T) {
	for _, level := range []string{"", "owner", "editor", "read_only"} {
		t.Run("permission="+level, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpoint, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpoint, testOrgID, "test-token")
			want := level
			if want == "" {
				want = "read_only"
			}
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["permission_level"] != want {
					t.Errorf("body = %v, want permission %s", body, want)
				}
				result := okBody()
				result["already_had_access"] = []int{13}
				testutil.RespondJSON(w, http.StatusOK, result)
			})
			cmd := setupShareTableTestCommand()
			args := []string{"share", "table", "12", "13", "--to", "alice@example.com", "--yes"}
			if level != "" {
				args = append(args, "--permission", level)
			}
			cmd.SetArgs(args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "New grants   : "+want) || !strings.Contains(out.String(), "Already had access (level unchanged): id=13") {
				t.Error(out.String())
			}
		})
	}
}

func TestShareTableRejectsInvalidPermissionBeforeConfig(t *testing.T) {
	for _, value := range []string{"", "admin", "READ_ONLY"} {
		t.Run(value, func(t *testing.T) {
			_ = testutil.NewTestEnv(t)
			cmd := setupShareTableTestCommand()
			cmd.SetArgs([]string{"share", "table", "12", "--to", "alice@example.com", "--permission", value, "--yes"})
			err := cmd.Execute()
			if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "--permission") {
				t.Fatalf("want permission usage error, got %v", err)
			}
		})
	}
}

func TestShareTableExternalRecipientHasClearExitTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpoint, testOrgID, "test-token")
	const message = "External users get access through their cloud sources, not through table sharing."
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, 400, map[string]string{"detail": message})
	})
	cmd := setupShareTableTestCommand()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"share", "table", "12", "--to", "external@example.com", "--yes", "-o", "json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), message) {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("false success document: %s", out.String())
	}
}
