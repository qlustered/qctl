package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// atlas's PII_COLUMNS_REMOVAL_DENIED_MSG (capi/capi/views/settings_view_core.py).
const piiColumnsRemovalDenied = `{"detail":{"msg":"You don't have access to remove columns from this table's ` +
	`personal data list. Only an administrator can remove them; please contact your administrator.",` +
	`"loc":["body","pii_columns"]}}`

const piiAdminNote = "Only an admin can remove a column from the PII list."

// newPiiColumnsApplyTest serves table orders with stored pii_columns and
// answers its PATCH with status and body, recording the PATCH body.
func newPiiColumnsApplyTest(t *testing.T, stored []string, status int, body string) (*testutil.TestEnv, *map[string]json.RawMessage) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	existing := sampleDatasetFullForApply()
	orgID := types.UUID{}
	_ = orgID.UnmarshalText([]byte(testOrgID))
	existing.OrganizationID = orgID
	existing.PiiColumns = stored
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID: existing.ID, Name: existing.Name, OrganizationID: orgID, State: api.DataSetStateActive,
				Users: []api.DataSetUserInfoTinySchema{}, VersionID: existing.VersionID,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})
	sent := map[string]json.RawMessage{}
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decode PATCH: %v", err)
		}
		if status == http.StatusOK {
			testutil.RespondJSON(w, http.StatusOK, existing)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env, &sent
}

// piiColumnsManifest is table orders with anomaly_threshold 12 and, unless
// it is "", the spec.pii_columns line piiLine.
func piiColumnsManifest(env *testutil.TestEnv, piiLine string) string {
	return env.CreateFile("table.yaml", `
apiVersion: qluster.ai/v1
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
  anomaly_threshold: 12
`+piiLine+`
`)
}

func runPiiColumnsApply(t *testing.T, manifestPath string) (string, error) {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"apply", "-f", manifestPath, "--yes"})
	err := root.Execute()
	return stdout.String(), err
}

// A 403 for an unrelated reason, through the same apply, gives the exit code
// a plain 403 has.
func plainForbiddenExitCode(t *testing.T) int {
	t.Helper()
	code := -1
	t.Run("plain 403", func(t *testing.T) {
		env, _ := newPiiColumnsApplyTest(t, nil, http.StatusForbidden, `{"detail":{"msg":"Forbidden."}}`)
		_, err := runPiiColumnsApply(t, piiColumnsManifest(env, ""))
		if err == nil {
			t.Fatal("apply answered 403 succeeded")
		}
		code = qerrors.GetExitCode(err)
	})
	return code
}

func TestApplyTableDroppingPiiColumnsWithoutPiiGrantNamesTheDroppedColumns(t *testing.T) {
	want := plainForbiddenExitCode(t)
	env, sent := newPiiColumnsApplyTest(t, []string{"cust_email", "phone", "ssn"},
		http.StatusForbidden, piiColumnsRemovalDenied)

	_, err := runPiiColumnsApply(t, piiColumnsManifest(env, "  pii_columns: [phone]"))
	if err == nil {
		t.Fatal("apply refused with 403 succeeded")
	}
	if got := qerrors.GetExitCode(err); got != want || got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d, the plain 403's", got, want)
	}
	msg := err.Error()
	for _, text := range []string{
		"You don't have access to remove columns from this table's personal data list.",
		piiAdminNote,
		"This apply drops cust_email, ssn from spec.pii_columns, so the table was not updated.",
		"Keep them in spec.pii_columns",
	} {
		if !strings.Contains(msg, text) {
			t.Errorf("error is missing %q:\n%s", text, msg)
		}
	}
	if strings.Contains(msg, "Scoped to organization") {
		t.Errorf("error carries the organization-scope note for a table it reached:\n%s", msg)
	}
	if got := string((*sent)["pii_columns"]); got != `["phone"]` {
		t.Errorf("PATCH pii_columns = %s, want [\"phone\"]", got)
	}
}

// A 403 at loc ["body", "pii_columns"] for a plan that drops nothing (the
// refusal a third-party table's PATCH sends) keeps the server's message alone.
func TestApplyTablePiiColumnsRefusalWithoutADropAddsNoNote(t *testing.T) {
	env, _ := newPiiColumnsApplyTest(t, []string{"cust_email"}, http.StatusForbidden,
		`{"detail":{"msg":"This table's personal data list can only be changed by the organization that owns the table.",`+
			`"loc":["body","pii_columns"]}}`)

	_, err := runPiiColumnsApply(t, piiColumnsManifest(env, "  pii_columns: [cust_email, phone]"))
	if err == nil {
		t.Fatal("apply refused with 403 succeeded")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
	if msg := err.Error(); strings.Contains(msg, piiAdminNote) ||
		!strings.Contains(msg, "can only be changed by the organization that owns the table") {
		t.Errorf("error = %q, want the server's message and no admin note", msg)
	}
}

// Any other 403 is unchanged, even when the plan drops pii_columns entries.
func TestApplyTableUnrelatedForbiddenIsUnchangedWhenThePlanDropsPiiColumns(t *testing.T) {
	want := plainForbiddenExitCode(t)
	env, _ := newPiiColumnsApplyTest(t, []string{"cust_email"}, http.StatusForbidden,
		`{"detail":{"msg":"Forbidden.","loc":["body","anomaly_threshold"]}}`)

	_, err := runPiiColumnsApply(t, piiColumnsManifest(env, "  pii_columns: []"))
	if err == nil {
		t.Fatal("apply refused with 403 succeeded")
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d", got, want)
	}
	if msg := err.Error(); strings.Contains(msg, piiAdminNote) {
		t.Errorf("error = %q, want no admin note on an unrelated 403", msg)
	}
}

// A manifest without spec.pii_columns plans no pii_columns change, and the
// PATCH resends the stored list exactly, order included -- the third-party
// refusal compares lists in order, and a removal is any stored entry missing.
func TestApplyTableWithoutPiiColumnsResendsTheStoredList(t *testing.T) {
	env, sent := newPiiColumnsApplyTest(t, []string{"ssn", "cust_email"}, http.StatusOK, "")

	stdout, err := runPiiColumnsApply(t, piiColumnsManifest(env, ""))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(stdout, "table/orders updated") {
		t.Errorf("stdout = %q, want the update line", stdout)
	}
	var got []string
	if err := json.Unmarshal((*sent)["pii_columns"], &got); err != nil {
		t.Fatalf("PATCH pii_columns = %s: %v", (*sent)["pii_columns"], err)
	}
	if !reflect.DeepEqual(got, []string{"ssn", "cust_email"}) {
		t.Errorf("PATCH pii_columns = %v, want the stored [ssn cust_email]", got)
	}
}
