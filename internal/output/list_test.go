package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewListWritesEveryKey(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(Options{Format: FormatJSON, Writer: &buf})
	if err := p.Print(NewList([]TestUser(nil), nil, nil, nil, nil)); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "items", "page", "limit", "total", "next_page"} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing from %s", key, buf.String())
		}
	}
	if items, ok := got["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %#v, want []", got["items"])
	}
}

// The items inside a List are masked like any other printed value.
func TestListItemsAreMasked(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML} {
		var buf bytes.Buffer
		p := NewPrinter(Options{Format: format, Writer: &buf})
		page := 1
		if err := p.Print(NewList([]TestUser{{ID: "1", Password: "s3cr3t"}}, &page, &page, &page, nil)); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "s3cr3t") {
			t.Errorf("-o %s leaked the password:\n%s", format, buf.String())
		}
	}
}
