package copycmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// resultTestState returns state as a request entry's state.
func resultTestState(state api.DatasetCopyDataSourceRequestState) *api.DatasetCopyDataSourceRequestState {
	return &state
}

// resultTestPlan is a copy of table 42 taking each of entries, out of an
// active s3 source 12, a draft sftp source 15 and an active simple_url
// source 18.
func resultTestPlan(entries ...api.DatasetCopyDataSourceRequest) *copyTablePlan {
	plan := &copyTablePlan{
		source: "table orders (id 42)",
		offered: &datasets.CopyOptions{DataSources: []api.DatasetCopyDataSourceInfo{
			{ID: 12, Name: "orders-s3", DataSourceType: api.DataSourceTypeS3, State: api.DataSourceStateActive,
				SecretsToResupply: []string{"s3_secret_key"}},
			{ID: 15, Name: "orders-sftp", DataSourceType: api.DataSourceTypeSftp, State: api.DataSourceStateDraft,
				SecretsToResupply: []string{"gpg_passphrase", "sftp_password"}},
			{ID: 18, Name: "orders-url", DataSourceType: api.DataSourceTypeSimpleURL, State: api.DataSourceStateActive,
				SecretsToResupply: []string{"simple_url"}},
		}},
	}
	if len(entries) > 0 {
		plan.request.DataSources = &entries
	}
	return plan
}

// resultTestResponse is the response to a copy into table 57 that carried
// the items given as JSON, and the result decoded from it as the client
// decodes it.
func resultTestResponse(t *testing.T, items string) (*datasets.CopyResult, []byte) {
	t.Helper()
	raw := []byte(`{"id": 57, "name": "Orders EU", "dataset_copy": {"id": 3, "items": [` + items + `]}}`)
	var result datasets.CopyResult
	if err := jsonnum.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return &result, raw
}

func writeTestResult(t *testing.T, format string, plan *copyTablePlan, result *datasets.CopyResult, raw []byte) string {
	t.Helper()
	var out bytes.Buffer
	if err := writeCopyResult(&out, format, plan, result, raw); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const resultTestTableItems = `
	{"id": 1, "item_type": "settings", "action": "created", "source_object_id": "9", "target_object_id": "31", "context": null},
	{"id": 2, "item_type": "migration_model", "action": "created", "source_object_id": "4", "target_object_id": "8",
	 "note": "Rule output fields left out: score."}`

// Each data source copy shows the secrets the server says it still lacks, and
// each draft copy gets a Next: line that names them and turns it on; an
// active copy gets none.
func TestWriteCopyResult_ListsSecretsToResupplyAndActivatesDrafts(t *testing.T) {
	plan := resultTestPlan(
		api.DatasetCopyDataSourceRequest{SourceID: 12, State: resultTestState(api.DatasetCopyDataSourceRequestStateDraft)},
		api.DatasetCopyDataSourceRequest{SourceID: 15},
		api.DatasetCopyDataSourceRequest{SourceID: 18},
	)
	result, raw := resultTestResponse(t, resultTestTableItems+`,
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "40",
	 "note": "Changed: s3_prefix, state.", "context": {"secrets_to_resupply": ["s3_secret_key"]}},
	{"id": 4, "item_type": "data_source", "action": "created", "source_object_id": "15", "target_object_id": "41",
	 "context": {"secrets_to_resupply": ["gpg_passphrase", "sftp_password"]}},
	{"id": 5, "item_type": "data_source", "action": "created", "source_object_id": "18", "target_object_id": "42",
	 "context": {"secrets_to_resupply": []}}`)
	want := `
Created table "Orders EU" (id 57), a copy of table orders (id 42).
  ITEM TYPE        ACTION   SOURCE  TARGET  SECRETS TO RESUPPLY            NOTE
  settings         created  9       31      -                              -
  migration_model  created  4       8       -                              Rule output fields left out: score.
  data_source      created  12      40      s3_secret_key                  Changed: s3_prefix, state.
  data_source      created  15      41      gpg_passphrase, sftp_password  -
  data_source      created  18      42      -                              -
Next: qctl describe table 57
Next: cloud source 40 is a draft without s3_secret_key: add it to the spec of 'qctl get cloud-source 40 -o yaml', apply that with 'qctl apply -f', then run 'qctl enable cloud-source 40'
Next: cloud source 41 is a draft without gpg_passphrase, sftp_password: add them to the spec of 'qctl get cloud-source 41 -o yaml', apply that with 'qctl apply -f', then run 'qctl enable cloud-source 41'
`
	if got := writeTestResult(t, "table", plan, result, raw); got != want {
		t.Errorf("result =\n%s\nwant\n%s", got, want)
	}
}

// With no data source copy lacking a secret the table has no secrets column,
// and a draft copy's Next: line only turns it on.
func TestWriteCopyResult_DraftWithNothingToResupply(t *testing.T) {
	plan := resultTestPlan(api.DatasetCopyDataSourceRequest{SourceID: 15})
	result, raw := resultTestResponse(t, resultTestTableItems+`,
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "15", "target_object_id": "41",
	 "context": {"secrets_to_resupply": []}}`)
	want := `
Created table "Orders EU" (id 57), a copy of table orders (id 42).
  ITEM TYPE        ACTION   SOURCE  TARGET  NOTE
  settings         created  9       31      -
  migration_model  created  4       8       Rule output fields left out: score.
  data_source      created  15      41      -
Next: qctl describe table 57
Next: qctl enable cloud-source 41
`
	if got := writeTestResult(t, "table", plan, result, raw); got != want {
		t.Errorf("result =\n%s\nwant\n%s", got, want)
	}
}

// A copy without data sources prints what it printed before secrets were
// reported, and a secrets_to_resupply on an item that is not a data source
// is not shown.
func TestWriteCopyResult_WithoutDataSourcesIsUnchanged(t *testing.T) {
	result, raw := resultTestResponse(t, resultTestTableItems+`,
	{"id": 3, "item_type": "value_maps", "action": "created", "source_object_id": "9", "target_object_id": "31",
	 "context": {"secrets_to_resupply": ["s3_secret_key"]}}`)
	want := `
Created table "Orders EU" (id 57), a copy of table orders (id 42).
  ITEM TYPE        ACTION   SOURCE  TARGET  NOTE
  settings         created  9       31      -
  migration_model  created  4       8       Rule output fields left out: score.
  value_maps       created  9       31      -
Next: qctl describe table 57
`
	if got := writeTestResult(t, "table", resultTestPlan(), result, raw); got != want {
		t.Errorf("result =\n%s\nwant\n%s", got, want)
	}
}

// An item takes the state of the entries copying its source when they agree,
// in whatever order the items come; for one source copied in two states, the
// state of the entry at the item's position among the data source items, and
// none, so no Next: line, when that entry copies another source.
func TestWriteCopyResult_MatchesItemsToEntries(t *testing.T) {
	draft := resultTestState(api.DatasetCopyDataSourceRequestStateDraft)
	active := resultTestState(api.DatasetCopyDataSourceRequestStateActive)
	// 12 as a draft, 15 as its source's draft, 12 as its source's active.
	twice := []api.DatasetCopyDataSourceRequest{{SourceID: 12, State: draft}, {SourceID: 15}, {SourceID: 12}}
	tests := []struct {
		name     string
		entries  []api.DatasetCopyDataSourceRequest
		items    string
		wantNext []string
	}{
		{"distinct sources out of request order",
			[]api.DatasetCopyDataSourceRequest{{SourceID: 12, State: draft}, {SourceID: 15, State: active}}, `
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "15", "target_object_id": "41"},
	{"id": 4, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "40"}`,
			[]string{"Next: qctl describe table 57", "Next: qctl enable cloud-source 40"}},
		{"one source twice in request order", twice, `
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "40"},
	{"id": 4, "item_type": "data_source", "action": "created", "source_object_id": "15", "target_object_id": "41"},
	{"id": 5, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "42"}`,
			[]string{"Next: qctl describe table 57", "Next: qctl enable cloud-source 40", "Next: qctl enable cloud-source 41"}},
		{"one source twice out of request order", twice, `
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "15", "target_object_id": "41"},
	{"id": 4, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "40"},
	{"id": 5, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "42"}`,
			[]string{"Next: qctl describe table 57", "Next: qctl enable cloud-source 41"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, raw := resultTestResponse(t, tt.items)
			out := writeTestResult(t, "table", resultTestPlan(tt.entries...), result, raw)
			var next []string
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "Next:") {
					next = append(next, line)
				}
			}
			if strings.Join(next, "\n") != strings.Join(tt.wantNext, "\n") {
				t.Errorf("Next: lines = %q, want %q\n%s", next, tt.wantNext, out)
			}
		})
	}
}

// A field name from the server is rendered for one cell, and a list member
// that is not a string is left out.
func TestWriteCopyResult_SanitizesSecretNames(t *testing.T) {
	plan := resultTestPlan(api.DatasetCopyDataSourceRequest{SourceID: 18})
	result, raw := resultTestResponse(t, `
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "18", "target_object_id": "42",
	 "context": {"secrets_to_resupply": ["simple_url\u001b[2J", 7, null]}}`)
	out := writeTestResult(t, "table", plan, result, raw)
	if !strings.Contains(out, `  data_source  created  18      42      simple_url\x1b[2J    -`) || strings.Contains(out, "\x1b") {
		t.Errorf("result does not show the escaped name alone:\n%q", out)
	}
}

// -o json|yaml print the response as received and -o name the new table,
// whatever the copy lacks.
func TestWriteCopyResult_OtherFormatsAreTheResponse(t *testing.T) {
	plan := resultTestPlan(api.DatasetCopyDataSourceRequest{SourceID: 12, State: resultTestState(api.DatasetCopyDataSourceRequestStateDraft)})
	result, raw := resultTestResponse(t, `
	{"id": 3, "item_type": "data_source", "action": "created", "source_object_id": "12", "target_object_id": "40",
	 "context": {"secrets_to_resupply": ["s3_secret_key"]}}`)
	var wantJSON, wantYAML bytes.Buffer
	if err := output.WriteIndentedJSON(&wantJSON, raw); err != nil {
		t.Fatal(err)
	}
	if err := output.WriteJSONAsYAML(&wantYAML, raw); err != nil {
		t.Fatal(err)
	}
	for format, want := range map[string]string{"json": wantJSON.String(), "yaml": wantYAML.String(), "name": "table/57\n"} {
		if got := writeTestResult(t, format, plan, result, raw); got != want {
			t.Errorf("-o %s =\n%s\nwant\n%s", format, got, want)
		}
	}
}
