package get

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupCloudSourcesTestCommand builds a root carrying the global flags the real
// root registers, with `get cloud-sources` under it. Verbose must be CountP:
// Bootstrap reads it with GetCount, and a BoolP yields verbosity 0 silently.
func setupCloudSourcesTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewCloudSourcesCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// cloudSourcesEnv wires /data-sources and counts the calls: a rejected filter
// must fail before the listing request, not after it.
type cloudSourcesEnv struct {
	t        *testing.T
	calls    int
	query    url.Values
	mock     *testutil.MockAPIServer
	lookedUp string
}

// serveDataset answers the name lookup --table performs, and records the name
// it was asked for. Unregistered, /datasets 404s — so a test that does not call
// this proves the command sent no lookup.
func (ce *cloudSourcesEnv) serveDataset(id int, name string) {
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		ce.lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
			Results: []api.DataSetTinySchema{{ID: id, Name: name}},
		})
	})
}

func newCloudSourcesEnv(t *testing.T) *cloudSourcesEnv {
	t.Helper()

	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})

	ce := &cloudSourcesEnv{t: t, mock: mock}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		ce.calls++
		ce.query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	return ce
}

func (ce *cloudSourcesEnv) run(args ...string) error {
	ce.t.Helper()
	rootCmd := setupCloudSourcesTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"get", "cloud-sources"}, args...))
	return rootCmd.Execute()
}

// --table-id 0 used to drop the filter and list every cloud source in the org
// at exit code 0.
func TestGetCloudSourcesRejectsNonPositiveTableID(t *testing.T) {
	for _, value := range []string{"0", "-9"} {
		t.Run(value, func(t *testing.T) {
			ce := newCloudSourcesEnv(t)

			err := ce.run("--table-id", value)
			if err == nil {
				t.Fatalf("expected an error for --table-id %s", value)
			}
			if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if ce.calls != 0 {
				t.Errorf("expected /data-sources never to be called, got %d call(s)", ce.calls)
			}
		})
	}
}

// The regression fence: a positive ID still reaches the query string.
func TestGetCloudSourcesSendsPositiveTableID(t *testing.T) {
	ce := newCloudSourcesEnv(t)

	if err := ce.run("--table-id", "7"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ce.query.Get("dataset_id"); got != "7" {
		t.Errorf("expected dataset_id=7, got %q", got)
	}
}

// --table is a NAME. The fixture is named "42" and has ID 7 — the one case an
// id-first resolver can never serve, so an Atoi short-circuit cannot pass here.
func TestGetCloudSourcesAcceptsTableName(t *testing.T) {
	ce := newCloudSourcesEnv(t)
	ce.serveDataset(7, "42")

	if err := ce.run("--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if ce.lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", ce.lookedUp)
	}
	if got := ce.query.Get("dataset_id"); got != "7" {
		t.Errorf("--table 42 must resolve to dataset_id=7, got %q", got)
	}
}

// A locally-invalid --order-by must fail before --table pays for a name
// lookup that can only be discarded. Resolving --table before
// parseCloudSourcesParams runs makes this test fail: the trap fires because
// the lookup runs first.
func TestGetCloudSourcesInvalidSortFieldSkipsTableLookup(t *testing.T) {
	ce := newCloudSourcesEnv(t)
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --order-by must fail before --table is looked up")
		})

	err := ce.run("--table", "nope", "--order-by", "bogus")
	if err == nil || !strings.Contains(err.Error(), "invalid sort field") {
		t.Fatalf("expected the sort-field error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if ce.calls != 0 {
		t.Errorf("expected /data-sources never to be called, got %d call(s)", ce.calls)
	}
}

// A locally-invalid --chunk-size must fail before --table pays for a name
// lookup that can only be discarded, the same ordering guarantee as
// TestGetCloudSourcesInvalidSortFieldSkipsTableLookup above.
func TestGetCloudSourcesInvalidChunkSizeSkipsTableLookup(t *testing.T) {
	ce := newCloudSourcesEnv(t)
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --chunk-size must fail before --table is looked up")
		})

	err := ce.run("--table", "nope", "--chunk-size", "0")
	if err == nil || !strings.Contains(err.Error(), "--chunk-size must be a positive integer, got 0") {
		t.Fatalf("expected the chunk-size error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if ce.calls != 0 {
		t.Errorf("expected /data-sources never to be called, got %d call(s)", ce.calls)
	}
}

// A --state outside the enum must fail before --table pays for a name lookup
// that can only be discarded, the same ordering guarantee as
// TestGetCloudSourcesInvalidSortFieldSkipsTableLookup.
func TestGetCloudSourcesInvalidStateSkipsTableLookup(t *testing.T) {
	ce := newCloudSourcesEnv(t)
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --state must fail before --table is looked up")
		})

	err := ce.run("--table", "nope", "--state", "failed")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if ce.calls != 0 {
		t.Errorf("expected /data-sources never to be called, got %d call(s)", ce.calls)
	}
}

// --limit -5 used to fall through every `maxResults > 0` guard in
// GetAllCloudSources, behaving exactly like --limit 0 (unlimited): it fetched
// and printed every cloud source in the organization, at exit 0;
// --chunk-size 0 used to fall back to 100 without saying so.
func TestGetCloudSourcesRejectsBadLimits(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--limit", "-5"}, want: "--limit cannot be negative, got -5"},
		{args: []string{"--chunk-size", "0"}, want: "--chunk-size must be a positive integer, got 0"},
		{args: []string{"--chunk-size", "-5"}, want: "--chunk-size must be a positive integer, got -5"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			ce := newCloudSourcesEnv(t)

			err := ce.run(tc.args...)
			if err == nil {
				t.Fatalf("expected an error for %v", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q, got: %v", tc.want, err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if ce.calls != 0 {
				t.Errorf("expected /data-sources never to be called, got %d call(s)", ce.calls)
			}
		})
	}
}

// --limit 0 is this command's documented "unlimited": it must still work.
func TestGetCloudSourcesAllowsZeroLimit(t *testing.T) {
	ce := newCloudSourcesEnv(t)

	if err := ce.run("--limit", "0"); err != nil {
		t.Fatalf("--limit 0 must stay legal on this command: %v", err)
	}
	if ce.calls == 0 {
		t.Error("expected the listing to be fetched")
	}
}

// `--state deleted` is the only way to find a soft-deleted cloud source. The
// help text once hid it, which made `undelete cloud-source` undiscoverable for
// anyone who did not already know the state existed.
func TestCloudSourcesStateFlagHelpListsDeleted(t *testing.T) {
	cmd := NewCloudSourcesCommand()
	usage := cmd.Flags().Lookup("state").Usage
	if !strings.Contains(usage, "deleted") {
		t.Errorf("--state help = %q, want it to list deleted", usage)
	}
}
