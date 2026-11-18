package history

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func modelIDInt(t *testing.T, n int) api.HistoryTinySchema_ModelID {
	t.Helper()
	var m api.HistoryTinySchema_ModelID
	if err := m.FromHistoryTinySchemaModelID0(n); err != nil {
		t.Fatalf("FromHistoryTinySchemaModelID0: %v", err)
	}
	return m
}

func modelIDUUID(t *testing.T, s string) api.HistoryTinySchema_ModelID {
	t.Helper()
	var m api.HistoryTinySchema_ModelID
	if err := m.FromHistoryTinySchemaModelID1(uuid.MustParse(s)); err != nil {
		t.Fatalf("FromHistoryTinySchemaModelID1: %v", err)
	}
	return m
}

func baseItem(t *testing.T) Item {
	t.Helper()
	return Item{
		ID:        8821,
		CreatedAt: time.Date(2026, 8, 30, 14, 5, 0, 0, time.UTC),
		Model:     api.ModelsWithHistoryDatasetRule,
		ModelID:   modelIDInt(t, 42),
		Operation: api.SimpleHistoryActionChanged,
		Path:      []string{"params", "max"},
		VersionID: intPtr(3),
	}
}

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }
func boolPtr(b bool) *bool    { return &b }

// The undo tri-state is the column an auditor reads first. NULL is a real
// state -- an undo that half-applied and needs retrying -- and it must not be
// rendered as "active", which would say the change is still in force.
func TestStatusRendersTheTriState(t *testing.T) {
	cases := []struct {
		name      string
		isDeleted *bool
		want      string
	}{
		{"false is applied", boolPtr(false), "active"},
		{"true is fully undone", boolPtr(true), "undone"},
		{"null is half-undone", nil, "partial"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := baseItem(t)
			item.IsDeleted = tc.isDeleted
			if got := ToRows([]Item{item})[0].Status; got != tc.want {
				t.Errorf("Status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChangeRendersIterableAdditionsAndRemovals(t *testing.T) {
	unknown := api.HistoryValueTypesUnknown
	strType := api.HistoryValueTypesStr
	cases := []struct {
		name           string
		operation      api.SimpleHistoryAction
		oldVal, newVal *string
		oldType        *api.HistoryValueTypes
		wantChange     string
		wantOldValue   string
	}{
		{"changed", api.SimpleHistoryActionChanged, strPtr("5"), strPtr("9"), &strType, "5 → 9", "5"},
		{"added", api.SimpleHistoryActionAdded, strPtr("unknown___"), strPtr("9"), &unknown, " → 9", ""},
		{"removed", api.SimpleHistoryActionRemoved, strPtr("unknown___"), strPtr("5"), &unknown, "5 → ", ""},
		{"older server", api.SimpleHistoryActionRemoved, strPtr("unknown___"), strPtr("5"), nil, "5 → ", ""},
		{"literal value", api.SimpleHistoryActionChanged, strPtr("unknown___"), strPtr("9"), &strType, "unknown___ → 9", "unknown___"},
		{"neither", api.SimpleHistoryActionChanged, nil, nil, nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := baseItem(t)
			item.Operation = tc.operation
			item.OldValue, item.Value = tc.oldVal, tc.newVal
			item.OldValueType = tc.oldType
			row := ToRows([]Item{item})[0]
			if got := row.Change; got != tc.wantChange {
				t.Errorf("Change = %q, want %q", got, tc.wantChange)
			}
			if row.OldValue != tc.wantOldValue {
				t.Errorf("OldValue = %q, want %q", row.OldValue, tc.wantOldValue)
			}
		})
	}
}

func TestFieldJoinsThePathAndShowsAMove(t *testing.T) {
	item := baseItem(t)
	if got := ToRows([]Item{item})[0].Field; got != "params.max" {
		t.Errorf("Field = %q, want %q", got, "params.max")
	}

	moved := baseItem(t)
	moved.Operation = api.SimpleHistoryActionMoved
	newPath := []string{"params", "maximum"}
	moved.NewPath = &newPath
	if got := ToRows([]Item{moved})[0].Field; got != "params.max → params.maximum" {
		t.Errorf("Field = %q, want the move rendered", got)
	}
}

// model_id is an int|UUID union on the way back (api.HistoryTinySchema_ModelID),
// even though the model_id FILTER is an int. Rendering it through the union is
// what stops a UUID-keyed model printing as raw JSON with quotes.
func TestModelIDRendersBothUnionArms(t *testing.T) {
	asInt := baseItem(t)
	if got := ToRows([]Item{asInt})[0].ModelID; got != "42" {
		t.Errorf("ModelID = %q, want %q", got, "42")
	}

	asUUID := baseItem(t)
	asUUID.ModelID = modelIDUUID(t, "11111111-2222-3333-4444-555555555555")
	if got := ToRows([]Item{asUUID})[0].ModelID; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("ModelID = %q, want the bare UUID", got)
	}
}

// The printed timestamp must be pasteable straight back into --since, or the
// column cannot be used to narrow the listing it came from.
func TestCreatedAtIsAnRFC3339UTCTimestamp(t *testing.T) {
	item := baseItem(t)
	got := ToRows([]Item{item})[0].CreatedAt
	if got != "2026-08-30T14:05:00Z" {
		t.Fatalf("CreatedAt = %q, want %q", got, "2026-08-30T14:05:00Z")
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("CreatedAt does not parse as RFC 3339: %v", err)
	}
}

func TestUserPrefersANameThenAnEmail(t *testing.T) {
	named := baseItem(t)
	named.User = &UserInfo{FirstName: "Dana", LastName: "Reed", Email: "dana@example.com"}
	if got := ToRows([]Item{named})[0].User; got != "Dana Reed" {
		t.Errorf("User = %q, want %q", got, "Dana Reed")
	}

	anon := baseItem(t)
	anon.User = &UserInfo{Email: "dana@example.com"}
	if got := ToRows([]Item{anon})[0].User; got != "dana@example.com" {
		t.Errorf("User = %q, want the email", got)
	}

	none := baseItem(t)
	if got := ToRows([]Item{none})[0].User; got != "-" {
		t.Errorf("User = %q, want %q for an unattributed change", got, "-")
	}
}

// -o name goes through this struct and prints the first of name/email/id it
// finds (internal/output extractName). A field tagged "name" or "email" would
// silently turn `-o name` into a list of table names or addresses.
func TestRowHasNoNameOrEmailField(t *testing.T) {
	raw, err := json.Marshal(Row{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]interface{}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, forbidden := range []string{"name", "email"} {
		if _, ok := keys[forbidden]; ok {
			t.Errorf("Row has a %q field; -o name would stop printing ids", forbidden)
		}
	}
	if _, ok := keys["id"]; !ok {
		t.Error("Row has no id field; -o name would print nothing")
	}
}

// version_id is nullable on the wire. A null renders as an empty cell, the way
// dataset_id does, never as 0 -- which would name a version that does not exist.
func TestVersionIDRendersANullAsEmpty(t *testing.T) {
	var item Item
	wire := `{"id": 8821, "created_at": "2026-08-30T14:05:00Z", "model": "DatasetRule",
		"model_id": 42, "operation": "changed", "path": ["params", "max"], "version_id": null}`
	if err := json.Unmarshal([]byte(wire), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := ToRows([]Item{item})[0].VersionID; got != "" {
		t.Errorf("VersionID = %q, want empty for a null version_id", got)
	}

	if got := ToRows([]Item{baseItem(t)})[0].VersionID; got != "3" {
		t.Errorf("VersionID = %q, want %q", got, "3")
	}
}

// The unit-tag columns carry the wire tag verbatim, except for the
// `unknown___` placeholder: the OLD-VALUE and CHANGE columns hide it, so the
// OLD-VALUE-TYPE column must hide it too or the pair disagrees about whether
// the item recorded an old value at all.
func TestUnitTagColumnsCarryTheWireTagAndFoldTheSentinel(t *testing.T) {
	decimalTag, strTag := api.HistoryValueTypesDecimal, api.HistoryValueTypesStr
	unknownTag := api.HistoryValueTypesUnknown
	cases := []struct {
		name                  string
		oldType, newType      *api.HistoryValueTypes
		wantOldType, wantType string
		wantOldValue          string
	}{
		{"a converted cell edit", &decimalTag, &decimalTag, "decimal", "decimal", "150"},
		{"source text overwritten with a number", &strTag, &decimalTag, "str", "decimal", "150"},
		{"an untagged item", nil, nil, "", "", "150"},
		{"a list addition", &unknownTag, &strTag, "", "str", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := baseItem(t)
			item.OldValue, item.Value = strPtr("150"), strPtr("0.13")
			item.OldValueType, item.ValueType = tc.oldType, tc.newType
			row := ToRows([]Item{item})[0]
			if row.OldValueType != tc.wantOldType {
				t.Errorf("OldValueType = %q, want %q", row.OldValueType, tc.wantOldType)
			}
			if row.ValueType != tc.wantType {
				t.Errorf("ValueType = %q, want %q", row.ValueType, tc.wantType)
			}
			if row.OldValue != tc.wantOldValue {
				t.Errorf("OldValue = %q, want %q -- the tag column must agree with it",
					row.OldValue, tc.wantOldValue)
			}
		})
	}
}

// delegation_id and change_proposal_id are nullable UUIDs. A value renders as
// the full UUID, a null as an empty cell.
func TestAttributionIDsRenderTheUUIDOrEmpty(t *testing.T) {
	delegation := uuid.MustParse("6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e")
	proposal := uuid.MustParse("0a1b2c3d-4e5f-4a6b-9c7d-8e9f0a1b2c3d")

	item := baseItem(t)
	item.DelegationID, item.ChangeProposalID = &delegation, &proposal
	row := ToRows([]Item{item})[0]
	if row.DelegationID != delegation.String() {
		t.Errorf("DelegationID = %q, want %q", row.DelegationID, delegation.String())
	}
	if row.ChangeProposalID != proposal.String() {
		t.Errorf("ChangeProposalID = %q, want %q", row.ChangeProposalID, proposal.String())
	}

	var unset Item
	wire := `{"id": 8821, "created_at": "2026-08-30T14:05:00Z", "model": "DatasetRule",
		"model_id": 42, "operation": "changed", "path": ["params", "max"],
		"delegation_id": null, "change_proposal_id": null}`
	if err := json.Unmarshal([]byte(wire), &unset); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	row = ToRows([]Item{unset})[0]
	if row.DelegationID != "" || row.ChangeProposalID != "" {
		t.Errorf("DelegationID = %q, ChangeProposalID = %q, want both empty for nulls",
			row.DelegationID, row.ChangeProposalID)
	}
}
