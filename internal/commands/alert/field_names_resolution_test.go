package alert

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const fieldNamesManifest = `apiVersion: qluster.ai/v1
kind: AlertResolution
metadata:
  id: 9
spec:
  field_names_resolution:
    - incoming_field_name: Customer Name
      field_name: customer_name
      operation: new
      scope: dataset
      expand: false
`

func fieldNamesAlert(issueType api.AlertType) alerts.AlertFull {
	return alerts.AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders", IssueType: issueType,
		Resolved: testutil.BoolPtr(false), FieldNamesSuggestionsInfo: &api.FieldNamesSuggestionsInfo{
			Suggestions: []api.FieldNameSuggestion{{IncomingFieldName: "Customer Name", RecommendedFieldNameToMapTo: "customer_name"}},
		}}
}

func TestResolveFileSendsFieldNamesWithoutAnAction(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeInitialFields, api.AlertTypeUnexpectedFieldName} {
		t.Run(string(issueType), func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerResolvingGetAlert(mock, fieldNamesAlert(issueType))
			posts := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
				posts++
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var raw map[string]any
				if err := json.Unmarshal(data, &raw); err != nil {
					t.Fatal(err)
				}
				if _, present := raw["action"]; present {
					t.Errorf("action must be absent: %s", data)
				}
				var sent api.AlertResolveSchema
				if err := json.Unmarshal(data, &sent); err != nil {
					t.Fatal(err)
				}
				if sent.FieldNamesResolution == nil || len(*sent.FieldNamesResolution) != 1 {
					t.Fatalf("mappings missing from request: %s", data)
				}
				field := (*sent.FieldNamesResolution)[0]
				if field.IncomingFieldName != "Customer Name" || field.FieldName != "customer_name" ||
					field.Operation != api.FieldNameResolutionOperatorNew || field.Scope != api.ScopeDataset || field.Expand == nil || *field.Expand {
					t.Errorf("mapping changed: %+v", field)
				}
				okResolve(w, r)
			})
			path := writeManifest(t, fieldNamesManifest)
			out, err := runAlertCommand(t, "alert", "resolve", "-f", path, "-o", "json")
			if err != nil || posts != 1 {
				t.Fatalf("resolve: %v, posts: %d\n%s", err, posts, out)
			}
			if !strings.Contains(out, "resolved — field_names_resolution") || !strings.Contains(out, `"action": ""`) || !strings.Contains(out, `"resolved": true`) {
				t.Errorf("outcome = %s", out)
			}
		})
	}
}

func TestResolveFileRefusesKeyColumnsOnAColumnMapping(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeInitialFields, api.AlertTypeUnexpectedFieldName} {
		t.Run(string(issueType), func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerResolvingGetAlert(mock, fieldNamesAlert(issueType))
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(http.ResponseWriter, *http.Request) {
				t.Error("a mapping carrying spec.key_columns must not POST")
			})
			doc := fieldNamesManifest + "  key_columns: [customer_name]\n"
			_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, doc))
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

func TestFieldNamesResolutionRefusesWrongOrSettledAlerts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alert alerts.AlertFull
	}{
		{"wrong type", mapValueAlert()},
		{"resolved", alerts.AlertFull{ID: 9, IssueType: api.AlertTypeInitialFields, Resolved: testutil.BoolPtr(true)}},
		{"parked", alerts.AlertFull{ID: 9, IssueType: api.AlertTypeInitialFields, ResolveAfterMigration: testutil.BoolPtr(true)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, tc.alert)
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(http.ResponseWriter, *http.Request) {
				t.Error("invalid target must not POST")
			})
			path := writeManifest(t, fieldNamesManifest)
			for _, args := range [][]string{{"alert", "resolve", "-f", path}, {"alert", "resolve", "9", "--dry-run"}} {
				_, err := runAlertCommand(t, args...)
				if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
					t.Fatalf("error = %v, want exit 2", err)
				}
			}
		})
	}
}

func TestFieldNamesDryRunGeneratesTemplateWithoutPosting(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeInitialFields, api.AlertTypeUnexpectedFieldName} {
		t.Run(string(issueType), func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, fieldNamesAlert(issueType))
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(http.ResponseWriter, *http.Request) {
				t.Error("dry-run must not POST")
			})
			out, err := runAlertCommand(t, "alert", "resolve", "9", "--dry-run")
			if err != nil {
				t.Fatal(err)
			}
			res, err := alerts.ParseResolution([]byte(out))
			if err != nil {
				t.Fatal(err)
			}
			if res.Spec.Action != "" || len(res.Spec.FieldNamesResolution) != 1 ||
				*res.Spec.FieldNamesResolution[0].IncomingFieldName != "Customer Name" {
				t.Fatalf("template = %s", out)
			}
			if err := res.Spec.Validate(); err == nil {
				t.Fatal("template must require an explicit decision")
			}
		})
	}
}

func TestFieldNamesResolutionReportsAnUnresolvedOutcome(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, fieldNamesAlert(api.AlertTypeInitialFields))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", okResolve)
	out, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, fieldNamesManifest))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "field_names_resolution accepted, but the alert is still unresolved") {
		t.Errorf("outcome = %s", out)
	}
}

func TestFieldNamesResolutionKeepsServerErrorExitCodes(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, fieldNamesAlert(api.AlertTypeInitialFields))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "Only the admin can add a new field"})
	})
	_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, fieldNamesManifest))
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("error = %v, want unauthorized", err)
	}
}

func TestFieldNamesResolutionKeepsTheDependencyGate(t *testing.T) {
	mock := newAlertTestEnv(t)
	a := fieldNamesAlert(api.AlertTypeUnexpectedFieldName)
	a.DependentAlert = &api.DependentAlertSchema{ID: 7}
	registerGetAlert(mock, a)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(http.ResponseWriter, *http.Request) {
		t.Error("dependency must prevent POST")
	})
	_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, fieldNamesManifest))
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "7") {
		t.Fatalf("dependency error = %v", err)
	}
}

func TestFieldNamesResolutionRefusesAScopeTheAlertDoesNotOpen(t *testing.T) {
	mock := newAlertTestEnv(t)
	a := fieldNamesAlert(api.AlertTypeUnexpectedFieldName)
	a.FieldMappingScopes = &[]api.Scope{api.ScopeDataSourceModel, api.ScopeStoredItem}
	registerGetAlert(mock, a)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(http.ResponseWriter, *http.Request) {
		t.Error("a closed scope must not POST")
	})
	manifest := strings.Replace(fieldNamesManifest, "operation: new", "operation: map", 1)
	_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, manifest))
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "data_source_model, stored_item") {
		t.Fatalf("error = %v, want exit 2 naming the open scopes", err)
	}
}
