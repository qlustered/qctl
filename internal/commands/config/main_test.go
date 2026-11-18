package config

import (
	"os"
	"testing"

	"github.com/qlustered/qctl/internal/releases"
)

// TestMain bypasses use-context's release check, which would otherwise reach
// the context's server, git and $HOME/.qctl/versions. Tests of the check
// clear the variable and replace UseContextNewPreparerFunc.
func TestMain(m *testing.M) {
	_ = os.Setenv(releases.SkipEnv, "1")
	os.Exit(m.Run())
}
