package jsonschemautil

import (
	"strings"
	"testing"
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
	if !strings.Contains(err.Error(), "threshold") {
		t.Errorf("error should mention 'threshold', got: %v", err)
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
