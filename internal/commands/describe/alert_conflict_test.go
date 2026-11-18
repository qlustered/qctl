package describe

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func conflictMock(t *testing.T, body, fieldValue, fieldSchema string, schemaStatus int) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":1,"dataset_id":10,"dataset_name":"orders","issue_type":"cell_override_conflict","field_name":"discount","field_value":%s,"body":%s}`, fieldValue, body)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/10/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		if schemaStatus != http.StatusOK {
			testutil.RespondError(w, schemaStatus, "schema unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// A draft with different units must not decide the alert's units.
		fmt.Fprintf(w, `{"fields":{"discount":{"current":%s,"new":{"type":"Decimal","is_dollar":true,"display_decimals":4}}}}`, fieldSchema)
	})
	return mock
}

func execConflict(t *testing.T, args ...string) (map[string]any, string, string) {
	t.Helper()
	cmd := setupAlertTestCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"describe", "alert", "1"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, stderr.String())
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	return doc, out.String(), stderr.String()
}

func TestDescribeAlertConflictValues(t *testing.T) {
	const percent = `{"type":"Decimal","is_percent":true}`
	cases := []struct {
		name, body, schema, fixed, wantFixed string
		want                                 map[string]any
	}{
		{"stored percent", `{"pipeline_value":"0.09","pipeline_value_is_number":true,"base_displaced_value":0.125}`, percent, `"0.13"`, "13.00%",
			map[string]any{"pipeline_value": "9.00%", "pipeline_value_is_number": true, "base_displaced_value": "12.50%"}},
		{"source percent text", `{"pipeline_value":"9","pipeline_value_is_number":false,"base_displaced_value":"12.5"}`, percent, `"0.13"`, "13.00%",
			map[string]any{"pipeline_value": "9", "pipeline_value_is_number": false, "base_displaced_value": "12.5"}},
		{"composite percent", `{"pipeline_value":"0.09"}`, percent, `"0.13"`, "13.00%",
			map[string]any{"pipeline_value": "9.00%"}},
		{"numeric zero", `{"pipeline_value":0,"pipeline_value_is_number":false,"base_displaced_value":0}`, percent, `"0"`, "0.00%",
			map[string]any{"pipeline_value": "0.00%", "pipeline_value_is_number": false, "base_displaced_value": "0.00%"}},
		{"explicit nulls", `{"pipeline_value":null,"base_displaced_value":null}`, percent, `"0.13"`, "13.00%",
			map[string]any{"pipeline_value": nil, "base_displaced_value": nil}},
		{"empty strings", `{"pipeline_value":"","pipeline_value_is_number":false,"base_displaced_value":""}`, percent, `"0.13"`, "13.00%",
			map[string]any{"pipeline_value": "", "pipeline_value_is_number": false, "base_displaced_value": ""}},
		{"empty fix", `{"pipeline_value":"0.09"}`, percent, `""`, "",
			map[string]any{"pipeline_value": "9.00%"}},
		{"booleans", `{"pipeline_value":false,"base_displaced_value":true}`, `{"type":"Boolean"}`, `"false"`, "false",
			map[string]any{"pipeline_value": false, "base_displaced_value": true}},
		{"text with numeric flags", `{"pipeline_value":"0.09","pipeline_value_is_number":true,"base_displaced_value":"0.125"}`, `{"type":"String","is_percent":true}`, `"0.13"`, "0.13",
			map[string]any{"pipeline_value": "0.09", "pipeline_value_is_number": true, "base_displaced_value": "0.125"}},
		{"currency precision", `{"pipeline_value":"9007199254740993.25","pipeline_value_is_number":true,"base_displaced_value":9007199254740993}`, `{"type":"Decimal","is_dollar":true}`, `"1500"`, "$1,500.00",
			map[string]any{"pipeline_value": "$9,007,199,254,740,993.25", "pipeline_value_is_number": true, "base_displaced_value": "$9,007,199,254,740,993.00"}},
		{"number decimals", `{"pipeline_value":"12.345","pipeline_value_is_number":true,"base_displaced_value":12.345}`, `{"type":"Float","display_decimals":2}`, `"15.555"`, "15.56",
			map[string]any{"pipeline_value": "12.35", "pipeline_value_is_number": true, "base_displaced_value": "12.35"}},
	}
	for _, tc := range cases {
		for _, format := range []string{"json", "yaml"} {
			for _, verbosity := range []string{"", "-v"} {
				t.Run(tc.name+"/"+format+"/"+verbosity, func(t *testing.T) {
					conflictMock(t, tc.body, tc.fixed, tc.schema, http.StatusOK)
					args := []string{"-o", format}
					if verbosity != "" {
						args = append(args, verbosity)
					}
					doc, out, stderr := execConflict(t, args...)
					if stderr != "" {
						t.Fatalf("unexpected warning: %s", stderr)
					}
					spec := doc["spec"].(map[string]any)
					if !reflect.DeepEqual(spec["body"], tc.want) || spec["field_value"] != tc.wantFixed {
						t.Errorf("wrong conflict values, want %v, fix %q:\n%s", tc.want, tc.wantFixed, out)
					}
				})
			}
		}
	}
}

func TestDescribeAlertConflictTableShowsDisplayUnits(t *testing.T) {
	conflictMock(t, `{"pipeline_value":"0.09","pipeline_value_is_number":true,"base_displaced_value":0.125}`, `"0.13"`, `{"type":"Decimal","is_percent":true}`, http.StatusOK)
	cmd := setupAlertTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "alert", "1", "-o", "table", "--max-column-width", "0"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"9.00%", "12.50%", "13.00%"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table missing %q:\n%s", want, out.String())
		}
	}
}

func TestDescribeAlertConflictRawKeepsStoredValues(t *testing.T) {
	mock := conflictMock(t, `{"pipeline_value":"0.09","pipeline_value_is_number":true,"base_displaced_value":null}`, `"0.13"`, `{}`, http.StatusForbidden)
	doc, out, stderr := execConflict(t, "-vv", "-o", "json")
	body := doc["raw_response"].(map[string]any)["body"].(map[string]any)
	if body["pipeline_value"] != "0.09" {
		t.Errorf("raw pipeline changed: %s", out)
	}
	if value, exists := body["base_displaced_value"]; !exists || value != nil {
		t.Errorf("explicit null baseline lost: %s", out)
	}
	if stderr != "" || len(mock.Requests()) != 1 {
		t.Errorf("raw dump fetched schema: %s; %v", stderr, mock.Requests())
	}
}

func TestDescribeAlertConflictSchemaFailureWarnsAboutUnits(t *testing.T) {
	conflictMock(t, `{"pipeline_value":"0.09","pipeline_value_is_number":true}`, `"0.13"`, `{}`, http.StatusForbidden)
	doc, _, stderr := execConflict(t, "-o", "json")
	if !strings.Contains(stderr, "conflict values use stored units; column display format unavailable") {
		t.Errorf("missing units warning: %s", stderr)
	}
	if doc["spec"].(map[string]any)["body"].(map[string]any)["pipeline_value"] != "0.09" {
		t.Error("schema failure changed the stored value")
	}
}

// A viewer who sees the conflict's column masked gets mask text and
// masked_fields naming it (atlas capi/capi/controllers/pii_masking.py
// mask_issue_detail). masked_fields alone decides: the values print as sent,
// even one that reads as a number, and no schema is read for their units.
func TestDescribeAlertConflictMaskedValuesPrintAsSent(t *testing.T) {
	mock := conflictMock(t, `{"pipeline_value":"0.09","pipeline_value_is_number":true,"base_displaced_value":"•••••"}`,
		`"•••••0.13","masked_fields":["discount"]`, `{"type":"Decimal","is_percent":true}`, http.StatusOK)
	doc, out, stderr := execConflict(t, "-o", "json")
	spec := doc["spec"].(map[string]any)
	body := spec["body"].(map[string]any)
	if spec["field_value"] != "•••••0.13" || body["pipeline_value"] != "0.09" || body["base_displaced_value"] != "•••••" {
		t.Errorf("masked conflict values were reformatted:\n%s", out)
	}
	if stderr != "" || len(mock.Requests()) != 1 {
		t.Errorf("masked conflict fetched the schema: %s; %v", stderr, mock.Requests())
	}
}
