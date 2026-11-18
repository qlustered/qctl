package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

func TestConvertNumberFormats(t *testing.T) {
	const input = `{
	  "openapi": "3.1.0",
	  "components": {"schemas": {
	    "plain": {"type": "number", "minimum": 0, "default": 0.123456789},
	    "nullable": {"anyOf": [{"type": "number", "exclusiveMinimum": 0}, {"type": "null"}]},
	    "oneOf": {"oneOf": [{"type": "number"}, {"type": "null"}]},
	    "array": {"type": "array", "items": {"type": "number"}},
	    "map": {"type": "object", "additionalProperties": {"type": "number"}},
	    "object": {"type": "object", "properties": {"value": {"type": "number"}}},
	    "float": {"type": "number", "format": "float"},
	    "double": {"type": "number", "format": "double"},
	    "integer": {"type": "integer"}
	  }},
	  "paths": {"/values": {"get": {"parameters": [
	    {"in": "query", "name": "share", "schema": {"type": "number"}}
	  ]}}}
	}`
	const want = `{
	  "openapi": "3.0.3",
	  "components": {"schemas": {
	    "plain": {"type": "number", "format": "double", "minimum": 0, "default": 0.123456789},
	    "nullable": {"type": "number", "format": "double", "nullable": true, "minimum": 0, "exclusiveMinimum": true},
	    "oneOf": {"type": "number", "format": "double", "nullable": true},
	    "array": {"type": "array", "items": {"type": "number", "format": "double"}},
	    "map": {"type": "object", "additionalProperties": {"type": "number", "format": "double"}},
	    "object": {"type": "object", "properties": {"value": {"type": "number", "format": "double"}}},
	    "float": {"type": "number", "format": "float"},
	    "double": {"type": "number", "format": "double"},
	    "integer": {"type": "integer"}
	  }},
	  "paths": {"/values": {"get": {"parameters": [
	    {"in": "query", "name": "share", "schema": {"type": "number", "format": "double"}}
	  ]}}}
	}`
	cmd := exec.Command("go", "run", "convert-openapi-31-to-30.go")
	cmd.Stdin = bytes.NewBufferString(input)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert: %v\n%s", err, raw)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("converted spec:\n%s\nwant:\n%s", raw, want)
	}
}

func TestConvertNullableReferences(t *testing.T) {
	const input = `{
  "openapi":"3.1.0",
  "components":{"schemas":{
   "Kind":{"type":"string","enum":["first","second"]},
   "Payload":{"type":"object","properties":{
    "required":{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":"null"}],"description":"Required kind"},
    "optional":{"anyOf":[{"type":"null"},{"$ref":"#/components/schemas/Kind"}]},
    "explicit":{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":"null"}],"x-omitempty":false},
    "one_of":{"oneOf":[{"$ref":"#/components/schemas/Payload"},{"type":"null"}]},
    "plain":{"$ref":"#/components/schemas/Kind"},
    "array":{"type":"array","items":{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":"null"}]}},
    "map":{"type":"object","additionalProperties":{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":"null"}]}},
    "renamed":{"anyOf":[{"$ref":"#/components/schemas/IssueType"},{"type":"null"}]}
   },"required":["required","one_of"]},
   "IssueType":{"type":"string"}
  }}
 }`
	const want = `{
  "openapi":"3.0.3",
  "components":{"schemas":{
   "Kind":{"type":"string","enum":["first","second"]},
   "Payload":{"type":"object","properties":{
    "required":{"allOf":[{"$ref":"#/components/schemas/Kind"}],"nullable":true,"description":"Required kind"},
    "optional":{"allOf":[{"$ref":"#/components/schemas/Kind"}],"nullable":true,"x-omitempty":true},
    "explicit":{"allOf":[{"$ref":"#/components/schemas/Kind"}],"nullable":true,"x-omitempty":false},
    "one_of":{"allOf":[{"$ref":"#/components/schemas/Payload"}],"nullable":true},
    "plain":{"$ref":"#/components/schemas/Kind"},
    "array":{"type":"array","items":{"allOf":[{"$ref":"#/components/schemas/Kind"}],"nullable":true}},
    "map":{"type":"object","additionalProperties":{"allOf":[{"$ref":"#/components/schemas/Kind"}],"nullable":true}},
    "renamed":{"allOf":[{"$ref":"#/components/schemas/IssueTypeEnum"}],"nullable":true,"x-omitempty":true}
   },"required":["required","one_of"]},
   "IssueTypeEnum":{"type":"string"}
  }}
 }`
	cmd := exec.Command("go", "run", "convert-openapi-31-to-30.go")
	cmd.Stdin = bytes.NewBufferString(input)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert: %v\n%s", err, raw)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("converted spec:\n%s\nwant:\n%s", raw, want)
	}
}
