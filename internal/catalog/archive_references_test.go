package catalog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var localFrozenArchivePath = regexp.MustCompile(
	strings.Join([]string{"docs", "superpowers"}, "/") + `/[[:alnum:]_./-]+`,
)

// TestFrozenArchiveReferencesResolve keeps local links into the removed
// archive from surviving after their target files are deleted.
func TestFrozenArchiveReferencesResolve(t *testing.T) {
	for _, source := range trackedFiles(t) {
		if strings.HasPrefix(source, strings.Join([]string{"docs", "superpowers"}, "/")+"/") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(repositoryRoot, source))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		if bytes.IndexByte(content, 0) >= 0 {
			continue
		}
		for _, match := range localFrozenArchivePath.FindAllIndex(content, -1) {
			// References explicitly rooted in the sibling atlas checkout are
			// external citations, not paths in this repository.
			before := string(content[:match[0]])
			if strings.HasSuffix(before, "../atlas/") {
				continue
			}
			path := string(content[match[0]:match[1]])
			if _, err := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(path))); err != nil {
				t.Errorf("%s cites missing local archive path %q: %v", source, path, err)
			}
		}
	}
}
