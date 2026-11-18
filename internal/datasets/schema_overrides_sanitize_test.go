package datasets

import (
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Column names are the server's, and both builders below reach a terminal
// through apply and `qctl diff -f`.

// esc is built rather than written as a literal escape: a source file that
// spells the escape out is liable to be saved carrying a RAW control byte
// instead, which turns a JSON fixture into a parse panic and an assertion into
// a vacuous pass. Everything below splices this in.
const esc = "\x1b[31m"

// All three arms of the change list interpolate the field name, so a fix
// applied to only the one a smaller test happened to exercise would still pass.
func TestFormatSchemaFieldChangesSanitizesColumnNames(t *testing.T) {
	changes := []SchemaFieldChange{
		{Field: "email" + esc, Props: []SchemaPropChange{{Property: "type", Old: "String", New: "Integer"}}},
		{Field: "phone" + esc, Added: true},
		{Field: "fax" + esc, Cleared: true},
	}

	out := FormatSchemaFieldChanges(changes, "  ")
	if strings.Contains(out, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", out)
	}
	if got := strings.Count(out, `\x1b[31m`); got != 3 {
		t.Errorf("want a visible escape in all three arms (edit, add, clear), got %d in %q", got, out)
	}
}

// The deleted-column refusal names a column the server returned, and the
// message has to be safe by itself wherever it is printed. Both arms
// interpolate the name -- the edit and the `null` reset.
func TestDeletedColumnRefusalSanitizesColumnNames(t *testing.T) {
	// json.Marshal produces the properly escaped JSON key, so the fixture holds
	// no raw byte and no hand-written escape sequence either.
	key, err := json.Marshal("legacy" + esc)
	if err != nil {
		t.Fatalf("encoding the column name: %v", err)
	}
	fixture := strings.Replace(`{
  "confirmed": false,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"total_fields": 1},
  "fields": {
    "COLUMN": {"action": "removed",
      "current": {"type": "String", "args": [10], "is_nullable": true, "is_deleted": true}}
  },
  "migration_id": 91,
  "migration_model_version_id": 12,
  "settings_model_version_id": 7,
  "migration_state": "draft_with_current",
  "state": "draft"
}`, `"COLUMN"`, string(key), 1)

	for _, tc := range []struct {
		name  string
		block *FieldOverride
	}{
		{"an edit", &FieldOverride{Type: "Integer"}},
		{"a null reset", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := TableSchemaSpec{Fields: map[string]*FieldOverride{"legacy" + esc: tc.block}}
			_, _, err := BuildSchemaOverrides(mustSchemaDetail(fixture), spec, "f.yaml")
			// Guard the fixture itself: if the name stopped matching a deleted
			// column there would be no refusal, and the assertions below would
			// pass vacuously on a nil error's empty text.
			if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
				t.Fatalf("GetExitCode = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			msg := err.Error()
			if !strings.Contains(msg, "on a deleted column") {
				t.Fatalf("not the deleted-column refusal: %q", msg)
			}
			if strings.Contains(msg, "\x1b") {
				t.Errorf("a raw ESC reached the terminal: %q", msg)
			}
			if !strings.Contains(msg, `\x1b[31m`) {
				t.Errorf("the control must stay visible, got: %q", msg)
			}
		})
	}
}
