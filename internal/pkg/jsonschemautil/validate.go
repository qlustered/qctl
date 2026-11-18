package jsonschemautil

import (
	"encoding/json"
	"fmt"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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
	if err := jsonnum.Unmarshal(schemaJSON, &schemaObj); err != nil {
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
			// Params come from the user's manifest. YAML accepts values such as
			// .nan that JSON does not, so a conversion failure is malformed input.
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to marshal params")
		}
		if err := jsonnum.Unmarshal(paramsJSON, &paramsAny); err != nil {
			// Keep the whole manifest-to-JSON conversion path classified alike.
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to unmarshal params")
		}
	}

	err = compiled.Validate(paramsAny)
	if err == nil {
		return nil
	}

	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		// Defensive only: jsonschema/v6 Validate returns nil or *ValidationError.
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "params validation failed")
	}

	var msgs []string
	collectErrors(validationErr, &msgs)
	return qerrors.NewBadUsagef("params validation failed:\n  %s", strings.Join(msgs, "\n  "))
}

func collectErrors(err *jsonschema.ValidationError, msgs *[]string) {
	if len(err.Causes) == 0 {
		loc := "/" + strings.Join(err.InstanceLocation, "/")
		if loc == "/" {
			loc = "(root)"
		}
		// ErrorKind is an interface whose concrete values do not implement
		// fmt.Stringer, so formatting it directly exposes a Go struct such as
		// &{[threshold]}. BasicOutput attaches the library's English printer and
		// its OutputError.String returns the intended human-readable message.
		detail := err.Error()
		if output := err.BasicOutput(); output.Error != nil {
			detail = output.Error.String()
		}
		*msgs = append(*msgs, fmt.Sprintf("%s: %s", loc, detail))
		return
	}
	for _, cause := range err.Causes {
		collectErrors(cause, msgs)
	}
}
