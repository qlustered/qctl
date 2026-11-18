package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestFlaggerLabel(t *testing.T) {
	tests := []struct {
		name      string
		user      api.UserInfoTinySchema
		wantName  string
		wantLabel string
	}{
		{"full name", api.UserInfoTinySchema{FirstName: "Dana", LastName: "Ray", Email: "dana@x.io"}, "Dana Ray", "Dana Ray <dana@x.io>"},
		{"first name only", api.UserInfoTinySchema{FirstName: "Dana", Email: "dana@x.io"}, "Dana", "Dana <dana@x.io>"},
		{"blank name", api.UserInfoTinySchema{FirstName: " ", Email: "dana@x.io"}, "dana@x.io", "dana@x.io"},
		{"no email", api.UserInfoTinySchema{FirstName: "Dana", LastName: "Ray"}, "Dana Ray", "Dana Ray"},
		{"escape in name", api.UserInfoTinySchema{FirstName: "Da\x1b[2Jna", Email: "dana@x.io"}, `Da\x1b[2Jna`, `Da\x1b[2Jna <dana@x.io>`},
		{"role", api.UserInfoTinySchema{FirstName: "Ada", LastName: "Lovelace", Email: "ada@x.io", Role: rolePtr(api.RoleEnumExternal)}, "Ada Lovelace (external)", "Ada Lovelace (external) <ada@x.io>"},
		{"email only with role", api.UserInfoTinySchema{Email: "ada@x.io", Role: rolePtr(api.RoleEnumExternal)}, "ada@x.io (external)", "ada@x.io (external)"},
		{"role without email", api.UserInfoTinySchema{FirstName: "Ada", Role: rolePtr(api.RoleEnumEditor)}, "Ada (editor)", "Ada (editor)"},
		{"empty role", api.UserInfoTinySchema{FirstName: "Ada", Email: "ada@x.io", Role: rolePtr("")}, "Ada", "Ada <ada@x.io>"},
		{"escape in role", api.UserInfoTinySchema{FirstName: "Ada", Email: "ada@x.io", Role: rolePtr("x\x1b[2J")}, `Ada (x\x1b[2J)`, `Ada (x\x1b[2J) <ada@x.io>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FlaggerName(tt.user); got != tt.wantName {
				t.Errorf("FlaggerName = %q, want %q", got, tt.wantName)
			}
			if got := FlaggerLabel(tt.user); got != tt.wantLabel {
				t.Errorf("FlaggerLabel = %q, want %q", got, tt.wantLabel)
			}
		})
	}
}

func rolePtr(r api.RoleEnum) *api.RoleEnum { return &r }
