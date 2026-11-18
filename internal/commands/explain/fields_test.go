package explain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

const nestedFieldSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Nested fields", "version": "1"},
  "paths": {},
  "components": {"schemas": {
    "DataSetSchemaFull": {
      "type": "object",
      "properties": {"settings_model": {
        "type": "object",
        "properties": {"config": {
          "type": "object",
          "properties": {"rules": {
            "type": "object",
            "properties": {"priorities": {
              "type": "object",
              "properties": {"entries": {
                "type": "array",
                "items": {"$ref": "#/components/schemas/Node"}
              }}
            }}
          }}
        }}
      }}
    },
    "Node": {
      "type": "object",
      "required": ["label"],
      "properties": {
        "label": {"type": "string", "description": "The node's label."},
        "descendants": {"type": "array", "items": {"$ref": "#/components/schemas/Node"}}
      }
    }
  }}
}`

func TestExplainLiveObjectAndArrayFields(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-nested-fields-1", nestedFieldSpec)

	for _, tc := range []struct {
		path      string
		recursive bool
		want      []string
		absent    string
	}{
		{
			path:   "table.settings_model",
			want:   []string{"FIELDS:\n  config <object>\n", "To explain a child field: qctl explain table.settings_model.config\n"},
			absent: "label <string>",
		},
		{
			path:      "table",
			recursive: true,
			want:      []string{"[array item properties]\n", "label <string> <required>\n", "descendants <[]Node>\n"},
		},
		{
			path:      "table.settings_model",
			recursive: true,
			want:      []string{"[array item properties]\n", "label <string> <required>\n", "descendants <[]Node>\n"},
		},
		{
			path:   "table.settings_model.config.rules.priorities.entries",
			want:   []string{"FIELDS:\n  [array item properties]\n", "label <string> <required>\n"},
			absent: "To explain a child field:",
		},
	} {
		t.Run(fmt.Sprintf("%s/recursive=%t", tc.path, tc.recursive), func(t *testing.T) {
			args := []string{"explain", tc.path}
			if tc.recursive {
				args = append(args, "--recursive")
			}
			out, errOut, err := runExplain(t, args...)
			if err != nil {
				t.Fatalf("explain: %v\nstderr:\n%s", err, errOut)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			if tc.absent != "" && strings.Contains(out, tc.absent) {
				t.Errorf("unexpected %q:\n%s", tc.absent, out)
			}
		})
	}
}
