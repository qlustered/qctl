package aliasstore

import "testing"

func TestParseAlias(t *testing.T) {
	cases := []struct {
		in        string
		prefix    string
		ordinal   int
		wantError bool
	}{
		{"r-1", "r", 1, false},
		{"r-42", "r", 42, false},
		{"u-7", "u", 7, false},
		{"r-0", "", 0, true},   // ordinal must be >= 1
		{"r--1", "", 0, true},  // negative
		{"r", "", 0, true},     // no separator
		{"r-", "", 0, true},    // empty ordinal
		{"-1", "", 0, true},    // empty prefix
		{"r-1-2", "", 0, true}, // too many parts
		{"r-x", "", 0, true},   // non-numeric ordinal
		{"", "", 0, true},      // empty
	}
	for _, c := range cases {
		prefix, ord, err := ParseAlias(c.in)
		if c.wantError {
			if err == nil {
				t.Errorf("ParseAlias(%q): expected error, got prefix=%q ord=%d", c.in, prefix, ord)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAlias(%q): unexpected error %v", c.in, err)
			continue
		}
		if prefix != c.prefix || ord != c.ordinal {
			t.Errorf("ParseAlias(%q) = (%q,%d), want (%q,%d)", c.in, prefix, ord, c.prefix, c.ordinal)
		}
	}
}

func TestLooksLikeAlias(t *testing.T) {
	yes := []string{"r-1", "r-42", "u-7"}
	no := []string{"r-0", "r", "", "550e8400-e29b-41d4-a716-446655440000", "42", "email_check"}
	for _, s := range yes {
		if !LooksLikeAlias(s) {
			t.Errorf("LooksLikeAlias(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if LooksLikeAlias(s) {
			t.Errorf("LooksLikeAlias(%q) = true, want false", s)
		}
	}
}

func TestResourceTypeForPrefix(t *testing.T) {
	rt, err := ResourceTypeForPrefix("r")
	if err != nil || rt != ResourceDatasetRule {
		t.Errorf("ResourceTypeForPrefix(r) = (%v,%v), want (%v,nil)", rt, err, ResourceDatasetRule)
	}
	if _, err := ResourceTypeForPrefix("zzz"); err == nil {
		t.Errorf("ResourceTypeForPrefix(zzz): expected error")
	}
}

func TestPrefixForResource(t *testing.T) {
	p, err := prefixFor(ResourceDatasetRule)
	if err != nil || p != "r" {
		t.Errorf("prefixFor(ResourceDatasetRule) = (%q,%v), want (r,nil)", p, err)
	}
	if _, err := prefixFor(ResourceType("nope")); err == nil {
		t.Errorf("prefixFor(nope): expected error")
	}
}
