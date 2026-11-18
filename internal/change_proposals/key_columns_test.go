package change_proposals //nolint:revive // Package name mirrors the hyphenated command.

import "testing"

func TestKeyColumnsLineRendersTheBoundKeyChoice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bound interface{}
		want  string
	}{
		{"set", map[string]interface{}{"current": nil, "new": []interface{}{"a", "b"}, "no_key_columns": false},
			"Key columns: (none) -> a, b (applying locks them)\n"},
		{"keyless", map[string]interface{}{"current": nil, "new": nil, "no_key_columns": true},
			"Key columns: (none) -> none (the table is recorded as keyless) (applying locks them)\n"},
		{"kept", map[string]interface{}{"current": []interface{}{"id"}, "new": nil, "no_key_columns": false},
			"Key columns: id -> kept (applying locks them)\n"},
		{"absent", nil, ""},
	} {
		if got := keyColumnsLine(tc.bound); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
