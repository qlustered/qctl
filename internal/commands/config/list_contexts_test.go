package config

import (
	"testing"

	"github.com/qlustered/qctl/internal/config"
)

func TestRenderOrgDisplay(t *testing.T) {
	tests := []struct {
		name string
		ctx  *config.Context
		want string
	}{
		{
			name: "empty cache, no default",
			ctx:  &config.Context{},
			want: "",
		},
		{
			name: "default only, empty cache",
			ctx:  &config.Context{OrganizationName: "acme"},
			want: "acme",
		},
		{
			name: "single cached org, no suffix",
			ctx: &config.Context{
				OrganizationName: "acme",
				Organizations:    []config.OrganizationRef{{ID: "id1", Name: "acme"}},
			},
			want: "acme",
		},
		{
			name: "multiple cached orgs show (+N more)",
			ctx: &config.Context{
				OrganizationName: "acme",
				Organizations: []config.OrganizationRef{
					{ID: "id1", Name: "acme"},
					{ID: "id2", Name: "widgets"},
					{ID: "id3", Name: "gizmos"},
				},
			},
			want: "acme (+2 more)",
		},
		{
			name: "default missing but cache populated",
			ctx: &config.Context{
				Organizations: []config.OrganizationRef{
					{ID: "id1", Name: "acme"},
					{ID: "id2", Name: "widgets"},
				},
			},
			want: "(+2 orgs)",
		},
		{
			name: "org UUID only (no name) with multiple",
			ctx: &config.Context{
				Organization: "uuid-abc",
				Organizations: []config.OrganizationRef{
					{ID: "uuid-abc", Name: "acme"},
					{ID: "uuid-def", Name: "widgets"},
				},
			},
			want: "uuid-abc (+1 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderOrgDisplay(tt.ctx)
			if got != tt.want {
				t.Errorf("renderOrgDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}
