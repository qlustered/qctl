package secretmask

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
)

// jsonTree returns v encoded by encoding/json and decoded back, so that two
// values compare equal exactly when encoding/json writes the same document.
func jsonTree(t *testing.T, v interface{}) interface{} {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding %T: %v", v, err)
	}
	var tree interface{}
	if err := json.Unmarshal(encoded, &tree); err != nil {
		t.Fatalf("decoding %s: %v", encoded, err)
	}
	return tree
}

func mustUnion(t *testing.T, wire string) api.DryRunJobFullSchema_StructuredSummary {
	t.Helper()
	var union api.DryRunJobFullSchema_StructuredSummary
	if err := union.UnmarshalJSON([]byte(wire)); err != nil {
		t.Fatalf("UnmarshalJSON(%s): %v", wire, err)
	}
	return union
}

// Plain and Mask must write what encoding/json writes for the same value:
// UUIDs as their text, a generated union as the object it holds, nulls for
// nil pointers, and omitempty honored.
func TestPlainAndMaskWriteWhatEncodingJSONWrites(t *testing.T) {
	id := uuid.MustParse("5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c")
	revision := uuid.MustParse("0e9d8c7b-6a5f-4e3d-2c1b-a09f8e7d6c5b")
	data := struct {
		ID         openapi_types.UUID                        `json:"id"`
		IDs        []openapi_types.UUID                      `json:"ids"`
		Revision   *openapi_types.UUID                       `json:"revision"`
		Missing    *openapi_types.UUID                       `json:"missing"`
		Skipped    *openapi_types.UUID                       `json:"skipped,omitempty"`
		Zero       int                                       `json:"zero,omitempty"`
		Summary    api.DryRunJobFullSchema_StructuredSummary `json:"summary"`
		EmptyUnion api.DryRunJobFullSchema_StructuredSummary `json:"empty_union"`
		Raw        json.RawMessage                           `json:"raw"`
		Day        openapi_types.Date                        `json:"day"`
		Params     map[string]interface{}                    `json:"params"`
		Specs      []api.RuleRunSpec                         `json:"specs"`
	}{
		ID:       id,
		IDs:      []openapi_types.UUID{id, revision},
		Revision: &revision,
		Summary:  mustUnion(t, `{"total_eligible": 1000, "clean": {"rate": 1.0, "count": 1000}, "top_blocker_reasons": []}`),
		Raw:      json.RawMessage(`{"a": [1, "b", null]}`),
		Day:      openapi_types.Date{Time: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)},
		Params:   map[string]interface{}{"threshold": 3, "owner": id},
		Specs:    []api.RuleRunSpec{{Position: 1, DatasetRuleID: &id, RuleRevisionID: &revision}},
	}

	want := jsonTree(t, data)
	sm := New()
	for name, got := range map[string]interface{}{"Plain": sm.Plain(data), "Mask": sm.Mask(data)} {
		if got := jsonTree(t, got); !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			t.Errorf("%s(data) differs from encoding/json\n got: %s\nwant: %s", name, gotJSON, wantJSON)
		}
	}
}

func TestMaskReachesSecretsInsideMarshaledAndInterfaceValues(t *testing.T) {
	data := map[string]interface{}{
		"summary": mustUnion(t, `{"connection": {"host": "db", "password": "union-secret"}}`),
		"spec": map[string]interface{}{
			"password": "spec-secret",
			"db":       map[string]interface{}{"token": "nested-secret"},
		},
		"items":  []interface{}{map[string]interface{}{"api_key": "item-secret"}},
		"counts": map[string]int{"token": 7, "rows": 3},
	}

	masked := jsonTree(t, New().Mask(data))
	encoded, _ := json.Marshal(masked)
	for _, secret := range []string{"union-secret", "spec-secret", "nested-secret", "item-secret", `"token":7`} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("Mask left %s in plaintext: %s", secret, encoded)
		}
	}

	want := jsonTree(t, map[string]interface{}{
		"summary": map[string]interface{}{"connection": map[string]interface{}{"host": "db", "password": "***"}},
		"spec": map[string]interface{}{
			"password": "***",
			"db":       map[string]interface{}{"token": "***"},
		},
		"items":  []interface{}{map[string]interface{}{"api_key": "***"}},
		"counts": map[string]interface{}{"token": "***", "rows": 3},
	})
	if !reflect.DeepEqual(masked, want) {
		wantJSON, _ := json.Marshal(want)
		t.Errorf("Mask(data)\n got: %s\nwant: %s", encoded, wantJSON)
	}

	if plain, _ := json.Marshal(New().Plain(data)); !strings.Contains(string(plain), "union-secret") {
		t.Errorf("Plain masked a secret: %s", plain)
	}
}

// time.Time's own MarshalJSON writes RFC3339Nano; the printers write RFC3339.
func TestTimesAreRFC3339(t *testing.T) {
	at := time.Date(2026, 9, 18, 12, 30, 0, 123456789, time.UTC)
	data := struct {
		At      time.Time     `json:"at"`
		AtPtr   *time.Time    `json:"at_ptr"`
		InSlice []interface{} `json:"in_slice"`
	}{At: at, AtPtr: &at, InSlice: []interface{}{at}}

	want := jsonTree(t, map[string]interface{}{
		"at":       "2026-09-18T12:30:00Z",
		"at_ptr":   "2026-09-18T12:30:00Z",
		"in_slice": []interface{}{"2026-09-18T12:30:00Z"},
	})
	sm := New()
	for name, got := range map[string]interface{}{"Plain": sm.Plain(data), "Mask": sm.Mask(data)} {
		if got := jsonTree(t, got); !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			t.Errorf("%s(data) = %s, want RFC3339 timestamps", name, gotJSON)
		}
	}
}

func TestMaskOfNilIsNil(t *testing.T) {
	if got := New().Mask(nil); got != nil {
		t.Errorf("Mask(nil) = %#v, want nil", got)
	}
}

func TestAdditionalSecretFieldsAreLocalToTheMask(t *testing.T) {
	data := struct {
		URL      string              `json:"simple_url"`
		Password string              `json:"password"`
		Items    []map[string]string `json:"items"`
	}{
		URL:      "https://files.example.com/direct-secret",
		Password: "password-secret",
		Items:    []map[string]string{{"SIMPLE_URL": "https://files.example.com/nested-secret", "name": "source"}},
	}
	defaultMask := New()
	loggingMask := New("SIMPLE_URL")
	want := jsonTree(t, map[string]any{
		"simple_url": "***",
		"password":   "***",
		"items":      []map[string]string{{"SIMPLE_URL": "***", "name": "source"}},
	})
	if got := jsonTree(t, loggingMask.Mask(data)); !reflect.DeepEqual(got, want) {
		t.Errorf("extended Mask(data) = %#v, want %#v", got, want)
	}
	if got := jsonTree(t, loggingMask.Plain(data)); !reflect.DeepEqual(got, jsonTree(t, data)) {
		t.Errorf("extended Plain(data) changed values: %#v", got)
	}
	wantDefault := data
	wantDefault.Password = "***"
	for _, mask := range []*SecretMask{defaultMask, New()} {
		if mask.IsSecret("simple_url") {
			t.Error("additional field changed the default credential classification")
		}
		if got := jsonTree(t, mask.Mask(data)); !reflect.DeepEqual(got, jsonTree(t, wantDefault)) {
			t.Errorf("default Mask(data) changed: %#v", got)
		}
	}
}

// A number in a generated union comes out as an int64 when it fits one, and as
// the json.Number of its literal when it does not.
func TestPlainKeepsUnionIntegers(t *testing.T) {
	data := struct {
		Summary api.DryRunJobFullSchema_StructuredSummary `json:"summary"`
	}{mustUnion(t, `{"rows": 1234567, "huge": 18446744073709551616}`)}

	summary := New().Plain(data).(map[string]interface{})["summary"].(map[string]interface{})
	if got, ok := summary["rows"].(int64); !ok || got != 1234567 {
		t.Errorf("rows = %T(%v), want int64(1234567)", summary["rows"], summary["rows"])
	}
	if got, ok := summary["huge"].(json.Number); !ok || got != "18446744073709551616" {
		t.Errorf("huge = %T(%v), want json.Number(18446744073709551616)", summary["huge"], summary["huge"])
	}
}

// Every credential a cloud source carries is masked: a field of the source's
// wire schema whose name looks like a credential must be a secret field.
func TestEveryCloudSourceCredentialIsSecret(t *testing.T) {
	credential := []string{"password", "passphrase", "secret", "token", "_key"}
	// notCredential names the fields that look like a credential and are not:
	// a series intake's first and last period, such as "2026-10".
	notCredential := []string{"start_period_key", "end_period_key"}
	sm := New()
	typ := reflect.TypeOf(api.DataSourceModelAllOptionalSchema{})
	checked := 0
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if !slices.ContainsFunc(credential, func(s string) bool { return strings.Contains(name, s) }) {
			continue
		}
		if slices.Contains(notCredential, name) {
			if sm.IsSecret(name) {
				t.Errorf("%s is masked, but is no credential", name)
			}
			continue
		}
		checked++
		if !sm.IsSecret(name) {
			t.Errorf("%s is not masked", name)
		}
	}
	if checked == 0 {
		t.Fatal("no credential fields found on DataSourceModelAllOptionalSchema")
	}
}
