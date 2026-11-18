package history

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
)

// Row is the flat display projection of one history item.
//
// Table and -o name only: `get history` encodes the generated
// api.HistoryListSchema for -o json and -o yaml, so nothing machine-readable
// is ever shaped by this struct.
type Row struct {
	ID          int    `json:"id"`
	CreatedAt   string `json:"created_at"`
	User        string `json:"user"`
	DatasetName string `json:"dataset_name"`
	DatasetID   string `json:"dataset_id"`
	Model       string `json:"model"`
	ModelID     string `json:"model_id"`
	VersionID   string `json:"version_id"`
	Operation   string `json:"operation"`
	Field       string `json:"field"`
	Change      string `json:"change"`
	Value       string `json:"value"`
	// ValueType and OldValueType are the item's unit tags. `decimal` marks a
	// number the server recorded as one; no other tag carries a unit claim.
	ValueType        string `json:"value_type"`
	OldValue         string `json:"old_value"`
	OldValueType     string `json:"old_value_type"`
	Status           string `json:"status"`
	DeletedAt        string `json:"deleted_at"`
	DeletedBy        string `json:"deleted_by"`
	DelegationID     string `json:"delegation_id"`
	ChangeProposalID string `json:"change_proposal_id"`
}

// ToRows projects wire items into display rows.
func ToRows(items []Item) []Row {
	rows := make([]Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, Row{
			ID:           item.ID,
			CreatedAt:    formatTime(&item.CreatedAt),
			User:         userLabel(item.User),
			DatasetName:  derefString(item.DatasetName),
			DatasetID:    intLabel(item.DatasetID),
			Model:        string(item.Model),
			ModelID:      modelIDLabel(item.ModelID),
			VersionID:    intLabel(item.VersionID),
			Operation:    string(item.Operation),
			Field:        fieldLabel(item),
			Change:       changeLabel(item),
			Value:        derefString(item.Value),
			ValueType:    valueTypeLabel(item.ValueType),
			OldValue:     displayOldValue(item),
			OldValueType: displayOldValueType(item),
			Status:       statusLabel(item.IsDeleted),
			DeletedAt:    formatTime(item.DeletedAt),
			DeletedBy:    userLabel(item.DeletedByUser),

			DelegationID:     uuidLabel(item.DelegationID),
			ChangeProposalID: uuidLabel(item.ChangeProposalID),
		})
	}
	return rows
}

// statusLabel renders HistoryItem.is_deleted, which is a TRI-state and not a
// boolean: false is applied, true is fully undone and redo-able, and NULL means
// a bulk undo or redo half-applied and needs retrying. Rendering NULL as
// "active" would claim the change is still in force.
//
// An absent field and an explicit null are indistinguishable here (the Go type
// is *bool and the view declares response_model_exclude_unset=True). They mean
// the same thing in practice: FastAPI populates the field when it validates the
// ORM row, so it is only omitted when the column really is NULL.
func statusLabel(isDeleted *bool) string {
	switch {
	case isDeleted == nil:
		return "partial"
	case *isDeleted:
		return "undone"
	default:
		return "active"
	}
}

// fieldLabel joins the path, and renders a `moved` operation as old → new.
//
// The joined form is for READING. It is not what --field takes: the backend
// matches one path element (HistoryItem.path.any(field_name)), so the filter
// for "params.max" is `--field max`.
func fieldLabel(item Item) string {
	from := strings.Join(item.Path, ".")
	if item.NewPath == nil {
		return from
	}
	return from + " → " + strings.Join(*item.NewPath, ".")
}

// changeLabel renders old → new, with removed values on the left.
func changeLabel(item Item) string {
	oldValue, newValue := displayOldValue(item), derefString(item.Value)
	if item.Operation == api.SimpleHistoryActionRemoved && oldValue == "" {
		oldValue, newValue = newValue, ""
	}
	if oldValue == "" && newValue == "" {
		return ""
	}
	return oldValue + " → " + newValue
}

// valueTypeLabel renders a unit tag for its optional column, blank when the
// item carries none. Both tag fields are omitempty on the wire, and an
// "initiated" item records no old type at all.
func valueTypeLabel(tag *api.HistoryValueTypes) string {
	if tag == nil {
		return ""
	}
	return string(*tag)
}

// displayOldValueType renders old_value_type, hiding the `unknown___`
// placeholder that displayOldValue and changeLabel already hide. Left in, the
// OLD-VALUE-TYPE column would tag a cell the OLD-VALUE column deliberately
// leaves blank.
func displayOldValueType(item Item) string {
	if item.OldValueType == nil || *item.OldValueType == api.HistoryValueTypesUnknown {
		return ""
	}
	return string(*item.OldValueType)
}

func displayOldValue(item Item) string {
	oldValue := derefString(item.OldValue)
	if item.OldValueType != nil {
		if *item.OldValueType == api.HistoryValueTypesUnknown {
			return ""
		}
	} else if oldValue == string(api.HistoryValueTypesUnknown) {
		return ""
	}
	return oldValue
}

// modelIDLabel renders the model_id union. The response types it as int|UUID
// (api.HistoryTinySchema_ModelID) even though the model_id query parameter is
// an int, so this cannot assume a number.
func modelIDLabel(modelID api.HistoryTinySchema_ModelID) string {
	raw, err := modelID.MarshalJSON()
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return string(raw)
}

func userLabel(user *UserInfo) string {
	if user == nil {
		return "-"
	}
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name
	}
	if user.Email != "" {
		return user.Email
	}
	return "-"
}

// formatTime renders RFC 3339 in UTC, so the printed value is itself a valid
// --since argument and correlates with an incident timestamp. A relative age
// ("3 days ago") cannot do either, which is why this ledger does not use one.
func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func uuidLabel(id *openapi_types.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func intLabel(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}
