package releases

import (
	"testing"

	"github.com/qlustered/qctl/internal/version"
)

func TestParseVersion(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want Version
		ok   bool
	}{
		{"2.135.1", Version{2, 135, 1}, true},
		{"0.0.0", Version{0, 0, 0}, true},
		{"10.20.30", Version{10, 20, 30}, true},
		{"v2.135.1", Version{}, false},
		{"-x", Version{}, false},
		{"--upload-pack=touch /tmp/pwned", Version{}, false},
		{"-2.135.1", Version{}, false},
		{"2.135", Version{}, false},
		{"2.135.1.4", Version{}, false},
		{"2.135.1-rc1", Version{}, false},
		{"2.135.1-3-gabcdef", Version{}, false},
		{"2.135.1-dirty", Version{}, false},
		{"2.135.01", Version{}, false},
		{" 2.135.1", Version{}, false},
		{"2.135.1\n", Version{}, false},
		{"2.135.x", Version{}, false},
		{"２.135.1", Version{}, false},
		{"dev", Version{}, false},
		{"", Version{}, false},
	} {
		got, err := ParseVersion(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
		if tt.ok && got.String() != tt.in {
			t.Errorf("ParseVersion(%q).String() = %q", tt.in, got.String())
		}
	}
}

func TestVersionCompareAndMinorKey(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"2.135.1", "2.135.1", 0},
		{"2.135.1", "2.135.2", -1},
		{"2.135.10", "2.135.9", 1},
		{"2.136.0", "2.135.99", 1},
		{"3.0.0", "2.999.999", 1},
		{"1.9.0", "1.10.0", -1},
	} {
		a, _ := ParseVersion(tt.a)
		b, _ := ParseVersion(tt.b)
		if got := a.Compare(b); got != tt.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := b.Compare(a); got != -tt.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", tt.b, tt.a, got, -tt.want)
		}
	}
	if got := (Version{2, 135, 7}).MinorKey(); got != "2.135" {
		t.Errorf("MinorKey = %q", got)
	}
}

func TestReleaseOfClassifiesBuilds(t *testing.T) {
	for _, tt := range []struct {
		build string
		ok    bool
	}{
		{"2.135.1", true},
		{"dev", false},
		{"2.135.1-4-g1a2b3c4", false},
		{"2.135.1-dirty", false},
		{"1a2b3c4", false},
		{"v2.135.1", false},
	} {
		if _, ok := ReleaseOf(tt.build); ok != tt.ok {
			t.Errorf("ReleaseOf(%q) ok = %v, want %v", tt.build, ok, tt.ok)
		}
	}

	saved := version.Version
	t.Cleanup(func() { version.Version = saved })
	version.Version = "2.140.3"
	if v, ok := RunningRelease(); !ok || v != (Version{2, 140, 3}) {
		t.Errorf("RunningRelease() = %v, %v", v, ok)
	}
	version.Version = "dev"
	if _, ok := RunningRelease(); ok {
		t.Error("RunningRelease() reports a dev build as a release")
	}
}

func TestNewestPicksTheHighestInTheMinor(t *testing.T) {
	vs := parseTags([]string{"2.135.0", "2.135.10", "2.135.9", "2.136.5", "2.13.50"})
	if got, ok := newest(vs, "2.135"); !ok || got.String() != "2.135.10" {
		t.Errorf("newest = %v, %v", got, ok)
	}
	if _, ok := newest(vs, "2.137"); ok {
		t.Error("newest found a release in a minor with none")
	}
}
