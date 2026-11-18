package resources

import (
	"reflect"
	"strings"
	"testing"
)

const samplePage = `---
summary: rows of a table
aliases: [rows]
kind:
schema: openapi:DataSetSchemaFull
createOnly: [state]
guide: inspect-data
signature: 7f3a91c
---

## USAGE

    qctl get data <table-id-or-name>

## EXAMPLES

    # first page of rows, default 20 per page
    qctl get data orders

    # page 3, 100 rows, as JSON for scripting
    qctl get data orders --page 3 --limit 100 -o json

## GOTCHAS

- one backend page per call (default ` + "`--page 1 --limit 20`" + `); no auto-pagination
- flag values are validated by the backend, not by qctl [backend]

## SEE ALSO

- ` + "`qctl explain table-schema`" + `
- ` + "`qctl guide inspect-data`" + `
`

func TestParsePageFrontmatter(t *testing.T) {
	p, err := ParsePage("data", []byte(samplePage))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if p.Name != "data" || p.Summary != "rows of a table" {
		t.Errorf("name/summary = %q/%q", p.Name, p.Summary)
	}
	if !reflect.DeepEqual(p.Aliases, []string{"rows"}) {
		t.Errorf("aliases = %v, want [rows]", p.Aliases)
	}
	// A bare key is an empty value, not an error.
	if p.Kind != "" {
		t.Errorf("kind = %q, want empty", p.Kind)
	}
	if p.Schema != "openapi:DataSetSchemaFull" || p.Guide != "inspect-data" || p.Signature != "7f3a91c" {
		t.Errorf("schema/guide/signature = %q/%q/%q", p.Schema, p.Guide, p.Signature)
	}
	if !reflect.DeepEqual(p.CreateOnly, CreateOnlyFields{"state": nil}) {
		t.Errorf("createOnly = %v, want [state]", p.CreateOnly)
	}
}

func TestParsePageCreateOnlyForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		form string
		want CreateOnlyFields
	}{
		{"list", "[state, name]", CreateOnlyFields{"state": nil, "name": nil}},
		{"map", "{state: [draft], name: []}", CreateOnlyFields{"state": {"draft"}, "name": {}}},
		{"empty list", "[]", CreateOnlyFields{}},
		{"empty map", "{}", CreateOnlyFields{}},
		{"null", "null", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(samplePage, "createOnly: [state]", "createOnly: "+tc.form, 1)
			p, err := ParsePage("data", []byte(body))
			if err != nil {
				t.Fatalf("ParsePage: %v", err)
			}
			if !reflect.DeepEqual(p.CreateOnly, tc.want) {
				t.Errorf("createOnly = %v, want %v", p.CreateOnly, tc.want)
			}
		})
	}
}

func TestParsePageRejectsMalformedCreateOnly(t *testing.T) {
	for _, form := range []string{"state", "{state: draft}", "[[state]]", "{state: [[draft]]}"} {
		body := strings.Replace(samplePage, "createOnly: [state]", "createOnly: "+form, 1)
		if _, err := ParsePage("data", []byte(body)); err == nil {
			t.Errorf("createOnly: %s: expected an error", form)
		}
	}
}

func TestParsePageBodySections(t *testing.T) {
	p, err := ParsePage("data", []byte(samplePage))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if !reflect.DeepEqual(p.Usage, []string{"qctl get data <table-id-or-name>"}) {
		t.Errorf("usage = %v", p.Usage)
	}
	want := []Example{
		{Description: "first page of rows, default 20 per page", Command: "qctl get data orders"},
		{Description: "page 3, 100 rows, as JSON for scripting", Command: "qctl get data orders --page 3 --limit 100 -o json"},
	}
	if !reflect.DeepEqual(p.Examples, want) {
		t.Errorf("examples =\n%v\nwant\n%v", p.Examples, want)
	}
	wantGotchas := []Gotcha{
		{Text: "one backend page per call (default `--page 1 --limit 20`); no auto-pagination"},
		{Text: "flag values are validated by the backend, not by qctl", Backend: true},
	}
	if !reflect.DeepEqual(p.Gotchas, wantGotchas) {
		t.Errorf("gotchas =\n%v\nwant\n%v", p.Gotchas, wantGotchas)
	}
	if len(p.SeeAlso) != 2 || p.SeeAlso[0] != "`qctl explain table-schema`" {
		t.Errorf("see also = %v", p.SeeAlso)
	}
}

// TestParsePageStripsTheBackendMarker: [backend] is an internal maintenance
// tag for tools/gotchassweep. It must never reach a user, so it is parsed into
// Gotcha.Backend rather than left in the prose.
func TestParsePageStripsTheBackendMarker(t *testing.T) {
	for name, tc := range map[string]struct {
		bullet string
		want   Gotcha
	}{
		"tagged":           {"the server decides [backend]", Gotcha{Text: "the server decides", Backend: true}},
		"untagged":         {"qctl decides", Gotcha{Text: "qctl decides"}},
		"trailing space":   {"the server decides [backend]  ", Gotcha{Text: "the server decides", Backend: true}},
		"mid-line mention": {"the [backend] literal is not a marker here", Gotcha{Text: "the [backend] literal is not a marker here"}},
		"marker only":      {"[backend]", Gotcha{Text: "", Backend: true}},
	} {
		body := "---\nsummary: x\n---\n\n## GOTCHAS\n\n- " + tc.bullet + "\n"
		p, err := ParsePage("x", []byte(body))
		if err != nil {
			t.Fatalf("%s: ParsePage: %v", name, err)
		}
		if len(p.Gotchas) != 1 || p.Gotchas[0] != tc.want {
			t.Errorf("%s: gotchas = %v, want [%v]", name, p.Gotchas, tc.want)
		}
	}
}

func TestParsePageRejectsMalformedFrontmatter(t *testing.T) {
	for name, body := range map[string]string{
		"no opener":    "summary: x\n---\n\n## USAGE\n",
		"unterminated": "---\nsummary: x\n\n## USAGE\n",
		"not yaml":     "---\nsummary: [unclosed\n---\n\n## USAGE\n",
	} {
		if _, err := ParsePage("x", []byte(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSchemaRefSplitsTheNamespace(t *testing.T) {
	cases := []struct {
		in      string
		ns, ref string
		ok      bool
	}{
		{"openapi:DataSetSchemaFull", "openapi", "DataSetSchemaFull", true},
		{"manifest:table-rule", "manifest", "table-rule", true},
		{"", "", "", false},
		{"DataSetSchemaFull", "", "", false}, // bare names are rejected
		{"weird:thing", "", "", false},
	}
	for _, tc := range cases {
		ns, ref, ok := Page{Schema: tc.in}.SchemaRef()
		if ns != tc.ns || ref != tc.ref || ok != tc.ok {
			t.Errorf("SchemaRef(%q) = %q,%q,%v want %q,%q,%v", tc.in, ns, ref, ok, tc.ns, tc.ref, tc.ok)
		}
	}
}

func TestPagesLoadsEmbeddedPages(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no embedded pages")
	}
	p, ok := LookupPage("dataset") // alias of table
	if !ok || p.Name != "table" {
		t.Errorf("LookupPage(dataset) = %+v, %v; want the table page", p, ok)
	}
	if _, ok := LookupPage("nope"); ok {
		t.Error("LookupPage(nope) should not resolve")
	}
}
