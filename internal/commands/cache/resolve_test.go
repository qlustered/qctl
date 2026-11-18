package cache

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestCacheAliasesResolve(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "resolve", "r-1", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("resolve output = %q, want UUID", buf.String())
	}
}

// TestCacheAliasesResolveUnknown covers the fresh/no-DB case: resolving any alias
// when the alias store DB file does not exist yet should return a clean "unknown alias"
// error rather than a raw file-not-found error.
func TestCacheAliasesResolveUnknown(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "resolve", "r-99", "--server", "https://api.qluster.test"})
	err := cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; error: %v", code, qerrors.ExitNotFound, err)
	}
	if err == nil {
		t.Errorf("resolve of unknown alias on fresh DB should error")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown alias") {
		t.Errorf("expected clean 'unknown alias' error, got: %v", err)
	}
}

// TestCacheAliasesResolveUnknownInExistingStore covers the case where the DB file
// exists (at least one alias has been allocated) but the requested alias is absent.
func TestCacheAliasesResolveUnknownInExistingStore(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	// Seed one alias so the DB file exists.
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// Resolve a different alias that was never allocated.
	cmd.SetArgs([]string{"cache", "aliases", "resolve", "r-99", "--server", "https://api.qluster.test"})
	err := cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; error: %v", code, qerrors.ExitNotFound, err)
	}
	if err == nil {
		t.Errorf("resolve of absent alias in existing store should error")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown alias") {
		t.Errorf("expected 'unknown alias' error, got: %v", err)
	}
}

func TestCacheAliasesResolveMalformed(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "resolve", "not-an-alias", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("malformed alias exit code = %d, want %d; error: %v", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
}
