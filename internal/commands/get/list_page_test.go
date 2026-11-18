package get

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestListPageNextPage(t *testing.T) {
	tests := []struct {
		name string
		lp   listPage
		want *int
	}{
		{"total says a page follows, no next", listPage{page: intPtr(1), limit: intPtr(2), total: intPtr(250)}, intPtr(2)},
		{"total says this is the last page, next sent anyway", listPage{page: intPtr(2), limit: intPtr(2), total: intPtr(4), more: true}, nil},
		{"partial last page", listPage{page: intPtr(3), limit: intPtr(2), total: intPtr(5)}, nil},
		{"past the end", listPage{page: intPtr(9), limit: intPtr(2), total: intPtr(5)}, nil},
		{"huge page cannot overflow the last-page check", listPage{page: intPtr(math.MaxInt), limit: intPtr(2), total: intPtr(5)}, nil},
		{"unknown total cannot overflow next", listPage{page: intPtr(math.MaxInt), more: true}, nil},
		{"no total, next sent", listPage{page: intPtr(1), limit: intPtr(2), more: true}, intPtr(2)},
		{"no total, no next", listPage{page: intPtr(1), limit: intPtr(2)}, nil},
		{"unpaged list", listPage{total: intPtr(9), more: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.lp.nextPage()
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("nextPage() = %v, want %v", deref(got), deref(tt.want))
			}
		})
	}
}

func TestListPageHint(t *testing.T) {
	tests := []struct {
		name  string
		lp    listPage
		shown int
		want  string
	}{
		{"range from the total", listPage{noun: "rules", page: intPtr(2), limit: intPtr(10), total: intPtr(45)}, 10,
			"Showing 11-20 of 45 rules (page 2 of 5). More available: use --page 3 or add filters."},
		{"range counts the request, not the rows", listPage{noun: "rules", page: intPtr(1), limit: intPtr(10), total: intPtr(45)}, 23,
			"Showing 1-10 of 45 rules (page 1 of 5). More available: use --page 2 or add filters."},
		{"no total keeps the old wording", listPage{noun: "alerts", page: intPtr(1), limit: intPtr(5), more: true}, 5,
			"Showing 5 results. More available: use --page 2 or add filters."},
		{"no filters to add", listPage{noun: "tables", page: intPtr(1), limit: intPtr(1), total: intPtr(2), noFilters: true}, 1,
			"Showing 1-1 of 2 tables (page 1 of 2). More available: use --page 2."},
		{"last page", listPage{noun: "alerts", page: intPtr(5), limit: intPtr(10), total: intPtr(45)}, 5, ""},
		{"unpaged list cut short by --limit", listPage{noun: "cloud sources", limit: intPtr(150), total: intPtr(450)}, 150,
			"Showing 150 of 450 cloud sources. More available: raise --limit (0 = all) or add filters."},
		{"unpaged list, all shown", listPage{noun: "cloud sources", total: intPtr(450)}, 450, ""},
		{"unpaged list with no total", listPage{noun: "rule ignores", limit: intPtr(5)}, 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lp.hint(tt.shown); got != tt.want {
				t.Errorf("hint() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

// An empty page past the end is still a List under -o json, with its paging,
// and gets no hint on stderr.
func TestPrintListPageEmptyPageIsAList(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "json", "")
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	var got any
	lp := listPage{noun: "alerts", page: intPtr(999), limit: intPtr(2), total: intPtr(250)}
	err := printListPage(cmd, []struct{ ID int }(nil), lp, func(data any) error {
		raw, err := json.Marshal(data)
		if err == nil {
			err = json.Unmarshal(raw, &got)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"kind": "List", "items": []any{}, "page": 999.0, "limit": 2.0, "total": 250.0, "next_page": nil}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("rendered %s, want %s", gotJSON, wantJSON)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// decodeListItems decodes the items of a -o json List envelope into v,
// failing the test when raw is not a List.
func decodeListItems(t *testing.T, raw []byte, v any) {
	t.Helper()
	var list struct {
		Kind  string          `json:"kind"`
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("-o json did not decode as a List: %v\n%s", err, raw)
	}
	if list.Kind != "List" {
		t.Fatalf("kind = %q, want List:\n%s", list.Kind, raw)
	}
	if err := json.Unmarshal(list.Items, v); err != nil {
		t.Fatalf("items did not decode: %v\n%s", err, list.Items)
	}
}

// assertEnvelopeNextPage fails unless raw is a -o json List whose next_page is
// want and stderr is empty: under json the envelope carries the paging.
func assertEnvelopeNextPage(t *testing.T, raw []byte, stderr string, want int) {
	t.Helper()
	var list struct {
		Kind     string `json:"kind"`
		NextPage *int   `json:"next_page"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || list.Kind != "List" {
		t.Fatalf("-o json is not a List (err %v):\n%s", err, raw)
	}
	if list.NextPage == nil || *list.NextPage != want {
		t.Errorf("next_page = %v, want %d:\n%s", deref(list.NextPage), want, raw)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing under -o json", stderr)
	}
}

// unpagedListItems decodes -o json|yaml output, fails unless it is a List
// with all four paging keys null, and returns its items as encoding/json
// decodes them.
func unpagedListItems(t *testing.T, format, out string) []map[string]any {
	t.Helper()
	var list map[string]any
	var err error
	if format == "yaml" {
		err = yaml.Unmarshal([]byte(out), &list)
	} else {
		err = json.Unmarshal([]byte(out), &list)
	}
	if err != nil {
		t.Fatalf("-o %s is not an object: %v\n%s", format, err, out)
	}
	keys := make([]string, 0, len(list))
	for k := range list {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"items", "kind", "limit", "next_page", "page", "total"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v\n%s", keys, want, out)
	}
	if list["kind"] != "List" {
		t.Errorf("kind = %v, want List", list["kind"])
	}
	for _, k := range []string{"page", "limit", "total", "next_page"} {
		if list[k] != nil {
			t.Errorf("%s = %v, want null", k, list[k])
		}
	}
	raw, err := json.Marshal(list["items"])
	if err != nil {
		t.Fatalf("re-encode items: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		t.Fatalf("items = %s, want a list (%v)", raw, err)
	}
	return items
}
