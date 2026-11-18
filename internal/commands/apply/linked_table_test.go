package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/testutil"
)

func linkedTableYAML() string {
	return `
apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: orders-enriched
spec:
  destination: warehouse
  table:
    database: analytics
    name: orders_enriched
  spine:
    source: orders
    natural_key_columns: [order_id]
  sources:
    orders:
      dataset: raw_orders
    customers:
      dataset: raw_customers
      arity: zero_or_one
      join_fields:
        customer_id: customer_id
  output:
    order_id: { source: orders, field: order_id }
    cust_name: { source: customers, field: name }
`
}

// setupLinkedTableEnv returns a TestEnv with config + credential wired to the
// mock server. Use the SAME env for CreateFile so HOME stays consistent.
func setupLinkedTableEnv(t *testing.T, mockURL string) *testutil.TestEnv {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	env.SetupConfigWithOrg(mockURL, "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mockURL)
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env
}

const linkedTableGroupID = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"

// linkedTableApplyAnswer is a CompositeDatasetApplyResponse for a first apply
// that launched a dry run to auto-enable; overrides replace or, when nil,
// delete keys.
func linkedTableApplyAnswer(overrides map[string]any) map[string]any {
	answer := map[string]any{
		"result": "created", "dataset_id": 7, "settings_model_id": 8, "data_source_model_id": 9,
		"composite_spec_group_id":    linkedTableGroupID,
		"composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
		"revision_state":             "draft", "release": "v1", "spec_sha256": "ab12",
		"desired_state": "enabled", "converged": false,
		"composite_dry_run_job_id": 812, "ingestion_job_id": 9001,
		"warnings": []any{},
	}
	for key, value := range overrides {
		if value == nil {
			delete(answer, key)
			continue
		}
		answer[key] = value
	}
	return answer
}

const linkedTableCreatedOutput = `linked-table/orders-enriched created
  revision : v1 (draft)
  dry run  : composite dry run 812 launched (ingestion job 9001); the revision is enabled if it succeeds and the enable gates pass
Next: qctl describe composite-dry-run-job 812
`

func TestApplyLinkedTable_Subcommand(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var received api.CompositeDatasetApplyManifest
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(nil))
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"apply", "linked-table", "-f", path})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if received.Kind != "CompositeDataset" {
		t.Fatalf("expected wire kind CompositeDataset, got %q", received.Kind)
	}
	if out.String() != linkedTableCreatedOutput {
		t.Fatalf("unexpected output:\n got %q\nwant %q", out.String(), linkedTableCreatedOutput)
	}
}

// Each converge outcome atlas's _converge can answer with prints its own
// dry-run line and Next: line.
func TestApplyLinkedTable_PrintsTheOutcome(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
		want      string
	}{
		{
			name: "new revision, dry run launched without auto-enable",
			overrides: map[string]any{
				"result": "revision_added", "release": "v2", "desired_state": "draft",
			},
			want: `linked-table/orders-enriched configured
  revision : v2 (draft)
  dry run  : composite dry run 812 launched (ingestion job 9001); desired_state is draft, so it does not enable the revision
Next: qctl describe composite-dry-run-job 812
`,
		},
		{
			name: "enabled, nothing launched",
			overrides: map[string]any{
				"result": "unchanged", "revision_state": "enabled", "converged": true,
				"composite_dry_run_job_id": nil, "ingestion_job_id": nil,
			},
			want: `linked-table/orders-enriched unchanged
  revision : v1 (enabled)
Next: qctl get composite-run-jobs --group-id ` + linkedTableGroupID + `
`,
		},
		{
			name: "enabled revision re-applied as draft",
			overrides: map[string]any{
				"result": "unchanged", "revision_state": "enabled", "desired_state": "draft", "converged": true,
				"composite_dry_run_job_id": nil, "ingestion_job_id": nil,
			},
			want: `linked-table/orders-enriched unchanged
  revision : v1 (enabled; desired_state: draft never disables an enabled revision)
Next: qctl get composite-run-jobs --group-id ` + linkedTableGroupID + `
`,
		},
		{
			name: "draft that already passed a dry run",
			overrides: map[string]any{
				"result": "unchanged", "desired_state": "draft", "converged": true,
				"composite_dry_run_job_id": nil, "ingestion_job_id": nil,
			},
			want: `linked-table/orders-enriched unchanged
  revision : v1 (draft)
  dry run  : the revision already has a current successful dry run
Next: qctl enable linked-table --table orders-enriched --revision 5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33
`,
		},
		{
			// The rollback path: an old manifest matched an older, disabled
			// revision, which is not the table's latest -- hence --revision.
			name: "disabled revision re-applied as draft",
			overrides: map[string]any{
				"result": "unchanged", "revision_state": "disabled", "desired_state": "draft", "converged": true,
				"composite_spec_revision_id": "7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b",
				"composite_dry_run_job_id":   nil, "ingestion_job_id": nil,
			},
			want: `linked-table/orders-enriched unchanged
  revision : v1 (disabled)
  dry run  : the revision already has a current successful dry run
Next: qctl enable linked-table --table orders-enriched --revision 7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(tt.overrides))
			})
			env := setupLinkedTableEnv(t, mock.URL())
			path := env.CreateFile("linked-table.yaml", linkedTableYAML())

			stdout, stderr, err := execApplyLinkedTable(t, path)
			if err != nil {
				t.Fatalf("execute: %v\nstderr:\n%s", err, stderr)
			}
			if stdout != tt.want {
				t.Fatalf("unexpected stdout:\n got %q\nwant %q", stdout, tt.want)
			}
		})
	}
}

// The enable recipe is pasted into a shell, so a table name that needs quoting
// gets it.
func TestWriteLinkedTableApplied_QuotesTheTableInTheEnableRecipe(t *testing.T) {
	var out bytes.Buffer
	writeLinkedTableApplied(&out, "orders enriched", &linked_tables.ApplyResponse{
		Result: "unchanged", Release: "v1", RevisionState: "draft", DesiredState: "draft", Converged: true,
		CompositeSpecRevisionID: uuid.MustParse("5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"),
	}, false)
	want := "Next: qctl enable linked-table --table 'orders enriched' --revision 5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33\n"
	if !strings.HasSuffix(out.String(), want) {
		t.Fatalf("got:\n%s\nwant it to end with:\n%s", out.String(), want)
	}
}

// The warnings a real apply was accepted in spite of print on stderr under
// every -o, ahead of the result block, which -o json also moves to stderr so
// stdout stays the raw answer.
func TestApplyLinkedTable_WarningsGoToStderr(t *testing.T) {
	for _, output := range []string{"", "json"} {
		t.Run("output="+output, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(map[string]any{
					"warnings": []any{map[string]any{
						"loc":  []string{"revision", "output", "gooz", "field"},
						"msg":  "output column 'gooz' reads deleted field 'gooz'; the mapping was removed from the spec",
						"code": "deleted_field_pruned",
					}},
				}))
			})
			env := setupLinkedTableEnv(t, mock.URL())
			path := env.CreateFile("linked-table.yaml", linkedTableYAML())

			var args []string
			if output != "" {
				args = append(args, "--output", output)
			}
			stdout, stderr, err := execApplyLinkedTable(t, path, args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			want := "Warning: revision.output.gooz.field: output column 'gooz' reads deleted field 'gooz'; the mapping was removed from the spec (deleted_field_pruned)\n"
			if output == "json" {
				want += "linked-table/orders-enriched created\n"
			}
			if !strings.HasPrefix(stderr, want) || (output == "" && stderr != want) {
				t.Fatalf("unexpected stderr:\n got %q\nwant %q", stderr, want)
			}
			if strings.Contains(stdout, "Warning:") {
				t.Fatalf("a warning leaked onto stdout:\n%s", stdout)
			}
			if output == "json" {
				var got map[string]any
				if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil || got["result"] != "created" {
					t.Fatalf("-o json stdout must be the raw answer, got %q", stdout)
				}
			}
		})
	}
}

// A 2xx the client cannot read as an apply answer is still an apply the server
// accepted: it prints the bare result line and exits 0.
func TestApplyLinkedTable_AnswerWithoutResultPrintsApplied(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"dataset_id": 7})
	})
	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	stdout, stderr, err := execApplyLinkedTable(t, path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != "linked-table/orders-enriched applied\n" || stderr != "" {
		t.Fatalf("unexpected output: stdout %q, stderr %q", stdout, stderr)
	}
}

func TestApplyLinkedTable_ValidateOnly(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotValidateOnly string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		gotValidateOnly = r.URL.Query().Get("validate_only")
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"valid": true, "errors": []any{},
			"warnings": []any{map[string]any{
				"loc":  []string{"revision", "output", "gooz", "field"},
				"msg":  "output column 'gooz' reads deleted field 'gooz' from table 'customers' (dataset id=4); the mapping was removed from the spec",
				"code": "deleted_field_pruned",
			}},
		})
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	stdout, stderr, err := execApplyLinkedTable(t, path, "--validate-only")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotValidateOnly != "true" {
		t.Fatalf("expected validate_only=true, got %q", gotValidateOnly)
	}
	if stdout != "linked-table/orders-enriched validated\n" {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
	want := "Warning: revision.output.gooz.field: output column 'gooz' reads deleted field 'gooz' from table 'customers' (dataset id=4); the mapping was removed from the spec (deleted_field_pruned)\n"
	if stderr != want {
		t.Fatalf("unexpected stderr:\n got %q\nwant %q", stderr, want)
	}
}

// valid:false arrives as a 200. It used to print "validated" and exit 0; it
// must exit 2 -- the code a real apply's 400 gives the same manifest -- list
// every error, and print nothing on stdout.
func TestApplyLinkedTable_ValidateOnlyInvalidExitsTwo(t *testing.T) {
	for _, output := range []string{"", "json"} {
		t.Run("output="+output, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"valid": false,
					"errors": []any{
						map[string]any{"loc": []string{"spec", "destination"}, "msg": "destination 'warehouse' not found in this organization", "code": "unresolved_reference"},
						map[string]any{"loc": []string{"spec", "sources", "customers", "dataset"}, "msg": "dataset 'raw_customers' not found in this organization", "code": "unresolved_reference"},
					},
					"warnings": []any{},
				})
			})

			env := setupLinkedTableEnv(t, mock.URL())
			path := env.CreateFile("linked-table.yaml", linkedTableYAML())

			args := []string{"--validate-only"}
			if output != "" {
				args = append(args, "--output", output)
			}
			stdout, _, err := execApplyLinkedTable(t, path, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			for _, want := range []string{
				"spec.destination: destination 'warehouse' not found in this organization (unresolved_reference)",
				"spec.sources.customers.dataset: dataset 'raw_customers' not found in this organization (unresolved_reference)",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in:\n%s", want, err.Error())
				}
			}
			if output == "json" {
				var got map[string]any
				if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil || got["valid"] != false {
					t.Fatalf("-o json stdout must be the raw answer, got %q", stdout)
				}
			} else if stdout != "" {
				t.Fatalf("nothing belongs on stdout for an invalid manifest, got %q", stdout)
			}
		})
	}
}

const validateOnlyTableYAML = `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
`

// serveValidateOnly answers the linked-table apply route with a passing
// validate_only verdict and records each call's validate_only value.
func serveValidateOnly(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv, *[]string) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var validateOnly []string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		validateOnly = append(validateOnly, r.URL.Query().Get("validate_only"))
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"valid": true, "errors": []any{}, "warnings": []any{}})
	})
	return mock, setupLinkedTableEnv(t, mock.URL()), &validateOnly
}

// Only a linked table can be checked without persisting, so apply -f
// --validate-only refuses a file holding any other kind before the first
// document -- a linked table ahead of it included -- rather than applying that
// kind for real.
func TestApplyGeneric_ValidateOnlyRefusesOtherKinds(t *testing.T) {
	cases := map[string]struct {
		manifest string
		named    []string
	}{
		"a Table alone":                   {validateOnlyTableYAML, []string{"table/orders"}},
		"a Table ahead of a linked table": {validateOnlyTableYAML + "---" + linkedTableYAML(), []string{"table/orders"}},
		"a Table after a linked table":    {linkedTableYAML() + "---\n" + validateOnlyTableYAML, []string{"table/orders"}},
		"two other kinds": {
			strings.Replace(linkedTableYAML(), "kind: LinkedTable", "kind: CompositeDataset", 1) + "---\n" +
				validateOnlyTableYAML + "---\n" + strings.Replace(validateOnlyTableYAML, "kind: Table", "kind: TableSchema", 1),
			[]string{"table/orders", "table-schema/orders"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock, env, _ := serveValidateOnly(t)
			path := env.CreateFile("mixed.yaml", tc.manifest)

			stdout, _, err := execApplyRoot(t, "apply", "-f", path, "--validate-only")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("expected no request, got %v", reqs)
			}
			if stdout != "" {
				t.Errorf("expected nothing on stdout, got %q", stdout)
			}
			for _, label := range tc.named {
				if !strings.Contains(err.Error(), label) {
					t.Errorf("the refusal does not name %s: %v", label, err)
				}
			}
		})
	}
}

// Both spellings of a linked table pass the refusal and are each validated.
func TestApplyGeneric_ValidateOnlyValidatesEveryLinkedTable(t *testing.T) {
	_, env, validateOnly := serveValidateOnly(t)
	path := env.CreateFile("linked.yaml", linkedTableYAML()+"---"+
		strings.Replace(linkedTableYAML(), "kind: LinkedTable", "kind: CompositeDataset", 1))

	if _, stderr, err := execApplyRoot(t, "apply", "-f", path, "--validate-only"); err != nil {
		t.Fatalf("execute: %v\nstderr:\n%s", err, stderr)
	}
	if want := []string{"true", "true"}; !reflect.DeepEqual(*validateOnly, want) {
		t.Errorf("validate_only per request = %q, want %q", *validateOnly, want)
	}
}

// A document apply -f would never apply -- an unsupported kind -- is not
// refused: it fails on its own line, and the linked table is still validated.
func TestApplyGeneric_ValidateOnlyLeavesAnUnsupportedKindToFailAlone(t *testing.T) {
	_, env, validateOnly := serveValidateOnly(t)
	path := env.CreateFile("mixed.yaml", linkedTableYAML()+"---\n"+
		strings.Replace(validateOnlyTableYAML, "kind: Table", "kind: Gadget", 1))

	_, _, err := execApplyRoot(t, "apply", "-f", path, "--validate-only")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if want := []string{"true"}; !reflect.DeepEqual(*validateOnly, want) {
		t.Errorf("validate_only per request = %q, want %q", *validateOnly, want)
	}
}

// The refusal reads the flag's value: --validate-only=false is an ordinary
// apply, which sends a Table's requests.
func TestApplyGeneric_ValidateOnlyFalseIsNotRefused(t *testing.T) {
	mock, env, _ := serveValidateOnly(t)
	path := env.CreateFile("table.yaml", validateOnlyTableYAML)

	_, _, err := execApplyRoot(t, "apply", "-f", path, "--validate-only=false")
	if code := qerrors.GetExitCode(err); code == qerrors.ExitBadUsage {
		t.Fatalf("an ordinary apply was refused: %v", err)
	}
	if len(mock.Requests()) == 0 {
		t.Error("expected the Table's requests, got none")
	}
}

func TestApplyLinkedTable_GenericDispatchBothKinds(t *testing.T) {
	for _, kind := range []string{"LinkedTable", "CompositeDataset"} {
		t.Run(kind, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var received api.CompositeDatasetApplyManifest
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Fatalf("decode: %v", err)
				}
				testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(nil))
			})

			env := setupLinkedTableEnv(t, mock.URL())
			yaml := linkedTableYAML()
			if kind == "CompositeDataset" {
				yaml = strings.Replace(yaml, "kind: LinkedTable", "kind: CompositeDataset", 1)
			}
			path := env.CreateFile("lt.yaml", yaml)

			root := setupApplyRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			// no subcommand: generic dispatch by kind
			root.SetArgs([]string{"apply", "-f", path})

			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if received.Kind != "CompositeDataset" {
				t.Fatalf("expected wire kind CompositeDataset, got %q", received.Kind)
			}
			if out.String() != linkedTableCreatedOutput {
				t.Fatalf("unexpected output: %q", out.String())
			}
		})
	}
}

func TestApplyLinkedTable_OutputJSONDumpsRawBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(nil))
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"apply", "linked-table", "-f", path, "--output", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Output must be the raw backend body, parseable as JSON.
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %q", err, out.String())
	}
	if got["result"] != "created" || got["composite_dry_run_job_id"] != float64(812) {
		t.Fatalf("expected the raw backend body, got: %q", out.String())
	}
}

func TestApplyLinkedTable_ValidationFailureSkipsHTTP(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Omit spec.output entirely -> Validate() must fail on spec.output.
	badYAML := strings.Replace(linkedTableYAML(),
		`  output:
    order_id: { source: orders, field: order_id }
    cust_name: { source: customers, field: name }
`, "", 1)

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("bad.yaml", badYAML)

	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"apply", "linked-table", "-f", path})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected validation error, got nil (output: %q)", out.String())
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation error message, got: %v", err)
	}
	if called {
		t.Fatalf("backend should NOT be called when validation fails")
	}
}

// A join constant YAML cannot decode as the integer written fails the load,
// so neither form of apply sends anything.
func TestApplyLinkedTable_RefusesAConstantItCannotSendAsWritten(t *testing.T) {
	manifest := strings.Replace(linkedTableYAML(), "        customer_id: customer_id\n",
		"        customer_id: customer_id\n        region: {source_fields: [region_code], constant: 18446744073709551616}\n", 1)
	for _, args := range [][]string{{"apply", "linked-table", "-f"}, {"apply", "-f"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			env := setupLinkedTableEnv(t, mock.URL())
			path := env.CreateFile("linked-table.yaml", manifest)

			root := setupApplyRoot()
			root.SilenceUsage = true
			root.SilenceErrors = true
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(append(append([]string(nil), args...), path))

			err := root.Execute()
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "spec.sources.customers.join_fields.region.constant") {
				t.Fatalf("error does not name the join's constant: %v", err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Fatalf("requests = %#v, want none", got)
			}
		})
	}
}

// execApplyLinkedTable runs the subcommand with stdout and stderr captured
// separately. The retry helper prints progress prose, and under -o json stdout
// is a parsed document -- the two must not share a stream.
func execApplyLinkedTable(t *testing.T, path string, extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"apply", "linked-table", "-f", path}, extraArgs...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// The backend refuses the converge while the user's exploratory budget is full,
// then accepts the identical request. Retrying is safe: the 429 rolls the whole
// apply back (atlas get_session_for_fastapi, cettings/cettings/db.py) and apply
// is create-or-converge. The header and the body's retry_after_seconds disagree
// deliberately (9 vs 15): equal values would let this assertion hold even if the
// header were ignored entirely, so it would not guard what it claims to.
func TestApplyLinkedTable_RetriesAThrottledConverge(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	slept := stubSleep(t)

	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "9")
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]any{
				"detail": map[string]any{
					"msg":        "You have 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to complete before starting a new one.",
					"error_code": "DRY_RUN_RATE_LIMIT",
					"context": map[string]any{
						"current_count": 3, "max_allowed": 3,
						"limit_kind": "concurrent_execution", "retry_after_seconds": 15,
					},
				},
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(nil))
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	stdout, stderr, err := execApplyLinkedTable(t, path)
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if calls != 2 {
		t.Fatalf("expected 2 apply attempts, got %d", calls)
	}
	if len(*slept) != 1 || (*slept)[0] != 9*time.Second {
		t.Fatalf("expected a single 9s wait honouring Retry-After, got %v (the computed backoff would be [2s], and the body's retry_after_seconds is 15)", *slept)
	}
	if stdout != linkedTableCreatedOutput {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "throttling") {
		t.Errorf("expected the wait to be explained on stderr, got:\n%s", stderr)
	}
}

// Under -o json stdout is a document a script parses, so the progress prose has
// to go to stderr. This is why the handler passes cmd.ErrOrStderr() rather than
// following applyTableRuleInstantiate's cmd.OutOrStdout().
func TestApplyLinkedTable_ThrottleProseStaysOffJSONStdout(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	stubSleep(t)

	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]any{
				"detail": map[string]any{"msg": "slow down", "error_code": "DRY_RUN_RATE_LIMIT"},
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"dataset_id": 7})
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	stdout, stderr, err := execApplyLinkedTable(t, path, "--output", "json")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "throttling") {
		t.Errorf("progress prose leaked into stdout, which is parsed as JSON:\n%s", stdout)
	}
	// Keeping stdout clean is only half of it: the prose still has to reach the
	// user. Without this, a handler that dropped the progress line entirely
	// would pass the check above.
	if !strings.Contains(stderr, "throttling") {
		t.Errorf("the wait was not explained anywhere -- stderr:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v):\n%s", err, stdout)
	}
	if _, ok := got["dataset_id"]; !ok {
		t.Fatalf("expected the raw backend body, got: %q", stdout)
	}
}

// Exhausting the budget is still a failure, with the throttle's exit code (1,
// not 2: the manifest was well-formed).
func TestApplyLinkedTable_GivesUpAfterTheAttemptLimit(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	stubSleep(t)

	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]any{
			"detail": map[string]any{"msg": "still busy", "error_code": "DRY_RUN_RATE_LIMIT"},
		})
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	_, _, err := execApplyLinkedTable(t, path)
	if err == nil {
		t.Fatal("expected an error once the attempts are exhausted")
	}
	if calls != cmdutil.ThrottleMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", cmdutil.ThrottleMaxAttempts, calls)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: a throttle is not bad usage", code, qerrors.ExitGenericError)
	}
}

// A non-429 is permanent: retrying it wastes the user's time and, on an apply,
// re-sends a manifest the backend already judged.
func TestApplyLinkedTable_DoesNotRetryAPermanentFailure(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	stubSleep(t)

	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": map[string]any{"msg": "bad spec"},
		})
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml", linkedTableYAML())

	if _, _, err := execApplyLinkedTable(t, path); err == nil {
		t.Fatal("expected the 422 to fail")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a 422, got %d", calls)
	}
}

// The outcome echoes the manifest's own name and the server's result, release
// and revision state, none of which qctl authored: an ESC sequence in any of
// them prints as a visible escape.
func TestApplyLinkedTableSanitizesTheAppliedLine(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, linkedTableApplyAnswer(map[string]any{
			"result": "created\x1b[32m", "release": "v1\x1b[33m", "revision_state": "draft\x1b[34m",
			"composite_dry_run_job_id": nil, "ingestion_job_id": nil,
		}))
	})

	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("linked-table.yaml",
		strings.Replace(linkedTableYAML(), "name: orders-enriched", `name: "orders-enriched\x1b[31m"`, 1))

	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"apply", "linked-table", "-f", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := `linked-table/orders-enriched\x1b[31m created\x1b[32m` + "\n" +
		`  revision : v1\x1b[33m (draft\x1b[34m)` + "\n"
	if got := out.String(); strings.ContainsRune(got, '\x1b') || got != want {
		t.Errorf("the outcome was not sanitized:\n got %q\nwant %q", got, want)
	}
}
