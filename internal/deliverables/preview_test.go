package deliverables

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestPreviewDecodesExactRowAndCellNumbers(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/deliverable-revisions/"+revV1+"/preview", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"sheets":[{
"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
"columns":["id"],"rows":[{"id":9007199254740993,"nested":{"max":18446744073709551615}}],
"in_scope_count":1,"out_of_scope_count":0,"indeterminate_count":0}],
"cells":[{"sheet_name":"Alpha","cell_ref":"B1","value":9007199254740993}]}`))
	})
	result, err := NewClient(mock.URL(), testOrgID, 0).Preview("token", uuid.MustParse(revV1), RunParams{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	row := (*result.Sheets)[0].Rows[0]
	if got := row["id"]; got != int64(9007199254740993) {
		t.Errorf("row id = %v (%T)", got, got)
	}
	if got := row["nested"].(map[string]interface{})["max"]; got != json.Number("18446744073709551615") {
		t.Errorf("nested max = %v (%T)", got, got)
	}
	if got := *(*result.Cells)[0].Value; got != int64(9007199254740993) {
		t.Errorf("cell = %v (%T)", got, got)
	}
}

func TestPreviewTextEscapesTerminalControls(t *testing.T) {
	var result PreviewResult
	if err := json.Unmarshal([]byte(`{"sheets":[{"sheet_name":"Title\u001b[31m\nForged","columns":["a\tb"],
"rows":[{"a\tb":"value\nnew\u001b[31m"}],"in_scope_count":1}],
"cells":[{"sheet_name":"Alpha","cell_ref":"A1","value":"hello\tworld\n"}]}`), &result.RevisionPreviewResponse); err != nil {
		t.Fatal(err)
	}
	out := FormatPreview(&result)
	if strings.Contains(out, "\x1b") {
		t.Errorf("terminal escape survived: %q", out)
	}
	for _, want := range []string{`Title\x1b[31m\nForged`, `a\tb`, `value\nnew\x1b[31m`, `hello\tworld\n`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
}

func TestPreviewTextNamesMaskedFields(t *testing.T) {
	var result PreviewResult
	if err := jsonnum.Unmarshal([]byte(`{"sheets":[
{"sheet_name":"Alpha","member_dataset_id":42,"columns":["policy","email","ssn"],
 "rows":[{"policy":"P-1","email":"•••••.com","ssn":"•••••"},{"policy":"P-2","email":null,"ssn":"•••••"}],
 "masked_fields":["email","ssn"],"in_scope_count":2},
{"sheet_name":"Plain","member_dataset_id":43,"columns":["policy"],"rows":[{"policy":"P-3"}],"in_scope_count":1},
{"sheet_name":"Empty","member_dataset_id":44,"columns":["email"],"rows":[],"masked_fields":["email"]}],
"cells":[{"sheet_name":"Alpha","cell_ref":"B1","value":"•••••0.00","masked_fields":["premium\u001b[31m"]},
{"sheet_name":"Alpha","cell_ref":"B2","value":null,"masked_fields":["premium"]},
{"sheet_name":"Alpha","cell_ref":"B3","value":12}]}`), &result.RevisionPreviewResponse); err != nil {
		t.Fatal(err)
	}
	out := FormatPreview(&result)
	want := "Sheet Alpha (table 42): 2 kept / 0 left out / 0 blocked; showing 2 rows\n" +
		"Masked fields: email, ssn\n" +
		"policy  email      ssn\n" +
		"P-1     •••••.com  •••••\n" +
		"P-2     null       •••••\n" +
		"\n" +
		"Sheet Plain (table 43): 1 kept / 0 left out / 0 blocked; showing 1 rows\n" +
		"policy\n" +
		"P-3\n" +
		"\n" +
		"Sheet Empty (table 44): 0 kept / 0 left out / 0 blocked; showing 0 rows\n" +
		"  (no preview rows)\n" +
		"Cells:\n" +
		`  Alpha!B1 = •••••0.00 (masked: premium\x1b[31m)` + "\n" +
		"  Alpha!B2 = null (masked: premium)\n" +
		"  Alpha!B3 = 12\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
