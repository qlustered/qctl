package root

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	qctlconfig "github.com/qlustered/qctl/internal/config"
	internalversion "github.com/qlustered/qctl/internal/version"
)

func TestExecute(t *testing.T) {
	// Basic test to ensure Execute doesn't panic
	// More comprehensive tests will be added as commands are implemented
	// Expected to fail without arguments, but shouldn't panic
	_ = Execute()
}

func TestGetRootCmd(t *testing.T) {
	cmd := GetRootCmd()
	if cmd == nil {
		t.Fatal("GetRootCmd returned nil")
	}

	if cmd.Use != "qctl" {
		t.Errorf("Expected Use to be 'qctl', got '%s'", cmd.Use)
	}
}

func TestVersion(t *testing.T) {
	if internalversion.Version == "" {
		t.Error("Version should not be empty")
	}
	if internalversion.Commit == "" {
		t.Error("Commit should not be empty")
	}
}

// --config was registered on root but bound to a variable nothing read, so
// passing it silently did nothing. Run a real command through Execute so the
// test covers the whole path: flag parsing -> cobra initializer -> the config
// package that every command loads from.
func TestConfigFlagRedirectsConfigPath(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "alternate-config")

	cmd := GetRootCmd()
	cmd.SetArgs([]string{"--config", overridePath, "version"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	defer func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		qctlconfig.SetPathOverride("")
		cfgFile = ""
	}()

	if err := cmd.Execute(); err != nil {
		t.Fatalf("qctl --config %s version failed: %v", overridePath, err)
	}

	got, err := qctlconfig.GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}
	if got != overridePath {
		t.Errorf("--config did not take effect: config path = %q, want %q", got, overridePath)
	}
}

// The root help is the one screen every user and agent sees first. It must
// name all three discovery surfaces, not just api-resources.
func TestRootHelpNamesEveryDiscoverySurface(t *testing.T) {
	long := GetRootCmd().Long
	for _, want := range []string{
		"START HERE", "qctl api-resources", "qctl explain", "qctl guide",
		"--search",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("root help does not mention %q:\n%s", want, long)
		}
	}
}

// Root help is where a user finds out the skill installer exists at all, and
// the one place that has to say installing it is optional -- an agent that
// reads "install this first" will try to, on a machine where it cannot write.
// The host list is interpolated from agentskill rather than typed here, so
// asserting the join is what fires if someone replaces it with a literal.
func TestRootHelpOffersTheSkillGroupWithoutRequiringIt(t *testing.T) {
	long := GetRootCmd().Long
	for _, want := range []string{
		"AGENT SKILL", "qctl skill install", agentskill.JoinTargets(), "Optional",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("root help does not mention %q:\n%s", want, long)
		}
	}
}

// The same screen is where an agent decides what to do about an access
// failure. Saying it here is what makes "do not try another organization" a
// rule it read before it failed, not one it has to infer afterwards.
func TestRootHelpStatesOrganizationScope(t *testing.T) {
	long := GetRootCmd().Long
	for _, want := range []string{
		"ORGANIZATION SCOPE",
		"An empty list, a 403 or a 404",
		"--org, else QCTL_ORG",
		"qctl never retries in another one",
		"qctl auth\n  switch-org",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("root help does not state %q:\n%s", want, long)
		}
	}
}
