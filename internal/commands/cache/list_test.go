package cache

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupCacheCommand builds a root command with the global flags the cache commands read.
func setupCacheCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("config", "", "config file")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "") // CountP, not BoolP
	rootCmd.PersistentFlags().StringP("org", "O", "", "")
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

// seedAlias allocates one alias in the default (HOME-scoped) store for serverKey.
func seedAlias(t *testing.T, serverKey, uuid string) string {
	t.Helper()
	s, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	alias, err := s.Allocate(serverKey, aliasstore.ResourceDatasetRule, uuid)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	return alias
}

func TestCacheAliasesList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "list", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"r-1", "550e8400-e29b-41d4-a716-446655440000", "dataset_rule"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestCacheAliasesListEmpty(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfig("https://api.qluster.test", "")

	cmd := setupCacheCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"cache", "aliases", "list", "--server", "https://api.qluster.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "No aliases") {
		t.Errorf("empty list should report 'No aliases', got: %q", buf.String())
	}
}

// runList runs cache aliases list against the test server with extra args,
// validating -o as root.go does.
func runList(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	cmd := setupCacheCommand()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error { return cmdutil.ValidateOutputFormat(c) }
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"cache", "aliases", "list", "--server", "https://api.qluster.test"}, extra...))
	err := cmd.Execute()
	return stdout.String(), err
}

func TestCacheAliasesListJSON(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig("https://api.qluster.test", "")
	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")
	seedAlias(t, serverKey, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	stdout, err := runList(t, "-o", "json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	want := []map[string]any{
		{"alias": "r-1", "resource": "table-rule", "uuid": "550e8400-e29b-41d4-a716-446655440000"},
		{"alias": "r-2", "resource": "table-rule", "uuid": "6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v\nwant %v", rows, want)
	}
}

func TestCacheAliasesListYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig("https://api.qluster.test", "")
	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")

	stdout, err := runList(t, "-o", "yaml")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout is not a YAML sequence: %v\n%s", err, stdout)
	}
	want := []map[string]any{{"alias": "r-1", "resource": "table-rule", "uuid": "550e8400-e29b-41d4-a716-446655440000"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v\nwant %v", rows, want)
	}
}

// Both empty paths print [] under json: no store file yet, and a store with
// no aliases for this server.
func TestCacheAliasesListEmptyIsAnEmptyArray(t *testing.T) {
	t.Run("no store file", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		env.SetupConfig("https://api.qluster.test", "")
		stdout, err := runList(t, "-o", "json")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if stdout != "[]\n" {
			t.Errorf("stdout = %q, want []", stdout)
		}
	})
	t.Run("no aliases for this server", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		env.SetupConfig("https://api.qluster.test", "")
		otherKey, _ := config.NormalizeEndpointKey("https://other.qluster.test")
		seedAlias(t, otherKey, "550e8400-e29b-41d4-a716-446655440000")
		stdout, err := runList(t, "-o", "json")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if stdout != "[]\n" {
			t.Errorf("stdout = %q, want []", stdout)
		}
	})
}

// The table view is unchanged: the store's own resource type, not the catalog's.
func TestCacheAliasesListTableIsUnchanged(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig("https://api.qluster.test", "")
	serverKey, _ := config.NormalizeEndpointKey("https://api.qluster.test")
	seedAlias(t, serverKey, "550e8400-e29b-41d4-a716-446655440000")

	stdout, err := runList(t)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "ALIAS  RESOURCE      UUID\nr-1    dataset_rule  550e8400-e29b-41d4-a716-446655440000\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestCacheAliasesListHasNoNameView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig("https://api.qluster.test", "")
	stdout, err := runList(t, "-o", "name")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// An alias whose prefix no resource type registers has a null resource.
func TestAliasRowsResourceIsNullForAnUnknownPrefix(t *testing.T) {
	rows := aliasRows([]aliasstore.Entry{{Alias: "x-1", UUID: "550e8400-e29b-41d4-a716-446655440000"}})
	if rows[0].Resource != nil {
		t.Errorf("resource = %q, want nil", *rows[0].Resource)
	}
}

// Every alias prefix the store registers names its catalog resource, so a new
// aliased resource cannot print its internal type under -o json.
func TestEveryAliasResourceTypeHasACatalogName(t *testing.T) {
	registered := 0
	for c := 'a'; c <= 'z'; c++ {
		rt, err := aliasstore.ResourceTypeForPrefix(string(c))
		if err != nil {
			continue
		}
		registered++
		if _, ok := catalogResources[rt]; !ok {
			t.Errorf("alias resource type %q (prefix %q) is not in catalogResources", rt, string(c))
		}
	}
	if registered == 0 {
		t.Fatal("no alias prefix in a-z resolves; the walk no longer sees the store's prefixes")
	}
}
