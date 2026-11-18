package dataview

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// maskedPageJSON is one clean row as get_data sends it to a member (masked
// false) and to a read_only viewer who sees discount and email masked (atlas
// capi/capi/controllers/pii_masking.py mask_data_page): the cells, the steward
// pin's values and the row log carry the mask, and masked_fields names every
// masked field of the dataset, phone included although the page does not
// select it.
func maskedPageJSON(masked bool) string {
	discount, displaced, email, logged := `"0.13"`, `0.125`, `"jane.doe@example.com"`, `"0.125"`
	maskedFields := ""
	if masked {
		discount, displaced, email, logged = `"•••••"`, `"•••••"`, `"•••••.com"`, `"•••••"`
		maskedFields = `"masked_fields": ["discount", "email", "phone"],`
	}
	return `{
  "total_page_count": 1, "total_rows": 1, "dataset_name": "work_orders",
  "fields": ["id", "discount", "email", "row_logs_"],
  "labels": [null, "Discount", "Email", null],
  "field_types": ["Integer", "Decimal", "Text", "JSON"],
  "field_display_formats": [null, {"kind": "percent", "decimals": 2}, null, null],
  "results": [[2000, ` + discount + `, ` + email + `, {"discount": [{"value": ` + logged + `, "why": "fix"}]}]],
  "steward_overrides": [{"discount": {"target_value": ` + discount + `, "base_displaced_value": ` + displaced + `}}],
  ` + maskedFields + `
  "clean_rows_count": 1, "bad_rows_count": 0
}`
}

func normalizeJSON(t *testing.T, body string, opts NormalizeOptions) *Page {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var schema api.DataWithCellHistorySchema
	if err := dec.Decode(&schema); err != nil {
		t.Fatalf("decode: %v", err)
	}
	page, err := Normalize(&schema, opts)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return page
}

func TestMaskedValuesPrintAsSent(t *testing.T) {
	opts := NormalizeOptions{RequestedPage: 1, ShowRowLogs: true}
	page := normalizeJSON(t, maskedPageJSON(true), opts)
	got := render(t, page, nil)

	for _, want := range []string{
		"Masked fields: discount, email, phone — ",
		"| 2000 | ••••• [S] | •••••.com |",
		"- 2000 discount: ••••• → •••••\n",
		`- Log: {"value":"•••••","why":"fix"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %q:\n%s", want, got)
		}
	}
}

func TestUnmaskedValuesKeepTheirDisplayFormat(t *testing.T) {
	opts := NormalizeOptions{RequestedPage: 1, ShowRowLogs: true}
	got := render(t, normalizeJSON(t, maskedPageJSON(false), opts), nil)

	for _, want := range []string{
		"| 2000 | 13.00% [S] | jane.doe@example.com |",
		"- 2000 discount: 12.50% → 13.00%\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Masked fields") {
		t.Errorf("a page with no masked_fields names masked fields:\n%s", got)
	}
}

// masked_fields, not the value's shape, decides: a masked field's value is
// never put through the column's display format, even one that reads as a
// number.
func TestMaskedFieldsSkipTheDisplayFormat(t *testing.T) {
	body := strings.Replace(maskedPageJSON(false), `"clean_rows_count"`, `"masked_fields": ["discount"], "clean_rows_count"`, 1)
	page := normalizeJSON(t, body, NormalizeOptions{RequestedPage: 1})
	if got := page.Rows[0].Cells[1].Display; got != "0.13" {
		t.Errorf("masked discount cell = %q, want it as sent", got)
	}
	if got := page.Rows[0].Cells[1].StewardOverride.Displaced; got != "0.125" {
		t.Errorf("masked displaced value = %q, want it as sent", got)
	}
	var buf bytes.Buffer
	if err := Render(&buf, page, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Masked fields: discount — ") {
		t.Errorf("summary does not name the masked field:\n%s", buf.String())
	}
}
