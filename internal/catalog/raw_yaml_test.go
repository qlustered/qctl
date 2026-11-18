package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The mechanism this file guards.
//
// "-o yaml of a wire response" was written by hand twelve times: json.Marshal,
// json.Unmarshal into interface{}, yaml.v3 at indent 2. Every copy decoded
// numbers as float64, so an integer of seven or more digits came out in
// exponent form (1234567 as 1.234567e+06) and one past 2^53 was rounded.
// output.WriteRaw is the one copy now, and it goes through WriteJSONAsYAML,
// which keeps the integer. A thirteenth copy would bring the float64 decode
// back with it.

// jsonToYAMLHome is the one place allowed to decode JSON and encode YAML in the
// same function: output.WriteJSONAsYAML lives there.
const jsonToYAMLHome = "internal/output/"

// jsonToYAMLFuncs reports the top-level functions in path that call both a
// JSON decoder (json.Unmarshal, json.NewDecoder) and a YAML encoder
// (jsonnum.NewYAMLEncoder, jsonnum.MarshalYAML, yaml.NewEncoder, yaml.Marshal),
// including inside function literals they contain.
func jsonToYAMLFuncs(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("../..", path), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var decodes, encodes bool
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call) {
			case "json.Unmarshal", "json.NewDecoder":
				decodes = true
			case "jsonnum.NewYAMLEncoder", "jsonnum.MarshalYAML", "yaml.NewEncoder", "yaml.Marshal":
				encodes = true
			}
			return true
		})
		if decodes && encodes {
			found = append(found, fn.Name.Name)
		}
	}
	return found
}

// TestNoHandRolledJSONToYAML is the tripwire for a new copy of the round trip.
func TestNoHandRolledJSONToYAML(t *testing.T) {
	for _, path := range goSourceFiles(t) {
		if strings.HasPrefix(path, jsonToYAMLHome) {
			continue
		}
		for _, fn := range jsonToYAMLFuncs(t, path) {
			t.Errorf("%s %s decodes JSON and encodes YAML itself.\n"+
				"Decoding into interface{} makes every number a float64, so yaml.v3\n"+
				"prints 1234567 as 1.234567e+06. To print a wire response for -o json|yaml\n"+
				"call output.WriteRaw(w, format, data); for a JSON body already in hand,\n"+
				"output.WriteJSONAsYAML(w, body).", path, fn)
		}
	}

	// Harness-alive control: the one sanctioned copy must still match, or a
	// rename in the detector would leave this passing over nothing at all.
	home := filepath.Join(jsonToYAMLHome, "raw_body.go")
	if got := jsonToYAMLFuncs(t, home); len(got) != 1 || got[0] != "WriteJSONAsYAML" {
		t.Fatalf("expected the detector to find WriteJSONAsYAML in %s, found %v -- this sweep is checking nothing", home, got)
	}
}
