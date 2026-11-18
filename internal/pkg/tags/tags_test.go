package tags

import "testing"

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		pairs []TagPair
		want  string
	}{
		{
			name:  "no pairs",
			pairs: nil,
			want:  "-",
		},
		{
			name: "all inactive",
			pairs: []TagPair{
				{Label: "Default", Active: false},
				{Label: "Built-in", Active: false},
			},
			want: "-",
		},
		{
			name: "single active",
			pairs: []TagPair{
				{Label: "Default", Active: true},
				{Label: "Built-in", Active: false},
			},
			want: "Default",
		},
		{
			name: "multiple active",
			pairs: []TagPair{
				{Label: "Default", Active: true},
				{Label: "Built-in", Active: true},
			},
			want: "Default, Built-in",
		},
		{
			name: "three active with order preserved",
			pairs: []TagPair{
				{Label: "Default", Active: true},
				{Label: "Built-in", Active: true},
				{Label: "CAF", Active: true},
			},
			want: "Default, Built-in, CAF",
		},
		{
			name: "middle inactive",
			pairs: []TagPair{
				{Label: "Default", Active: true},
				{Label: "Built-in", Active: false},
				{Label: "Upgrade Available", Active: true},
			},
			want: "Default, Upgrade Available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.pairs...)
			if got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}
