package jsonschemautil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidateParams validates params against a JSON Schema (param_schema from the rule revision).
// If schema is nil or empty, validation is skipped (permissive).
func ValidateParams(params map[string]interface{}, schema map[string]interface{}) error {
	if len(schema) == 0 {
		return nil
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("failed to marshal param_schema: %w", err)
	}

	var schemaObj interface{}
	if err := json.Unmarshal(schemaJSON, &schemaObj); err != nil {
		return fmt.Errorf("failed to unmarshal param_schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaObj); err != nil {
		return fmt.Errorf("failed to compile param_schema: %w", err)
	}

	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("failed to compile param_schema: %w", err)
	}

	// Convert params to interface{} for validation
	var paramsAny interface{}
	if params == nil {
		paramsAny = map[string]interface{}{}
	} else {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		if err := json.Unmarshal(paramsJSON, &paramsAny); err != nil {
			return fmt.Errorf("failed to unmarshal params: %w", err)
		}
	}

	err = compiled.Validate(paramsAny)
	if err == nil {
		return nil
	}

	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return fmt.Errorf("params validation failed: %w", err)
	}

	var msgs []string
	collectErrors(validationErr, &msgs)
	return fmt.Errorf("params validation failed:\n  %s", strings.Join(msgs, "\n  "))
}

func collectErrors(err *jsonschema.ValidationError, msgs *[]string) {
	if len(err.Causes) == 0 {
		loc := "/" + strings.Join(err.InstanceLocation, "/")
		if loc == "/" {
			loc = "(root)"
		}
		*msgs = append(*msgs, fmt.Sprintf("%s: %s", loc, err.ErrorKind))
		return
	}
	for _, cause := range err.Causes {
		collectErrors(cause, msgs)
	}
}
