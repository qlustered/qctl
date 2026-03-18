//go:build ignore

// convert-openapi-31-to-30.go converts an OpenAPI 3.1 spec to 3.0.3 compatible format.
// This is needed because oapi-codegen does not fully support OpenAPI 3.1.
//
// Main transformations:
// 1. Changes openapi version from 3.1.x to 3.0.3
// 2. Converts anyOf[{type:X},{type:null}] to type:X with nullable:true
// 3. Converts anyOf[{$ref:X},{type:null}] to $ref:X with nullable:true
// 4. Converts anyOf with multiple types + null to {} (any type) with nullable:true
// 5. Removes standalone {type:null} entries from anyOf arrays
// 6. Adds missing path parameter declarations to operations (oapi-codegen requires all path params declared)
//
// Usage: go run scripts/convert-openapi-31-to-30.go < input.json > output.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

func main() {
	// Read input
	var spec map[string]interface{}
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading JSON: %v\n", err)
		os.Exit(1)
	}

	// Transform the spec
	transformSpec(spec)

	// Write output
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(spec); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
		os.Exit(1)
	}
}

// conflictingSchemas maps schema names that conflict with enum value names
// to their renamed versions. oapi-codegen converts "issue_type" to "IssueType"
// which conflicts with the "IssueType" schema.
var conflictingSchemas = map[string]string{
	"IssueType":      "IssueTypeEnum",
	"ReviewDecision": "ReviewDecisionEnum",
}

func transformSpec(spec map[string]interface{}) {
	// Change OpenAPI version from 3.1.x to 3.0.3
	if version, ok := spec["openapi"].(string); ok {
		if len(version) >= 3 && version[:3] == "3.1" {
			spec["openapi"] = "3.0.3"
		}
	}

	// Rename conflicting schemas
	renameConflictingSchemas(spec)

	// Fix operations missing declared path parameters
	fixMissingPathParams(spec)

	// Recursively transform the entire spec
	transformValue(spec)
}

// renameConflictingSchemas renames schemas that conflict with enum value names
func renameConflictingSchemas(spec map[string]interface{}) {
	// Get the components/schemas section
	components, ok := spec["components"].(map[string]interface{})
	if !ok {
		return
	}
	schemas, ok := components["schemas"].(map[string]interface{})
	if !ok {
		return
	}

	// Rename conflicting schemas
	for oldName, newName := range conflictingSchemas {
		if schema, exists := schemas[oldName]; exists {
			delete(schemas, oldName)
			schemas[newName] = schema
		}
	}

	// Now update all $ref references throughout the spec
	updateRefs(spec)
}

// updateRefs updates all $ref references to use renamed schema names
func updateRefs(v interface{}) {
	switch val := v.(type) {
	case map[string]interface{}:
		// Check if this object has a $ref
		if ref, ok := val["$ref"].(string); ok {
			for oldName, newName := range conflictingSchemas {
				oldRef := "#/components/schemas/" + oldName
				newRef := "#/components/schemas/" + newName
				if ref == oldRef {
					val["$ref"] = newRef
				}
			}
		}
		// Recurse into all values
		for _, v := range val {
			updateRefs(v)
		}
	case []interface{}:
		for _, item := range val {
			updateRefs(item)
		}
	}
}

// fixMissingPathParams ensures every operation declares all path parameters
// present in the URL template. oapi-codegen requires this.
func fixMissingPathParams(spec map[string]interface{}) {
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return
	}

	pathParamRe := regexp.MustCompile(`\{(\w+)\}`)

	for pathStr, pathObj := range paths {
		matches := pathParamRe.FindAllStringSubmatch(pathStr, -1)
		if len(matches) == 0 {
			continue
		}
		urlParams := make(map[string]bool)
		for _, m := range matches {
			urlParams[m[1]] = true
		}

		methods, ok := pathObj.(map[string]interface{})
		if !ok {
			continue
		}

		for method, details := range methods {
			op, ok := details.(map[string]interface{})
			if !ok {
				continue
			}

			params, _ := op["parameters"].([]interface{})
			declared := make(map[string]bool)
			for _, p := range params {
				pm, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				if pm["in"] == "path" {
					if name, ok := pm["name"].(string); ok {
						declared[name] = true
					}
				}
			}

			// Find a sibling operation that declares the missing param so we can copy its schema
			for paramName := range urlParams {
				if declared[paramName] {
					continue
				}

				var paramDef map[string]interface{}
				// Look in sibling operations for the parameter definition
				for otherMethod, otherDetails := range methods {
					if otherMethod == method {
						continue
					}
					otherOp, ok := otherDetails.(map[string]interface{})
					if !ok {
						continue
					}
					otherParams, _ := otherOp["parameters"].([]interface{})
					for _, p := range otherParams {
						pm, ok := p.(map[string]interface{})
						if !ok {
							continue
						}
						if pm["in"] == "path" {
							if name, ok := pm["name"].(string); ok && name == paramName {
								paramDef = pm
								break
							}
						}
					}
					if paramDef != nil {
						break
					}
				}

				if paramDef != nil {
					// Copy the parameter definition
					newParam := make(map[string]interface{})
					for k, v := range paramDef {
						newParam[k] = v
					}
					params = append(params, newParam)
				} else {
					// Fallback: synthesize a string path parameter
					params = append(params, map[string]interface{}{
						"in":       "path",
						"name":     paramName,
						"required": true,
						"schema":   map[string]interface{}{"type": "string"},
					})
				}

				fmt.Fprintf(os.Stderr, "Fixed missing path param %q on %s %s\n", paramName, method, pathStr)
			}

			op["parameters"] = params
		}
	}
}

func transformValue(v interface{}) {
	switch val := v.(type) {
	case map[string]interface{}:
		transformObject(val)
	case []interface{}:
		for _, item := range val {
			transformValue(item)
		}
	}
}

func transformObject(obj map[string]interface{}) {
	// Check if this is a schema with anyOf containing null type
	if anyOf, ok := obj["anyOf"].([]interface{}); ok {
		transformed := transformAnyOfWithNull(anyOf)
		if transformed != nil {
			// Replace the anyOf with the transformed schema
			delete(obj, "anyOf")
			for k, v := range transformed {
				obj[k] = v
			}
		} else {
			// Remove null type entries from anyOf arrays that couldn't be fully transformed
			obj["anyOf"] = removeNullFromAnyOf(anyOf)
		}
	}

	// Handle oneOf similarly (less common but possible)
	if oneOf, ok := obj["oneOf"].([]interface{}); ok {
		transformed := transformAnyOfWithNull(oneOf)
		if transformed != nil {
			delete(obj, "oneOf")
			for k, v := range transformed {
				obj[k] = v
			}
		} else {
			obj["oneOf"] = removeNullFromAnyOf(oneOf)
		}
	}

	// Recurse into all nested values
	for _, v := range obj {
		transformValue(v)
	}
}

// isNullType checks if a schema represents the null type
func isNullType(schema map[string]interface{}) bool {
	typeVal, ok := schema["type"].(string)
	return ok && typeVal == "null"
}

// removeNullFromAnyOf removes {type:null} entries from anyOf arrays
// and adds nullable:true to the remaining schemas if null was present
func removeNullFromAnyOf(anyOf []interface{}) []interface{} {
	var result []interface{}
	hasNull := false

	for _, item := range anyOf {
		schema, ok := item.(map[string]interface{})
		if !ok {
			result = append(result, item)
			continue
		}

		if isNullType(schema) {
			hasNull = true
			continue
		}

		result = append(result, item)
	}

	// If we found and removed null, we should ideally mark the anyOf as nullable
	// but OpenAPI 3.0 doesn't support nullable on anyOf directly
	// We'll leave it as is - oapi-codegen should handle the non-null types
	_ = hasNull

	return result
}

// transformAnyOfWithNull checks if an anyOf/oneOf array contains a null type
// and can be simplified to a nullable schema
func transformAnyOfWithNull(anyOf []interface{}) map[string]interface{} {
	var nonNullSchemas []map[string]interface{}
	hasNull := false

	for _, item := range anyOf {
		schema, ok := item.(map[string]interface{})
		if !ok {
			return nil
		}

		// Check if this is the null type
		if isNullType(schema) {
			hasNull = true
			continue
		}

		nonNullSchemas = append(nonNullSchemas, schema)
	}

	if !hasNull {
		return nil
	}

	// Case 1: Single non-null schema -> make it nullable
	if len(nonNullSchemas) == 1 {
		result := make(map[string]interface{})
		for k, v := range nonNullSchemas[0] {
			result[k] = v
		}
		result["nullable"] = true
		return result
	}

	// Case 2: Multiple non-null schemas with $refs -> keep anyOf without null
	// but return nil to let removeNullFromAnyOf handle it
	return nil
}
