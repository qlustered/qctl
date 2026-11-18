package agentskill

import (
	"path/filepath"
	"testing"
)

func TestCodexUserPathIgnoresCodeHomeAndLegacyKeepsItsFallback(t *testing.T) {
	home := t.TempDir()
	for _, override := range []string{"", t.TempDir()} {
		t.Run(override, func(t *testing.T) {
			i := &Installer{Target: TargetCodex, HostHome: override, UserHomeDir: func() (string, error) { return home, nil }}
			dir, err := i.Dir()
			if err != nil || dir != filepath.Join(home, ".agents", "skills", "qctl") {
				t.Fatalf("preferred path = %q, %v", dir, err)
			}
			i.LegacyCodex = true
			legacyRoot := override
			if legacyRoot == "" {
				legacyRoot = filepath.Join(home, ".codex")
			}
			dir, err = i.Dir()
			if err != nil || dir != filepath.Join(legacyRoot, "skills", "qctl") {
				t.Fatalf("legacy path = %q, %v", dir, err)
			}
		})
	}
}
