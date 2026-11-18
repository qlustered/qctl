package deliverables

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func literalColumn(value string) api.ColumnBindingResponse {
	return api.ColumnBindingResponse{
		ID: uuid.MustParse(bpColumnA), TargetColumnIndex: 1,
		SourceType: api.DeliverableColumnSourceTypeLiteral, Origin: api.DeliverableColumnBindingOriginUser,
		LiteralValue: &value,
	}
}

func TestLiteralColumnManifestSources(t *testing.T) {
	for _, value := range []string{"", "Acme", "0.25", "2026-10-13", "=SUM(A1:A9)", "first\nsecond"} {
		for _, doc := range []string{
			validSheet("columns:", "  A: {literal: "+jsonQuoted(value)+", format: '0%'}"),
			sheetJSONDoc(`{"table":7,"header_rows":[],"data_start_row":1,"columns":{"A":{"literal":` + jsonQuoted(value) + `,"format":"0%"}}}`),
		} {
			m, err := ParseManifest([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			column := m.Spec.Sheets["Data"].ColumnsByIndex()[1]
			if column.Literal == nil || *column.Literal != value || column.Format == nil || *column.Format != "0%" {
				t.Fatalf("literal %q decoded as %+v", value, column)
			}
		}
	}
	for _, source := range []string{
		`{literal: null}`, `{literal: "", column: amount}`, `{literal: "Acme", run_param: period}`,
		`{literal: "Acme", column: amount, run_param: period}`, `{literal: []}`, `{literal: {value: Acme}}`,
	} {
		_, err := ParseManifest([]byte(validSheet("columns:", "  A: "+source)))
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.sheets.Data.columns.A") {
			t.Errorf("source %s: error %v (exit %d), want exit 2 and manifest path", source, err, qerrors.GetExitCode(err))
		}
	}
}

func jsonQuoted(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func TestLiteralColumnDiffAndSourceChanges(t *testing.T) {
	stored := literalColumn("Acme")
	for _, tc := range []struct {
		name, column, source string
		literal              any
		changed              bool
	}{
		{"unchanged", `{literal: "Acme"}`, "literal", "Acme", false},
		{"changed text", `{literal: "Other"}`, "literal", "Other", true},
		{"empty text", `{literal: ""}`, "literal", "", true},
		{"format", `{literal: "Acme", format: "@"}`, "literal", "Acme", true},
		{"table source", `{column: amount}`, "column", nil, true},
		{"run parameter", `{run_param: period}`, "run_param", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bpPlan(t, bpData("columns: {A: "+tc.column+"}"))
			requests, err := p.bindingRequests(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, stored)))
			if err != nil {
				t.Fatal(err)
			}
			if !tc.changed {
				if len(requests) != 0 {
					t.Fatalf("matching literal planned %v", requests)
				}
				return
			}
			if len(requests) != 1 || requests[0].Method != http.MethodPatch {
				t.Fatalf("requests = %+v, want one PATCH", requests)
			}
			body := requests[0].Body
			if value, written := body[wireLiteralValue]; !written || value != tc.literal || body[wireSourceType] != tc.source {
				t.Errorf("body = %+v, want source %s with literal_value %#v", body, tc.source, tc.literal)
			}
		})
	}
	for _, previous := range []api.ColumnBindingResponse{bpColumn(bpColumnA, 1, "amount"), bpColumn(bpColumnA, 1, "$period")} {
		p := bpPlan(t, bpData(`columns: {A: {literal: "0.25", format: "0%"}}`))
		requests := p.columnRequests("Data", uuid.MustParse(bpDataID), []api.ColumnBindingResponse{previous})
		want := map[string]any{
			wireTargetColumnIndex: 1, wireSourceType: "literal", wireSourceColumn: nil,
			wireRunParam: nil, wireLiteralValue: "0.25", wireFormatOverride: "0%",
		}
		if len(requests) != 1 || !reflect.DeepEqual(requests[0].Body, want) {
			t.Fatalf("switch from %s = %+v, want %+v", previous.SourceType, requests, want)
		}
	}
}

func TestLiteralBindingResponsesPreserveValues(t *testing.T) {
	column := literalColumn("")
	column.NeedsConfirmation = true
	format := "0%"
	column.FormatOverride = &format
	sheet := bpSheet(bpDataID, "Data", 5, []int{1}, 2, column)
	tree := bpTree(sheet)
	columnBody, _ := json.Marshal(column)
	sheetBody, _ := json.Marshal(sheet)
	treeBody, _ := json.Marshal(tree)
	for _, tc := range []struct {
		name, method, path string
		body               []byte
		read               func(*Client) (*string, error)
	}{
		{"revision", "GET", bpTreePath + revV3, treeBody, func(c *Client) (*string, error) {
			r, err := c.GetRevision("token", uuid.MustParse(revV3))
			if err != nil {
				return nil, err
			}
			return (*(*r.SheetBindings)[0].ColumnBindings)[0].LiteralValue, nil
		}},
		{"enable", "POST", bpTreePath + revV3 + "/enable", treeBody, func(c *Client) (*string, error) {
			r, err := c.Enable("token", uuid.MustParse(revV3))
			if err != nil {
				return nil, err
			}
			return (*(*r.SheetBindings)[0].ColumnBindings)[0].LiteralValue, nil
		}},
		{"revision create", "POST", "/api/orgs/" + testOrgID + "/deliverables/" + monthlyID + "/revisions", treeBody, func(c *Client) (*string, error) {
			r, err := c.CreateRevision("token", uuid.MustParse(monthlyID), map[string]any{"release": "v3"})
			if err != nil {
				return nil, err
			}
			return (*(*createdTree(r.DeliverableRevisionCreateResponse).SheetBindings)[0].ColumnBindings)[0].LiteralValue, nil
		}},
		{"sheet create", "POST", bpTreePath + revV3 + "/sheet-bindings", sheetBody, func(c *Client) (*string, error) {
			r, err := c.CreateSheetBinding("token", uuid.MustParse(revV3), nil)
			if err != nil {
				return nil, err
			}
			return (*r.ColumnBindings)[0].LiteralValue, nil
		}},
		{"sheet patch", "PATCH", bpTreePath + revV3 + "/sheet-bindings/" + bpDataID, sheetBody, func(c *Client) (*string, error) {
			r, err := c.PatchSheetBinding("token", uuid.MustParse(revV3), uuid.MustParse(bpDataID), nil)
			if err != nil {
				return nil, err
			}
			return (*r.ColumnBindings)[0].LiteralValue, nil
		}},
		{"column create", "POST", "/api/orgs/" + testOrgID + bpDataColumns, columnBody, func(c *Client) (*string, error) {
			r, err := c.CreateColumnBinding("token", uuid.MustParse(bpDataID), manifestColumnBody(1, ManifestColumn{Literal: column.LiteralValue, Format: &format}))
			if err != nil {
				return nil, err
			}
			return r.LiteralValue, nil
		}},
		{"confirm column", "PATCH", "/api/orgs/" + testOrgID + bpDataColumns + "/" + bpColumnA, columnBody, func(c *Client) (*string, error) {
			r, err := c.ConfirmColumns("token", sheet, []int{1})
			if err != nil {
				return nil, err
			}
			return r.Bindings()[0].LiteralValue, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, req *http.Request) {
				if tc.name == "confirm column" || tc.name == "column create" {
					var body map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body[wireLiteralValue] != "" || body[wireSourceType] != "literal" || body[wireSourceColumn] != nil ||
						body[wireRunParam] != nil || body[wireFormatOverride] != format {
						t.Errorf("sent body = %+v, want literal and format preserved", body)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.body))
			})
			value, err := tc.read(NewClient(mock.URL(), testOrgID, 0))
			if err != nil || value == nil || *value != "" {
				t.Fatalf("literal = %v, error = %v, want empty text", value, err)
			}
		})
	}
}

func TestLiteralColumnDisplayAndRunParams(t *testing.T) {
	column := literalColumn("")
	tree := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, column))
	if got := FormatRevision(&RevisionDetail{DeliverableRevisionResponse: tree}); !strings.Contains(got, `Column 1 (A) <- literal ""`) {
		t.Errorf("revision did not identify empty literal: %s", got)
	}
	if params := RequiredRunParams(tree, "report.xlsx", nil); len(params) != 0 {
		t.Errorf("literal requires run params %v", params)
	}
}

func TestLiteralColumnSuggestionsPreserveAndDisplayTheValue(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	column := literalColumn("Acme")
	body, _ := json.Marshal(column)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+bpDataColumns+"/apply-suggestions", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"created_count":0,"replaced_count":0,"empty_columns":[],"column_bindings":[`+string(body)+`]}`))
	})
	r, err := NewClient(mock.URL(), testOrgID, 0).AcceptColumnSuggestions("token", uuid.MustParse(bpDataID))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ColumnBindings) != 1 || r.ColumnBindings[0].LiteralValue == nil || *r.ColumnBindings[0].LiteralValue != "Acme" {
		t.Fatalf("suggestions lost literal: %+v", r)
	}
	if report := FormatColumnSuggestions(r); !strings.Contains(report, `literal "Acme"`) {
		t.Errorf("suggestions report = %s", report)
	}
}

func TestLiteralColumnReanchorKeepsMatchingValue(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	column := literalColumn("Acme")
	stored := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, column))
	moved := column
	moved.TargetColumnIndex = 2
	final := bpTree(bpSheet(bpDataID, "Data", 5, []int{2}, 3, moved))
	s.trees(revV3, stored, final, final)
	s.on(http.MethodPatch, bpSheets+"/"+bpDataID, api.SheetBindingPatchResponse{ID: uuid.MustParse(bpDataID), SheetName: "Data",
		MemberDatasetID: 5, HeaderRows: []int{2}, DataStartRow: 3,
		AnchorReport: &api.BindingAnchorReport{ColumnBindings: []api.ColumnAnchorChange{{
			ColumnBindingID: column.ID, FromColumnIndex: 1, ToColumnIndex: 2, Outcome: api.BindingAnchorOutcomeMoved}}},
		ColumnBindings: &[]api.ColumnBindingResponse{moved},
	})
	m := bpManifest(t, "", bpSheetDoc("Data", bpBase, "header_rows: [2]", "data_start_row: 3", `columns: {B: {literal: "Acme"}}`))
	p, _ := s.prepare(m, "")
	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatal(err)
	}
	if r.BindingWrites != 1 || len(r.AnchorReports) != 1 {
		t.Fatalf("re-anchor performed extra writes: %+v", r)
	}
	if c := r.Manifest.Spec.Sheets["Data"].Columns["B"]; c.Literal == nil || *c.Literal != "Acme" {
		t.Errorf("re-anchor export = %+v, want literal Acme", c)
	}
}
