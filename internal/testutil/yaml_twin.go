package testutil

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"gopkg.in/yaml.v3"
)

// AssertYAMLReadsAsJSON fails t unless yamlOut, read back, is the value
// jsonOut holds: the same keys, the same values, nothing added or dropped.
func AssertYAMLReadsAsJSON(t *testing.T, yamlOut, jsonOut string) {
	t.Helper()
	if err := yamlReadsAsJSON(yamlOut, jsonOut); err != nil {
		t.Error(err)
	}
}

// yamlReadsAsJSON is AssertYAMLReadsAsJSON's check.
func yamlReadsAsJSON(yamlOut, jsonOut string) error {
	var fromYAML interface{}
	if err := yaml.Unmarshal([]byte(yamlOut), &fromYAML); err != nil {
		return fmt.Errorf("-o yaml output does not parse: %w\n%s", err, yamlOut)
	}
	reencoded, err := json.Marshal(fromYAML)
	if err != nil {
		return fmt.Errorf("re-encode -o yaml output as JSON: %w\n%s", err, yamlOut)
	}
	var got, want interface{}
	if err := jsonnum.Unmarshal(reencoded, &got); err != nil {
		return fmt.Errorf("decode re-encoded -o yaml output: %w", err)
	}
	if err := jsonnum.Unmarshal([]byte(jsonOut), &want); err != nil {
		return fmt.Errorf("-o json output does not parse: %w\n%s", err, jsonOut)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("-o yaml does not read back as what -o json prints\n-o yaml:\n%s\n-o json:\n%s", yamlOut, jsonOut)
	}
	return nil
}
