package apply

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/destinations"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func TestApplyNamePrintsEveryKindInFileOrder(t *testing.T) {
	env, mock := setupTableSchemaApplyEnv(t)
	registerTableApplyMocks(t, mock)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	base := "/api/orgs/" + testOrgID
	respond := func(method, path string, status int, body any) {
		mock.RegisterHandler(method, base+path, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, status, body)
		})
	}
	respond("GET", "/destinations", http.StatusOK, destinations.ListPage{Results: []destinations.DestinationTiny{}})
	respond("POST", "/destinations", http.StatusOK, sampleDestinationFullForApply())
	respond("GET", "/backup-settings", http.StatusOK, backup_settings.ListPage{Results: []backup_settings.BackupSettingsTiny{}})
	respond("POST", "/backup-settings", http.StatusOK, applyBackupSettingsFixture("nightly"))
	respond("GET", "/data-sources", http.StatusOK, cloud_sources.CloudSourcesPage{Results: []cloud_sources.CloudSourceTiny{}})
	respond("GET", "/datasets/11", http.StatusOK, datasets.DatasetFull{ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 2}})
	respond("POST", "/data-sources", http.StatusOK, sampleCloudSourceFull())
	respond("POST", "/composite-datasets/apply", http.StatusOK, linkedTableApplyAnswer(nil))
	registerRuleGetHandler(mock, testRuleID, sampleLiveRule())
	registerRulePatchHandler(mock, testRuleID, nil)
	registerTableRulesList(mock, 1)
	registerPendingCreateSuggestions(t, mock)
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &api.InstantiateRuleRequestSchema{})
	respond("GET", "/rule-review-policy", http.StatusOK, effectiveRulePolicyJSON("system_default"))
	respond("PUT", "/rule-review-policy", http.StatusOK, rulePolicyResponseJSON())
	respond("GET", "/data-review-policy", http.StatusOK, dataPolicyResponseJSON("system_default", false))
	respond("PUT", "/data-review-policy", http.StatusOK, dataPolicyResponseJSON("organization", true))
	respond("GET", "/change-review-policy", http.StatusOK, changePolicyResponseJSON(nil))
	respond("GET", "/notifications/email-preferences", http.StatusOK, map[string]any{
		"preferences": map[string]bool{"period_summary": true}, "defaults": map[string]bool{"period_summary": true}})
	respond("PUT", "/notifications/email-preferences", http.StatusOK, map[string]any{
		"preferences": map[string]bool{"period_summary": false}, "defaults": map[string]bool{"period_summary": true}})
	respond("PUT", "/change-review-policy", http.StatusOK, changePolicyResponseJSON(nil))
	id := uuid.MustParse(testTableRuleID)
	respond("GET", "/concepts", http.StatusOK, api.ConceptsListSchema{Results: []api.ConceptTinySchema{}})
	respond("POST", "/concepts", http.StatusCreated, api.ConceptSchemaFull{ID: id, Slug: "amount", Name: "Amount"})
	respond("GET", "/problem-domains", http.StatusOK, dataset_kinds.DatasetKindsPage{Results: []dataset_kinds.DatasetKindTiny{}})
	respond("POST", "/problem-domains", http.StatusCreated, dataset_kinds.DatasetKindWithConcepts{ID: id, Slug: "orders", Name: "Orders"})
	respond("GET", "/table-series", http.StatusOK, []table_series.Series{})
	respond("POST", "/table-series", http.StatusCreated, table_series.Series{ID: id, Name: "Monthly"})
	respond("GET", "/table-series/"+id.String(), http.StatusOK, table_series.Series{ID: id, Name: "Monthly"})
	respond("GET", "/deliverables", http.StatusOK, map[string]any{"results": []any{}, "total_rows": 0})
	respond("POST", "/deliverables", http.StatusOK, deliverables.Deliverable{ID: id, Slug: "report", Name: "Report", FilenamePattern: "report.xlsx"})

	cases := []struct{ kind, document, name string }{
		{"Table", tableDoc("orders"), "table/1"},
		{"Destination", "metadata: {name: postgres-prod}\nspec: {type: postgresql, host: db.example.com, port: 5432, database_name: production, user: admin}\n", "destination/1"},
		{"BackupSettings", "metadata: {name: nightly}\nspec: {backup_type: s3, backup_s3_bucket: archive, backup_s3_access_key: key, backup_s3_secret_key: secret}\n", "backup-settings/7"},
		{"CloudSource", "metadata: {name: s3-orders}\nspec: {dataset_id: 11, settings_model_id: 2, data_source_type: s3, state: draft, s3_bucket: raw-orders, s3_region_name: us-east-1}\n", "cloud-source/42"},
		{"LinkedTable", linkedTableYAML(), "linked-table/7"},
		{"Rule", "metadata: {id: urn:uuid:" + testRuleID + "}\nspec: {state: disabled}\n", "rule/" + testRuleID},
		{"TableRule", instantiate202Manifest, "table-rule/" + testTableRuleID},
		{"TableSchema", retypeAmount, "table/42"},
		{"RuleReviewPolicy", "metadata: {name: org-policy}\nspec: {requires_review: true}\n", "rule-review-policy/11111111-2222-3333-4444-555555555555"},
		{"ChangeReviewPolicy", "metadata: {name: org-policy}\nspec: {require_distinct_approver: true, operations: {}}\n", "change-review-policy/11111111-2222-3333-4444-555555555555"},
		{"NotificationPreferences", "metadata: {name: me}\nspec: {email: {period_summary: false}}\n", "notification-preference/me"},
		{"DataReviewPolicy", "metadata: {name: org-policy}\nspec: {requires_review: true}\n", "data-review-policy/22222222-3333-4444-5555-666666666666"},
		{"StandardField", "metadata: {slug: amount}\nspec: {name: Amount}\n", "standard-field/" + id.String()},
		{"TableKind", "metadata: {slug: orders}\nspec: {name: Orders, standard_fields: []}\n", "table-kind/" + id.String()},
		{"TableSeries", "metadata: {name: Monthly}\nspec: {members: []}\n", "table-series/" + id.String()},
		{"Deliverable", "metadata: {slug: report}\nspec: {name: Report, filename_pattern: report.xlsx}\n", "deliverable/" + id.String()},
	}
	var kinds, documents, names []string
	for _, tc := range cases {
		kinds = append(kinds, tc.kind)
		document := tc.document
		if !strings.Contains(document, "apiVersion:") {
			document = "apiVersion: qluster.ai/v1\nkind: " + tc.kind + "\n" + document
		}
		documents = append(documents, strings.TrimSpace(document)+"\n")
		names = append(names, tc.name)
	}
	// A TableRuleTemplate must be the only document in its file, so it cannot
	// join this one; TestApplyTemplateOutputFormats covers its -o name.
	kinds = append(kinds, "TableRuleTemplate")
	sort.Strings(kinds)
	if !reflect.DeepEqual(kinds, applyKinds()) {
		t.Fatalf("tested kinds = %v, want %v", kinds, applyKinds())
	}
	stdout, stderr, err := execApplyStreams(t, env.CreateFile("all.yaml", strings.Join(documents, "---\n")), "--yes", "-o", "name")
	if err != nil {
		t.Fatalf("apply: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if want := strings.Join(names, "\n") + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if !strings.Contains(stderr, "16 documents: 16 succeeded") || !strings.Contains(stderr, "created") {
		t.Errorf("stderr lacks the results and summary: %s", stderr)
	}
	if gets := countRequests(mock, "/rule-revisions/"+testRuleID+"/details"); gets != 1 {
		t.Errorf("revision reads = %d, want only the pre-apply read", gets)
	}
	for i, tc := range cases[:5] {
		t.Run("typed/"+tc.kind, func(t *testing.T) {
			cmd := setupApplyRoot()
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			cmd.SetArgs([]string{"apply", resourceNameForKind(tc.kind), "-f", env.CreateFile(tc.kind+".yaml", documents[i]), "--yes", "-o", "name"})
			if err := cmd.Execute(); err != nil || out.String() != tc.name+"\n" {
				t.Fatalf("stdout = %q, stderr = %q, err = %v", out.String(), diagnostics.String(), err)
			}
		})
	}

	respond("GET", "/concepts", http.StatusOK, map[string]any{"results": []any{map[string]any{"id": id, "slug": "amount", "name": "Amount"}}, "total_rows": 1})
	respond("GET", "/concepts/"+id.String(), http.StatusOK, api.ConceptSchemaFull{ID: id, Slug: "amount", Name: "Amount"})
	respond("GET", "/problem-domains", http.StatusOK, map[string]any{"results": []any{map[string]any{"id": id, "slug": "orders", "name": "Orders"}}, "total_rows": 1})
	respond("GET", "/problem-domains/"+id.String(), http.StatusOK, dataset_kinds.DatasetKindWithConcepts{ID: id, Slug: "orders", Name: "Orders"})
	respond("GET", "/table-series", http.StatusOK, []table_series.Series{{ID: id, Name: "Monthly"}})
	respond("GET", "/deliverables", http.StatusOK, map[string]any{"results": []any{map[string]any{"id": id, "slug": "report"}}, "total_rows": 1})
	respond("GET", "/deliverables/"+id.String(), http.StatusOK, deliverables.Deliverable{ID: id, Slug: "report", Name: "Report", FilenamePattern: "report.xlsx"})
	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	unchanged := append([]string{}, documents[12:]...)
	unchanged = append(unchanged, "apiVersion: qluster.ai/v1\nkind: Rule\nmetadata: {id: "+testRuleID+"}\nspec: {}\n",
		"apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {id: "+testTableRuleID+", instance_name: email_check}\nspec: {dataset_id: 1}\n")
	wanted := append([]string{}, names[12:]...)
	wanted = append(wanted, "rule/"+testRuleID, "table-rule/"+testTableRuleID)
	before := len(mock.Requests())
	stdout, stderr, err = execApplyStreams(t, env.CreateFile("unchanged.yaml", strings.Join(unchanged, "---\n")), "-o", "name")
	if err != nil || stdout != strings.Join(wanted, "\n")+"\n" || strings.Count(stderr, "unchanged") != 6 {
		t.Fatalf("unchanged stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
	}
	for _, request := range mock.Requests()[before:] {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("unchanged apply sent %s", request)
		}
	}
}

func TestApplySchemaNameIncludesUnchangedDocuments(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	document := strings.Replace(retypeAmount, "type: Decimal\n      args: [12, 2]", "type: String\n      args: [64]", 1)
	stdout, stderr, err := execApplyStreams(t, writeManifest(t, document), "-o", "name")
	if err != nil || stdout != "table/42\n" || !strings.Contains(stderr, "unchanged") {
		t.Fatalf("stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
	}
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "PATCH ") {
			t.Errorf("unchanged schema sent %s", request)
		}
	}
}

func TestApplyStagedTableRuleNameIdentifiesTheSuggestion(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	suggestion := sampleSuggestion(api.SuggestionStatePending)
	suggestion.Action = api.SuggestionActionCreate
	registerInstantiate202Mocks(mock, suggestion)
	stdout, stderr, err := execApplyStreams(t, env.CreateFile("staged.yaml", instantiate202Manifest), "--yes", "-o", "name")
	if err != nil || stdout != "rule-suggestion/"+testSuggestionID+"\n" || !strings.Contains(stderr, "staged") {
		t.Fatalf("stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
	}
}

func TestApplyLinkedTableNameFallbacks(t *testing.T) {
	for _, tc := range []struct {
		body     string
		validate bool
		wantCode int
	}{
		{body: ""}, {body: "null"}, {body: "{}"},
		{body: `{"valid":true}`, validate: true},
		{body: `{"valid":false,"errors":[{"code":"invalid","message":"invalid"}]}`, validate: true, wantCode: qerrors.ExitBadUsage},
	} {
		t.Run(fmt.Sprintf("%s/%t", tc.body, tc.validate), func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			env := setupLinkedTableEnv(t, mock.URL())
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.body == "" {
					w.WriteHeader(http.StatusNoContent)
				}
				fmt.Fprint(w, tc.body)
			})
			args := []string{"-o", "name"}
			if tc.validate {
				args = append(args, "--validate-only")
			}
			stdout, stderr, err := execApplyStreams(t, env.CreateFile("linked.yaml", linkedTableYAML()), args...)
			want := "linked-table/orders-enriched\n"
			if tc.wantCode != 0 {
				want = ""
			}
			if stdout != want || qerrors.GetExitCode(err) != tc.wantCode {
				t.Fatalf("stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
			}
		})
	}
}

func TestApplyNameOmitsFailedAndUntriedDocuments(t *testing.T) {
	for _, failFast := range []bool{false, true} {
		t.Run(fmt.Sprint(failFast), func(t *testing.T) {
			setupTableSchemaApplyEnv(t)
			unchanged := strings.Replace(retypeAmount, "type: Decimal\n      args: [12, 2]", "type: String\n      args: [64]", 1)
			bad := "apiVersion: qluster.ai/v1\nkind: Table\nmetadata: {name: broken}\nspec: {}\n"
			args := []string{"-o", "name"}
			if failFast {
				args = append(args, "--fail-fast")
			}
			stdout, stderr, err := execApplyStreams(t, writeManifest(t, unchanged+"---\n"+bad+"---\n"+unchanged), args...)
			want := "table/42\ntable/42\n"
			if failFast {
				want = "table/42\n"
				if !strings.Contains(stderr, "skipped (--fail-fast)") {
					t.Errorf("stderr lacks the skipped document: %s", stderr)
				}
			}
			if stdout != want || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(stderr, "table/broken failed") {
				t.Fatalf("stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
			}
		})
	}
}

func TestApplyNameKeepsPartialResultsAndExitCodes(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupted), func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("output", "name", "")
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			docs := []manifestDoc{{Kind: "Table", Name: "first"}, {Kind: "Table", Name: "failed"}, {Kind: "Table", Name: "last"}}
			failure := error(qerrors.NewNotFound("missing"))
			if interrupted {
				failure = &qerrors.Interrupted{Signal: syscall.SIGINT}
			}
			calls := 0
			err := applyDocuments(cmd, docs, onlyKind(func(_ *cobra.Command, doc manifestDoc) (*applyResult, error) {
				calls++
				if doc.Name == "failed" {
					return namedApplyResult(map[string]any{"id": 2}, "table", 2), failure
				}
				return namedApplyResult(map[string]any{"id": calls}, "table", calls), nil
			}))
			wantNames, wantCalls := "table/1\ntable/2\ntable/3\n", 3
			if interrupted {
				wantNames, wantCalls = "table/1\ntable/2\n", 2
			}
			if stdout.String() != wantNames || calls != wantCalls || qerrors.GetExitCode(err) != qerrors.GetExitCode(failure) {
				t.Fatalf("stdout = %q, calls = %d, err = %v", stdout.String(), calls, err)
			}
		})
	}
}
