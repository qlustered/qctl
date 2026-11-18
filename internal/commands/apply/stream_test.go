package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/destinations"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupStreamAuth(t *testing.T, env *testutil.TestEnv, mock *testutil.MockAPIServer) {
	t.Helper()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
}

// registerTableApplyMocks wires the three endpoints a `kind: Table` apply hits:
// defaults, the name lookup that decides create-vs-update, and the create.
func registerTableApplyMocks(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/new", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetDefaults())
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
}

func tableDoc(name string) string {
	return `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: ` + name + `
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: ` + name + `
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
`
}

// The bug this whole plan exists for, in its cheapest form: a file with two
// documents must apply two resources, not one.
func TestApplyStreamAppliesEveryDocument(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	posts := 0
	registerTableApplyMocks(t, mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if posts != 2 {
		t.Fatalf("expected 2 POSTs (one per document), got %d", posts)
	}
	if !strings.Contains(out, "2 documents: 2 succeeded") {
		t.Errorf("expected a summary count, got:\n%s", out)
	}
}

// Dispatch is per document, not per file: kubectl's model, and the natural way
// to write "here is my table, and here are the rules on it".
func TestApplyStreamDispatchesMixedKindsPerDocument(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	destPosts := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		destPosts++
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})
	setupStreamAuth(t, env, mock)

	mixed := tableDoc("orders") + `---
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`
	path := env.CreateFile("mixed.yaml", mixed)
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if destPosts != 1 {
		t.Fatalf("expected the Destination document to be applied, got %d POSTs", destPosts)
	}
	if !strings.Contains(out, "table/orders created") || !strings.Contains(out, "destination/postgres-prod created") {
		t.Errorf("expected one outcome line per kind, got:\n%s", out)
	}
}

// Without --fail-fast a failing document does not stop the run, and the
// aggregated error keeps the first failure's exit code.
func TestApplyStreamContinuesPastAFailureAndKeepsTheExitCode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		if posts == 1 {
			testutil.RespondError(w, http.StatusNotFound, "no such destination")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))
	out, err := execApply(t, path)
	if err == nil {
		t.Fatal("expected an error when a document fails")
	}
	if posts != 2 {
		t.Fatalf("expected document 2 to still be attempted, got %d POSTs", posts)
	}
	if !strings.Contains(err.Error(), "1 of 2 documents failed") {
		t.Errorf("expected the failure count in the error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (the first failure's 404 must survive the aggregate)", got, qerrors.ExitNotFound)
	}
	if !strings.Contains(out, "table/orders failed:") {
		t.Errorf("expected a per-document failure line, got:\n%s", out)
	}
	if !strings.Contains(out, "2 documents: 1 succeeded, 1 failed") {
		t.Errorf("expected a summary count, got:\n%s", out)
	}
}

// A single-document file behaves exactly as it always has: the handler's error
// comes back verbatim, with no runner prose and no aggregate wrapper.
func TestApplyStreamLeavesSingleDocumentBehaviorAlone(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "no such destination")
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("table.yaml", tableDoc("orders"))
	out, err := execApply(t, path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "documents failed") {
		t.Errorf("a one-document file must not get the batch wrapper, got: %v", err)
	}
	if strings.Contains(out, "documents:") {
		t.Errorf("a one-document file must not get a summary line, got:\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

// Under -o json and -o yaml a file of several documents prints one List of
// their documents, in file order, and nothing else on stdout. The runner is
// not silent — its prose (here, the `2 documents: 2 succeeded` summary) moves
// to stderr, which TestApplyStreamJSONFailureReportsTheCauseOnStderr asserts.
func TestApplyStreamPrintsOneListUnderStructuredOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			registerTableApplyMocks(t, mock)
			setupStreamAuth(t, env, mock)

			path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))
			stdout, stderr, err := execApplyStreams(t, path, "-o", format)
			if err != nil {
				t.Fatalf("expected success, got: %v", err)
			}
			if items := appliedListItems(t, format, stdout); len(items) != 2 || items[0]["action"] != "created" {
				t.Errorf("List items = %v, want the two documents' results", items)
			}
			if !strings.Contains(stderr, "2 documents: 2 succeeded") {
				t.Errorf("stderr lacks the summary:\n%s", stderr)
			}
		})
	}
}

// appliedListItems decodes stdout as exactly one List and returns its items.
func appliedListItems(t *testing.T, format, stdout string) []map[string]any {
	t.Helper()
	var list struct {
		Kind  string           `json:"kind" yaml:"kind"`
		Items []map[string]any `json:"items" yaml:"items"`
	}
	var err error
	if format == "yaml" {
		dec := yaml.NewDecoder(strings.NewReader(stdout))
		err = dec.Decode(&list)
		if err == nil && dec.Decode(&struct{}{}) != io.EOF {
			err = fmt.Errorf("more than one document")
		}
	} else {
		dec := json.NewDecoder(strings.NewReader(stdout))
		err = dec.Decode(&list)
		if err == nil && dec.More() {
			err = fmt.Errorf("more than one document")
		}
	}
	if err != nil || list.Kind != "List" {
		t.Fatalf("stdout is not one List (%v):\n%s", err, stdout)
	}
	return list.Items
}

// execApplyStreams is execApply with the two streams kept apart: the -o json
// contract is about which stream a line lands on, so a helper that merges them
// cannot test it.
func execApplyStreams(t *testing.T, manifestPath string, extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	rootCmd := setupApplyRoot()
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(append([]string{"apply", "-f", manifestPath}, extraArgs...))
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// Under -o json stdout is the encoded stream, so the runner's prose moves to
// stderr rather than vanishing: suppressing it outright left the cause of a
// failure in no stream at all, and a stdout-only consumer saw a fully
// successful run. The aggregate error enumerates the causes for the same
// reason.
func TestApplyStreamJSONFailureReportsTheCauseOnStderr(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		if posts == 1 {
			testutil.RespondError(w, http.StatusNotFound, "no such destination")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))
	stdout, stderr, err := execApplyStreams(t, path, "-o", "json")
	if err == nil {
		t.Fatal("expected an error when a document fails")
	}

	// stdout is one List holding the document that succeeded, and none of the
	// runner's prose.
	if items := appliedListItems(t, "json", stdout); len(items) != 1 {
		t.Fatalf("List items = %v, want the one document that succeeded", items)
	}

	// stderr carries the failure line and the summary.
	failureLine := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "table/orders failed: ") {
			failureLine = line
		}
	}
	if failureLine == "" {
		t.Fatalf("expected the per-document failure line on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "2 documents: 1 succeeded, 1 failed") {
		t.Errorf("expected the summary line on stderr, got:\n%s", stderr)
	}

	// The aggregate error enumerates that same cause, so a caller that only
	// has the error still learns why the document failed.
	cause := strings.TrimPrefix(failureLine, "table/orders failed: ")
	if !strings.Contains(err.Error(), "1 of 2 documents failed") {
		t.Errorf("expected the failure count in the error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\n  table/orders: "+cause) {
		t.Errorf("expected the error to enumerate %q, got: %v", cause, err)
	}

	// Unwrap survived the reshape: the first failure's 404 is still the exit code.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

// The per-document failure line reaches the terminal without passing through
// qerrors.Exit, so an error carrying a control sequence must be escaped there.
// The refusal below prints the manifest's column name as written.
func TestApplyStreamFailureLineSanitizesTheError(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(http.ResponseWriter, *http.Request) { t.Error("a refused manifest must send nothing") })

	doc := `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    "ship\e[31mped\u202e":
      type: DateTime
      is_nullable: false
`
	out, err := execApply(t, writeManifest(t, doc+"---\n"+doc), "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.ContainsAny(out, "\x1b\u202e") {
		t.Errorf("failure line carries a raw control character:\n%q", out)
	}
	if !strings.Contains(out, `failed: spec.fields.ship\x1b[31mped\u202e.is_nullable`) {
		t.Errorf("expected the escaped error on the failure line, got:\n%s", out)
	}
}

func TestApplyStreamRejectsAnEmptyFileWithExitTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	path := env.CreateFile("empty.yaml", "# nothing here\n")
	_, err := execApply(t, path)
	if err == nil {
		t.Fatal("expected an error for a file with no documents")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A broken document is that document's failure, not the file's: the other
// documents are still applied.
func TestApplyStreamIsolatesABrokenEnvelope(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml", "kind: Table\nmetadata:\n  name: broken\n---\n"+tableDoc("orders"))
	_, err := execApply(t, path)
	if err == nil {
		t.Fatal("expected an error for the document missing apiVersion")
	}
	if posts != 1 {
		t.Fatalf("expected the valid document to still be applied, got %d POSTs", posts)
	}
	// The aggregate carries the first (and here, only) failure's code: a
	// malformed envelope, so exit 2 -- the guide's "apply -f splits the same
	// way" claim in a multi-document file.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// Declining a handler's confirmation prompt is a deliberate skip, not a
// failure: it must not turn into a non-zero exit code. No handler returns
// errDocumentDeclined yet, so the runner is exercised through its resolve seam.
func TestApplyStreamDeclinedDocumentIsNotAFailure(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := applyStream(cmd, path, func(manifestDoc) (applyFunc, error) {
		return func(*cobra.Command, manifestDoc) (*applyResult, error) { return nil, errDocumentDeclined }, nil
	})
	if err != nil {
		t.Fatalf("declining is a skip, not a failure, got: %v", err)
	}
	if !strings.Contains(out.String(), "2 documents: 0 succeeded, 2 skipped") {
		t.Errorf("expected declined documents to be counted as skipped, got:\n%s", out.String())
	}
}

// A signal during one document's wait ends the apply with that document's
// *qerrors.Interrupted, even after an earlier failure: no later document is
// tried, and neither its failure line nor a summary is printed.
func TestApplyStreamStopsAtAnInterruptedDocument(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	path := env.CreateFile("tables.yaml",
		tableDoc("orders")+"---\n"+tableDoc("returns")+"---\n"+tableDoc("refunds"))

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	interrupt := &qerrors.Interrupted{Signal: syscall.SIGINT, Message: "interrupted; check with: qctl describe x"}
	calls := 0
	err := applyStream(cmd, path, func(manifestDoc) (applyFunc, error) {
		return func(*cobra.Command, manifestDoc) (*applyResult, error) {
			calls++
			switch calls {
			case 1:
				return nil, qerrors.NewNotFound("no such destination")
			case 2:
				return nil, fmt.Errorf("waiting: %w", interrupt)
			}
			return nil, nil
		}, nil
	})
	if calls != 2 {
		t.Errorf("handler calls = %d, want 2: the document after the interrupted one was tried", calls)
	}
	var got *qerrors.Interrupted
	if !errors.As(err, &got) || got != interrupt {
		t.Fatalf("err = %v, want the document's *qerrors.Interrupted", err)
	}
	if code := qerrors.GetExitCode(err); code != 130 {
		t.Errorf("exit code = %d, want 130", code)
	}
	if strings.Contains(out.String(), "table/returns failed") || strings.Contains(out.String(), "documents:") {
		t.Errorf("the interrupted document was reported as a failure or summarised:\n%s", out.String())
	}
}

func TestApplyStreamFailFastStopsAndNamesTheRemainder(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		testutil.RespondError(w, http.StatusNotFound, "no such destination")
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml",
		tableDoc("orders")+"---\n"+tableDoc("returns")+"---\n"+tableDoc("refunds"))
	out, err := execApply(t, path, "--fail-fast")
	if err == nil {
		t.Fatal("expected an error with --fail-fast")
	}
	if posts != 1 {
		t.Fatalf("expected --fail-fast to stop after document 1, got %d POSTs", posts)
	}
	if !strings.Contains(out, "table/returns skipped (--fail-fast)") ||
		!strings.Contains(out, "table/refunds skipped (--fail-fast)") {
		t.Errorf("expected each untried document to be named, got:\n%s", out)
	}
	if !strings.Contains(out, "3 documents: 0 succeeded, 1 failed, 2 skipped") {
		t.Errorf("expected the summary to count the skipped remainder, got:\n%s", out)
	}
}

// execApplySub runs `qctl apply <sub> -f <path>` and returns (stdout, error).
func execApplySub(t *testing.T, sub, manifestPath string) (string, error) {
	t.Helper()
	return execApplyArgs(t, "apply", sub, "-f", manifestPath)
}

func TestApplyTableSubcommandAppliesEveryDocument(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("tables.yaml", tableDoc("orders")+"---\n"+tableDoc("returns"))
	out, err := execApplySub(t, "table", path)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if posts != 2 {
		t.Fatalf("expected 2 POSTs (one per document), got %d", posts)
	}
	if !strings.Contains(out, "2 documents: 2 succeeded") {
		t.Errorf("expected a summary count, got:\n%s", out)
	}
}

// A typed subcommand keeps its own wrong-kind message: it is more specific
// than the dispatcher's "unsupported kind" list.
func TestApplyTableSubcommandRejectsAnotherKind(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	path := env.CreateFile("dest.yaml", `apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
`)
	_, err := execApplySub(t, "table", path)
	if err == nil {
		t.Fatal("expected an error for the wrong kind")
	}
	if !strings.Contains(err.Error(), "expected kind 'Table'") {
		t.Errorf("expected the subcommand's own kind message, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A wrong-kind document in a single-document file (above) aborts the whole
// command. In a multi-document file it must not: onlyKind's resolver rejects
// only that document, and applyStream counts it as one failure among many --
// the same per-document fail counting every other kind gets, proven here for
// a typed subcommand's resolver rather than the generic dispatcher's.
func TestApplyTableSubcommandMultiDocumentCountsWrongKindAsAFailure(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})
	setupStreamAuth(t, env, mock)

	badDoc := `apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
`
	path := env.CreateFile("tables.yaml",
		tableDoc("orders")+"---\n"+badDoc+"---\n"+tableDoc("returns"))
	out, err := execApplySub(t, "table", path)
	if err == nil {
		t.Fatal("expected an error: one of the three documents is the wrong kind")
	}
	if posts != 2 {
		t.Fatalf("expected the two Table documents to still be applied, got %d POSTs", posts)
	}
	if !strings.Contains(out, "expected kind 'Table'") {
		t.Errorf("expected the wrong-kind document's own failure message, got:\n%s", out)
	}
	if !strings.Contains(out, "3 documents: 2 succeeded, 1 failed") {
		t.Errorf("expected the summary to count the wrong-kind document as a failure, got:\n%s", out)
	}
}

func TestDocLabel(t *testing.T) {
	at := func(doc manifestDoc) manifestDoc {
		doc.Index, doc.Total = 3, 4
		return doc
	}
	tests := []struct {
		name string
		doc  manifestDoc
		want string
	}{
		{
			name: "named",
			doc:  at(manifestDoc{Kind: "TableRule", Name: "pickup-check"}),
			want: "table-rule/pickup-check",
		},
		{
			name: "a name wins over an identity",
			doc:  at(manifestDoc{Kind: "TableRule", Name: "pickup-check", Identity: docIdentity{InstanceName: "Vehicle Aging"}}),
			want: "table-rule/pickup-check",
		},
		{
			name: "instance name",
			doc:  at(manifestDoc{Kind: "TableRule", Identity: docIdentity{InstanceName: "Vehicle Aging", Rule: "vehicle_aging", DatasetID: "8"}}),
			want: `table-rule "Vehicle Aging" (document 3 of 4)`,
		},
		{
			name: "quoted instance name",
			doc:  at(manifestDoc{Kind: "TableRule", Identity: docIdentity{InstanceName: "Vehicle \"Aging\" \\ old\nnew"}}),
			want: `table-rule "Vehicle \"Aging\" \\ old\nnew" (document 3 of 4)`,
		},
		{
			name: "rule and table",
			doc:  at(manifestDoc{Kind: "TableRule", Identity: docIdentity{Rule: "vehicle_aging", DatasetID: "8"}}),
			want: "table-rule for rule vehicle_aging on table 8 (document 3 of 4)",
		},
		{
			name: "rule without a table",
			doc:  at(manifestDoc{Kind: "TableRule", Identity: docIdentity{Rule: "vehicle_aging"}}),
			want: "table-rule for rule vehicle_aging (document 3 of 4)",
		},
		{
			name: "a table alone is not an identity",
			doc:  at(manifestDoc{Kind: "TableRule", Identity: docIdentity{DatasetID: "8"}}),
			want: "table-rule (document 3 of 4)",
		},
		{
			name: "no identity",
			doc:  at(manifestDoc{Kind: "TableRule"}),
			want: "table-rule (document 3 of 4)",
		},
		{
			name: "kind with no resource page",
			doc:  at(manifestDoc{Kind: "Gadget"}),
			want: "Gadget (document 3 of 4)",
		},
		{
			name: "no kind",
			doc:  at(manifestDoc{}),
			want: "document (document 3 of 4)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := docLabel(tt.doc); got != tt.want {
				t.Errorf("docLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Every value in a label comes from the user's file and lands on a terminal:
// C0, C1 and bidi controls are escaped, and an LF cannot split the label into
// what reads as a second enumeration line.
func TestDocLabelSanitizesFileSuppliedValues(t *testing.T) {
	const hostile = "a\x1b[31mb\u009bc\u202ed\ne"
	const rendered = `a\x1b[31mb\x9bc\u202ed\ne`
	tests := []struct {
		name string
		doc  manifestDoc
		want string
	}{
		{
			name: "name",
			doc:  manifestDoc{Kind: "TableRule", Name: hostile},
			want: "table-rule/" + rendered,
		},
		{
			name: "instance name",
			doc:  manifestDoc{Kind: "TableRule", Identity: docIdentity{InstanceName: hostile}, Index: 1, Total: 2},
			want: `table-rule "a\x1b[31mb\u009bc\u202ed\ne" (document 1 of 2)`,
		},
		{
			name: "rule and table",
			doc:  manifestDoc{Kind: "TableRule", Identity: docIdentity{Rule: hostile, DatasetID: hostile}, Index: 1, Total: 2},
			want: "table-rule for rule " + rendered + " on table " + rendered + " (document 1 of 2)",
		},
		{
			name: "unknown kind",
			doc:  manifestDoc{Kind: hostile, Index: 1, Total: 2},
			want: rendered + " (document 1 of 2)",
		},
		{
			name: "recovered kind and metadata",
			doc:  manifestDoc{Identity: docIdentity{Kind: hostile, Name: hostile}},
			want: rendered + "/" + rendered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := docLabel(tt.doc)
			if got != tt.want {
				t.Errorf("docLabel() = %q, want %q", got, tt.want)
			}
			if strings.ContainsAny(got, "\x1b\u009b\u202e\n") {
				t.Errorf("docLabel() let a control character through: %q", got)
			}
		})
	}
}

func TestApplyStreamLabelsSlugFailuresAndInvalidEnvelopes(t *testing.T) {
	const stream = `kind: TableRule
metadata: {}
spec: {instance_name: Check, dataset_id: 8}
---
apiVersion: qluster.ai/v1
kind: Concept
metadata: {slug: policy-number}
---
apiVersion: qluster.ai/v1
kind: TableKind
metadata: {slug: policy}
`
	for _, format := range []string{"table", "json", "yaml"} {
		for _, failFast := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail-fast=%t", format, failFast), func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()
				path := env.CreateFile("manifest.yaml", stream)
				cmd := &cobra.Command{}
				cmd.Flags().String("output", format, "")
				cmd.Flags().Bool("fail-fast", failFast, "")
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				dispatched := 0
				err := applyStream(cmd, path, func(doc manifestDoc) (applyFunc, error) {
					dispatched++
					return func(*cobra.Command, manifestDoc) (*applyResult, error) {
						if doc.Kind == "Concept" {
							return nil, qerrors.NewBadUsagef("rejected spec")
						}
						return nil, nil
					}, nil
				})
				if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
				}
				human := stdout.String()
				if format != "table" {
					human = stderr.String()
					if strings.Contains(stdout.String(), "failed:") || strings.Contains(stdout.String(), "skipped") {
						t.Errorf("runner prose reached structured stdout: %s", stdout.String())
					}
				}
				const invalidLabel = `table-rule "Check" (document 1 of 3)`
				if !strings.Contains(human, invalidLabel+" failed:") || !strings.Contains(err.Error(), invalidLabel+":") {
					t.Errorf("invalid envelope lost its identity:\noutput: %s\nerror: %v", human, err)
				}
				wantDispatch := 2
				if failFast {
					wantDispatch = 0
					for _, label := range []string{"standard-field/policy-number", "table-kind/policy"} {
						if !strings.Contains(human, label+" skipped (--fail-fast)") {
							t.Errorf("missing skipped label %q in:\n%s", label, human)
						}
					}
				} else if !strings.Contains(human, "standard-field/policy-number failed:") ||
					!strings.Contains(err.Error(), "standard-field/policy-number: rejected spec") {
					t.Errorf("slug failure lost its identity:\noutput: %s\nerror: %v", human, err)
				}
				if dispatched != wantDispatch {
					t.Errorf("dispatched %d documents, want %d", dispatched, wantDispatch)
				}
			})
		}
	}
}

// metadataLessRuleDoc is an instantiate TableRule written `metadata: {}`,
// identified only by its spec.
func metadataLessRuleDoc(specLines string) string {
	return "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n" + specLines
}

// The defect this label exists for: one metadata: {} rule out of several fails,
// and both the failure line and the aggregate error say which rule it was.
func TestApplyStreamNamesAFailingMetadataLessTableRule(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	setupStreamAuth(t, env, mock)

	named := metadataLessRuleDoc("  rule_revision_id: " + testRuleRevID + "\n  dataset_id: 8\n  instance_name: Vehicle Aging\n")
	byRule := metadataLessRuleDoc("  rule: vehicle_aging\n  dataset_id: 8\n")
	path := env.CreateFile("rules.yaml",
		tableDoc("orders")+"---\n"+named+"---\n"+byRule+"---\n"+tableDoc("returns"))
	out, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("expected an error: neither rule can be resolved")
	}
	if !strings.Contains(out, "4 documents: 2 succeeded, 2 failed") {
		t.Fatalf("expected the two rule documents to fail and the tables to succeed, got:\n%s", out)
	}
	for _, label := range []string{
		`table-rule "Vehicle Aging" (document 2 of 4)`,
		"table-rule for rule vehicle_aging on table 8 (document 3 of 4)",
	} {
		if !strings.Contains(out, label+" failed: ") {
			t.Errorf("expected the failure line to name %s, got:\n%s", label, out)
		}
		if !strings.Contains(err.Error(), "\n  "+label+": ") {
			t.Errorf("expected the aggregate error to enumerate %s, got: %v", label, err)
		}
	}
	if got := qerrors.GetExitCode(err); got == qerrors.ExitSuccess {
		t.Errorf("exit code = %d, want non-zero", got)
	}
}

// --fail-fast names the documents it never tried with the same label.
func TestApplyStreamFailFastNamesASkippedMetadataLessTableRule(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	registerTableApplyMocks(t, mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "no such destination")
	})
	setupStreamAuth(t, env, mock)

	path := env.CreateFile("rules.yaml", tableDoc("orders")+"---\n"+
		metadataLessRuleDoc("  rule: vehicle_aging\n  dataset_id: 8\n  instance_name: Vehicle Aging\n"))
	out, err := execApply(t, path, "--fail-fast", "--yes")
	if err == nil {
		t.Fatal("expected an error with --fail-fast")
	}
	if !strings.Contains(out, `table-rule "Vehicle Aging" (document 2 of 2) skipped (--fail-fast)`) {
		t.Errorf("expected the skipped rule to be named, got:\n%s", out)
	}
}

func TestApplyFailFastIsPersistentSoSubcommandsSeeIt(t *testing.T) {
	cmd := NewCommand()
	if cmd.PersistentFlags().Lookup("fail-fast") == nil {
		t.Fatal("--fail-fast must be persistent: the typed subcommands process documents too")
	}
	for _, sub := range cmd.Commands() {
		// cmd.Flags() alone never reflects a parent's PersistentFlags(): cobra
		// only merges them in lazily, as a side effect of InheritedFlags(),
		// LocalFlags(), or actual flag parsing (see the identical gotcha
		// documented on hasLongFlag in internal/catalog/build.go). Checking
		// InheritedFlags() too is what proves the flag actually reaches the
		// subcommand rather than just happening to exist on a FlagSet that was
		// merged for an unrelated reason earlier in the same process.
		if sub.Flags().Lookup("fail-fast") == nil && sub.InheritedFlags().Lookup("fail-fast") == nil {
			t.Errorf("apply %s cannot see --fail-fast", sub.Name())
		}
	}
}
