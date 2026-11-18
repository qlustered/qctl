// Package releases selects the qctl release that matches an atlas server's
// version and keeps the local record of installed releases.
package releases

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/qlustered/qctl/internal/version"
)

// tagPattern is the only accepted spelling of a release tag.
var tagPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Version is a release version, MAJOR.MINOR.PATCH.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion parses a bare MAJOR.MINOR.PATCH tag. Anything else, including
// a leading "v", a pre-release suffix or a leading zero, is an error.
func ParseVersion(s string) (Version, error) {
	m := tagPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid release version %q: want MAJOR.MINOR.PATCH, such as 2.135.1", s)
	}
	var v Version
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("invalid release version %q: %w", s, err)
		}
		*dst = n
	}
	return v, nil
}

// String returns the tag spelling, MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// MinorKey returns MAJOR.MINOR, the atlas line a release supports.
func (v Version) MinorKey() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Compare returns -1, 0 or +1 as v is older than, equal to or newer than o.
func (v Version) Compare(o Version) int {
	for _, d := range [...]int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
	}
	return 0
}

// ReleaseOf reports whether build, a binary's embedded version, names a
// release, and which.
func ReleaseOf(build string) (Version, bool) {
	v, err := ParseVersion(build)
	return v, err == nil
}

// RunningRelease reports whether this binary is a release build, and which.
func RunningRelease() (Version, bool) {
	return ReleaseOf(version.Version)
}

// newest returns the highest of vs whose MinorKey is minor.
func newest(vs []Version, minor string) (Version, bool) {
	var best Version
	found := false
	for _, v := range vs {
		if v.MinorKey() != minor {
			continue
		}
		if !found || v.Compare(best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}
