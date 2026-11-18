package testutil

import "testing"

func TestYAMLReadsAsJSON(t *testing.T) {
	const jsonOut = `{"family_id": "f1", "count": 1234567, "types": {"email": "string"}, "gone": null}`
	for _, tc := range []struct {
		name, yamlOut string
		match         bool
	}{
		{"same tree", "count: 1234567\nfamily_id: f1\ngone: null\ntypes:\n  email: string\n", true},
		{"lowercased Go name", "count: 1234567\nfamilyid: f1\ngone: null\ntypes:\n  email: string\n", false},
		{"union printed empty", "count: 1234567\nfamily_id: f1\ngone: null\ntypes:\n  email: {}\n", false},
		{"key JSON leaves out", "count: 1234567\nfamily_id: f1\ngone: null\ntypes:\n  email: string\nextra: null\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := yamlReadsAsJSON(tc.yamlOut, jsonOut); (err == nil) != tc.match {
				t.Errorf("match = %v, want %v (err: %v)", err == nil, tc.match, err)
			}
		})
	}
}
