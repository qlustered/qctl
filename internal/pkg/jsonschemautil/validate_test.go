package jsonschemautil

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func TestValidateParams_Valid(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{
				"type": "number",
			},
		},
		"required": []interface{}{"threshold"},
	}

	params := map[string]interface{}{
		"threshold": 0.9,
	}

	if err := ValidateParams(params, schema); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateParams_MissingRequired(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{
				"type": "number",
			},
		},
		"required": []interface{}{"threshold"},
	}

	params := map[string]interface{}{}

	err := ValidateParams(params, schema)
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	const want = "params validation failed:\n  (root): missing property 'threshold'"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if strings.Contains(err.Error(), "&{") {
		t.Errorf("error exposes a raw Go struct: %v", err)
	}
}

func TestValidateParams_WrongType(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{
				"type": "number",
			},
		},
	}

	params := map[string]interface{}{
		"threshold": "not_a_number",
	}

	err := ValidateParams(params, schema)
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error should mention validation, got: %v", err)
	}
}

func TestValidateParams_NilSchema(t *testing.T) {
	params := map[string]interface{}{
		"threshold": 0.9,
	}

	if err := ValidateParams(params, nil); err != nil {
		t.Errorf("nil schema should skip validation, got: %v", err)
	}
}

func TestValidateParams_EmptySchema(t *testing.T) {
	params := map[string]interface{}{
		"threshold": 0.9,
	}

	if err := ValidateParams(params, map[string]interface{}{}); err != nil {
		t.Errorf("empty schema should skip validation, got: %v", err)
	}
}

func TestValidateParams_NilParamsWithRequired(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{
				"type": "number",
			},
		},
		"required": []interface{}{"threshold"},
	}

	err := ValidateParams(nil, schema)
	if err == nil {
		t.Fatal("expected error for nil params when schema has required fields")
	}
	if !strings.Contains(err.Error(), "threshold") {
		t.Errorf("error should mention 'threshold', got: %v", err)
	}
}

func TestValidateParams_NonJSONYAMLValueIsBadUsage(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	var params map[string]interface{}
	if err := yaml.Unmarshal([]byte("threshold: .nan\n"), &params); err != nil {
		t.Fatalf("decode manifest params: %v", err)
	}

	err := ValidateParams(params, schema)
	if err == nil {
		t.Fatal("expected non-JSON manifest value to fail")
	}
	if !strings.Contains(err.Error(), "failed to marshal params") {
		t.Errorf("error should name params conversion, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// Numeric keywords judge integers, floats and integers past int64 by value.
// The two cases past 2^53 tell 9007199254740993 from 9007199254740992.
func TestValidateParams_NumericKeywords(t *testing.T) {
	property := func(p map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"n": p},
		}
	}
	for _, tc := range []struct {
		name    string
		schema  map[string]interface{}
		value   interface{}
		wantErr bool
	}{
		{"integer accepts an int", property(map[string]interface{}{"type": "integer"}), 1234567, false},
		{"integer accepts a whole float", property(map[string]interface{}{"type": "integer"}), 2.0, false},
		{"integer rejects a fraction", property(map[string]interface{}{"type": "integer"}), 1.5, true},
		{"integer accepts one past int64", property(map[string]interface{}{"type": "integer"}), uint64(18446744073709551615), false},
		{"number accepts an int", property(map[string]interface{}{"type": "number"}), 1234567, false},
		{"number accepts a float", property(map[string]interface{}{"type": "number"}), 0.9, false},
		{"number rejects a string", property(map[string]interface{}{"type": "number"}), "7", true},
		{"maximum accepts the bound", property(map[string]interface{}{"maximum": 10}), 10, false},
		{"maximum rejects an int above", property(map[string]interface{}{"maximum": 10}), 11, true},
		{"maximum rejects a float above", property(map[string]interface{}{"maximum": 10}), 10.5, true},
		{"float maximum accepts an int below", property(map[string]interface{}{"maximum": 2.5}), 2, false},
		{"maximum rejects one past int64", property(map[string]interface{}{"maximum": 100}), uint64(18446744073709551615), true},
		{"maximum rejects 2^53+1 above 2^53", property(map[string]interface{}{"maximum": 9007199254740992}), 9007199254740993, true},
		{"multipleOf accepts a multiple", property(map[string]interface{}{"multipleOf": 5}), 15, false},
		{"multipleOf rejects a non-multiple", property(map[string]interface{}{"multipleOf": 5}), 7, true},
		{"float multipleOf accepts a float multiple", property(map[string]interface{}{"multipleOf": 0.1}), 0.3, false},
		{"enum accepts an int member", property(map[string]interface{}{"enum": []interface{}{1, 2.5, 1234567}}), 1234567, false},
		{"enum accepts a float member", property(map[string]interface{}{"enum": []interface{}{1, 2.5, 1234567}}), 2.5, false},
		{"enum accepts an int as its whole float", property(map[string]interface{}{"enum": []interface{}{3.0}}), 3, false},
		{"enum rejects a non-member", property(map[string]interface{}{"enum": []interface{}{1, 2.5, 1234567}}), 3, true},
		{"enum rejects 2^53 against 2^53+1", property(map[string]interface{}{"enum": []interface{}{9007199254740993}}), 9007199254740992, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParams(map[string]interface{}{"n": tc.value}, tc.schema)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateParams(n=%v) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
			if err != nil && qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
			}
		})
	}
}

// An enum's members print as the integers they are in the error.
func TestValidateParams_EnumErrorPrintsIntegers(t *testing.T) {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"n": map[string]interface{}{"enum": []interface{}{1234567, 7654321}}},
	}
	err := ValidateParams(map[string]interface{}{"n": 3}, schema)
	if err == nil || !strings.Contains(err.Error(), "value must be one of 1234567, 7654321") {
		t.Fatalf("error = %v, want it to list 1234567, 7654321", err)
	}
}
