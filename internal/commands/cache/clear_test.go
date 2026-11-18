package cache

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestCacheAliasesClearCurrentServer(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	keyA, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	keyB, _ := config.NormalizeEndpointKey("https://other.qluster.test")
	seedAlias(t, keyA, "550e8400-e29b-41d4-a716-446655440000")
	seedAlias(t, keyB, "660e8400-e29b-41d4-a716-446655440001")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "clear", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	store, _ := aliasstore.Open()
	defer store.Close()
	if entries, _ := store.List(keyA); len(entries) != 0 {
		t.Errorf("server A aliases not cleared: %d remain", len(entries))
	}
	if entries, _ := store.List(keyB); len(entries) != 1 {
		t.Errorf("server B aliases must be untouched, got %d", len(entries))
	}
}

func TestCacheAliasesClearAllServers(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	keyA, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	keyB, _ := config.NormalizeEndpointKey("https://other.qluster.test")
	seedAlias(t, keyA, "550e8400-e29b-41d4-a716-446655440000")
	seedAlias(t, keyB, "660e8400-e29b-41d4-a716-446655440001")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "clear", "--all-servers", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	store, _ := aliasstore.Open()
	defer store.Close()
	if entries, _ := store.List(keyB); len(entries) != 0 {
		t.Errorf("--all-servers should clear server B too, got %d", len(entries))
	}
}

// The behavioral half of the claim: Clear drops the whole server bucket
// including bktCounters (aliasstore/store.go), decodeCounter(nil) is 0, so
// Allocate reissues r-1 — to a DIFFERENT rule.
func TestCacheAliasesClearResetsTheCounter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	key, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	first := seedAlias(t, key, "550e8400-e29b-41d4-a716-446655440000")
	if first != "r-1" {
		t.Fatalf("expected the first allocation to be r-1, got %s", first)
	}

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "clear", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	second := seedAlias(t, key, "660e8400-e29b-41d4-a716-446655440001")
	if second != "r-1" {
		t.Fatalf("expected the counter to reset and hand r-1 to a different rule, got %s", second)
	}
}

// The prose half: --help must not promise the uniqueness the store does not
// provide.
func TestCacheAliasesClearHelpDoesNotPromiseUniqueness(t *testing.T) {
	long := newClearCommand().Long
	if strings.Contains(long, "never reused") {
		t.Errorf("clear --help still promises aliases are never reused:\n%s", long)
	}
	if !strings.Contains(long, "r-1") {
		t.Errorf("clear --help should say the next allocation starts back at r-1, got:\n%s", long)
	}
}
