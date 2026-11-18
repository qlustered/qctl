package rule_versions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// affected_column_types is a map from rule field name to EITHER a list of
// accepted Atlas field-type names OR the literal string "any". oapi-codegen
// renders that anyOf as an opaque union struct per schema
// (RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties), whose
// only public shape-independent accessor is MarshalJSON. Decoding through
// encoding/json rather than through the generated As...Types0/As...Types1
// accessors keeps one code path for both shapes, and passes anything the
// server sends that fits neither through untouched instead of dropping it.
//
// The map does NOT necessarily have an entry per affected column. The backend
// property merges RuleRevision.column_field_types with the declared type of
// each enriched column that the read side does not already type
// (RuleRevision.affected_column_types, cettings/cettings/models/rule_model.py
// at atlas dd94349), so a field the rule author declared no type for is simply
// absent -- and a revision stored before types were declared has an empty map.
// Absence is therefore "the revision said nothing", never "this field accepts
// nothing", which is why every renderer here omits the field rather than
// printing a placeholder.

// AffectedColumnTypes decodes a revision's affected_column_types into the two
// shapes the wire uses -- a []interface{} of type names, or the string "any" --
// keyed by rule field name. It returns nil when the revision declares none, so
// an omitempty manifest field disappears rather than serialising as an empty
// map the server never sent.
func AffectedColumnTypes(resp *RuleRevisionFull) map[string]interface{} {
	if resp == nil || resp.AffectedColumnTypes == nil {
		return nil
	}
	out := make(map[string]interface{}, len(*resp.AffectedColumnTypes))
	for field, entry := range *resp.AffectedColumnTypes {
		raw, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		var decoded interface{}
		if err := jsonnum.Unmarshal(raw, &decoded); err != nil || decoded == nil {
			continue
		}
		out[field] = decoded
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AcceptedColumnTypeText renders a revision's affected_column_types as one
// display string per rule field: the accepted field-type names joined with
// ", ", or the wildcard the wire spells "any".
//
// A field with an empty list is left out, matching the frontend's
// hasExpectedColumnType (cosmosview 84274223,
// src/common/rule/ruleColumnTypes.ts) -- there the same value renders nothing
// rather than an empty label, and a CLI that printed "accepts: " for it would
// be making a claim the revision did not.
func AcceptedColumnTypeText(resp *RuleRevisionFull) map[string]string {
	decoded := AffectedColumnTypes(resp)
	if len(decoded) == 0 {
		return nil
	}
	out := make(map[string]string, len(decoded))
	for field, value := range decoded {
		if text := acceptedTypeText(value); text != "" {
			out[field] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// acceptedTypeText renders one decoded entry. An empty string means "nothing
// to show" -- an empty list, or a shape neither branch of the anyOf covers.
func acceptedTypeText(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case []interface{}:
		names := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				names = append(names, s)
				continue
			}
			names = append(names, fmt.Sprintf("%v", item))
		}
		return strings.Join(names, ", ")
	default:
		return ""
	}
}
