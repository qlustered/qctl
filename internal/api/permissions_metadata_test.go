package api

import (
	"bytes"
	"encoding/json"
	"go/format"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionMetadataIncludesDeliverableRoleSplit(t *testing.T) {
	for _, role := range StringEnumValues["Role"] {
		if _, ok := RolePermissions[role]; !ok {
			t.Errorf("role %q has no permission metadata", role)
		}
	}
	for role := range RolePermissions {
		if !contains(StringEnumValues["Role"], role) {
			t.Errorf("permission metadata has unknown role %q", role)
		}
	}
	if got := OperationPermissions["sign_off_deliverable_run"]; got != "deliverable:run:signoff" {
		t.Fatalf("sign-off permission = %q", got)
	}
	for _, role := range []string{"ops", "admin", "member", "editor", "agent"} {
		if !contains(RolePermissions[role], "deliverable:admin") {
			t.Errorf("%s lacks deliverable:admin", role)
		}
		want := role != "editor" && role != "agent"
		if got := contains(RolePermissions[role], "deliverable:run:signoff"); got != want {
			t.Errorf("%s sign-off permission = %v, want %v", role, got, want)
		}
	}
	if _, ok := OperationPermissions["get_me"]; ok {
		t.Error("authentication operation has a role permission")
	}
}

func TestPermissionMetadataGenerator(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "extract-metadata")
	if out, err := exec.Command("go", "build", "-o", binary, "../../scripts/extract-spec-metadata.go").CombinedOutput(); err != nil {
		t.Fatalf("build metadata generator: %v\n%s", err, out)
	}
	const fixture = `{
		"paths": {
			"/runs": {
				"parameters": [{"name":"id", "in":"query"}],
				"post": {"operationId":"z_sign_off", "x-required-permission":"run:signoff"},
				"get": {"operationId":"a_read", "x-required-permission":"run:read"}
			},
			"/auth": {"get": {"operationId":"get_me"}}
		},
		"components": {"schemas": {
			"RolePermissions": {"x-role-permissions":{"ops":["run:read","run:signoff"],"agent":["run:read"]}},
			"IssueTypeAlertActions": {"x-issue-type-alert-actions":{"alert":[]},
				"x-alert-action-catalog":{"actions":{"try_again":{"description":"Retries.","scope":["file"],"modifies_data":false,"follow_on":[],"reversible":false,"proposable":"proposable","same_effect_as":[]}},"issue_types":{"alert":{"business_label":null,"business_summary":null,"business_names_by_reason":null,"fix_owner":null,"fix_owner_by_reason":null,"user_resolvable":true,"actions":{},"no_action":null}}}},
			"IssueTypeWarningActions": {"x-issue-type-warning-actions":{"warning":[]},
				"x-warning-action-catalog":{"actions":{"try_again":{"description":"Retries.","scope":["file"],"modifies_data":false,"follow_on":[],"reversible":false,"proposable":"proposable","same_effect_as":[]}},"issue_types":{"warning":{"business_label":null,"business_summary":null,"business_names_by_reason":null,"fix_owner":null,"fix_owner_by_reason":null,"user_resolvable":true,"actions":{},"no_action":null}}}},
			"AlertResolveSchema": {"x-alert-action-fields":{"resolve":{}}},
			"WarningsResolveSchema": {"x-warning-action-fields":{"resolve":{}}},
			"ErrorCode": {"x-error-codes":["API_ERROR"]},
			"FieldType": {"type":"string", "enum":["String"]}
		}}
	}`
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
		want string
	}{
		{name: "valid"},
		{name: "missing permission extensions", edit: func(paths, _ map[string]any) { clear(paths) }, want: "no operation carries"},
		{name: "missing operation id", edit: func(paths, _ map[string]any) {
			delete(paths["/runs"].(map[string]any)["post"].(map[string]any), "operationId")
		}, want: "has no non-empty operationId"},
		{name: "empty permission", edit: func(paths, _ map[string]any) {
			paths["/runs"].(map[string]any)["post"].(map[string]any)["x-required-permission"] = ""
		}, want: "is not a non-empty string"},
		{name: "malformed permission", edit: func(paths, _ map[string]any) {
			paths["/runs"].(map[string]any)["post"].(map[string]any)["x-required-permission"] = []string{"run:signoff"}
		}, want: "is not a non-empty string"},
		{name: "duplicate operation", edit: func(paths, _ map[string]any) {
			paths["/runs"].(map[string]any)["post"].(map[string]any)["operationId"] = "a_read"
		}, want: "duplicate permission-bearing operationId"},
		{name: "missing roles", edit: func(_, schemas map[string]any) { delete(schemas, "RolePermissions") }, want: "no component"},
		{name: "empty roles", edit: func(_, schemas map[string]any) {
			schemas["RolePermissions"].(map[string]any)["x-role-permissions"] = map[string]any{}
		}, want: "extracted to an empty map"},
		{name: "unknown catalog key", edit: func(_, schemas map[string]any) {
			alertCatalog(schemas)["actions"].(map[string]any)["try_again"].(map[string]any)["undo_hint"] = "x"
		}, want: "is not an action catalog"},
		{name: "catalog misses an offered action", edit: func(_, schemas map[string]any) {
			schemas["IssueTypeAlertActions"].(map[string]any)["x-issue-type-alert-actions"] = map[string]any{"alert": []string{"try_again"}}
		}, want: "describes actions []"},
		{name: "catalog misses an issue type", edit: func(_, schemas map[string]any) {
			delete(alertCatalog(schemas)["issue_types"].(map[string]any), "alert")
		}, want: "has no entry for issue type"},
		{name: "catalog entry without a description", edit: func(_, schemas map[string]any) {
			alertCatalog(schemas)["actions"].(map[string]any)["try_again"].(map[string]any)["description"] = ""
		}, want: "has no description"},
		{name: "malformed roles", edit: func(_, schemas map[string]any) {
			schemas["RolePermissions"].(map[string]any)["x-role-permissions"] = map[string]any{"ops": "run:signoff"}
		}, want: "is not map[string][]string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec map[string]any
			if err := json.Unmarshal([]byte(fixture), &spec); err != nil {
				t.Fatal(err)
			}
			if tc.edit != nil {
				tc.edit(spec["paths"].(map[string]any), spec["components"].(map[string]any)["schemas"].(map[string]any))
			}
			input, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary)
			cmd.Stdin = bytes.NewReader(input)
			out, err := cmd.CombinedOutput()
			if tc.want != "" {
				if err == nil || !strings.Contains(string(out), tc.want) {
					t.Fatalf("error = %v, output = %s; want %q", err, out, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("generate: %v\n%s", err, out)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("invalid Go: %v\n%s", err, out)
			}
			for _, want := range []string{
				`"a_read":     "run:read"`, `"z_sign_off": "run:signoff"`,
				`"agent": {"run:read"}`, `"ops":   {"run:read", "run:signoff"}`,
			} {
				if !strings.Contains(string(formatted), want) {
					t.Errorf("generated metadata lacks %q:\n%s", want, formatted)
				}
			}
			if strings.Contains(string(formatted), "get_me") {
				t.Error("unpermissioned auth operation was emitted")
			}
			if bytes.Index(formatted, []byte(`"a_read"`)) > bytes.Index(formatted, []byte(`"z_sign_off"`)) {
				t.Error("operation map is not sorted")
			}
		})
	}
}

func alertCatalog(schemas map[string]any) map[string]any {
	return schemas["IssueTypeAlertActions"].(map[string]any)["x-alert-action-catalog"].(map[string]any)
}
