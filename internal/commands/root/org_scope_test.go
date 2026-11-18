package root

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	scopeTestOrgID  = "550e8400-e29b-41d4-a716-446655440000"
	scopeOtherOrgID = "11111111-2222-3333-4444-555555555555"
)

// resetPersistentFlag restores a root persistent flag to its default AND
// clears Changed.
//
// GetRootCmd() returns a package-level singleton, so a flag one test sets is
// still set for the next one. Clearing Changed matters as much as the value:
// ApplyContextOutputFormat only overrides -o when the flag was not changed,
// so a test that leaves output "changed" silently disables that for every
// test after it. --config is bound to the cfgFile package variable with
// StringVar, so resetting the flag resets the variable too.
func resetPersistentFlag(t *testing.T, cmd *cobra.Command, name, def string) {
	t.Helper()
	f := cmd.PersistentFlags().Lookup(name)
	if f == nil {
		t.Fatalf("root has no persistent flag %q", name)
	}
	if err := f.Value.Set(def); err != nil {
		t.Fatalf("resetting --%s: %v", name, err)
	}
	f.Changed = false
}

// runRootScoped executes the real qctl command tree and returns its error.
//
// Named apart from output_format_test.go's runRoot, which shares this package
// but returns (stdout, error) and resets a different set of flags.
func runRootScoped(t *testing.T, args ...string) error {
	t.Helper()
	cmd := GetRootCmd()

	for _, f := range []struct{ name, def string }{
		{"org", ""}, {"output", "table"}, {"config", ""}, {"server", ""},
	} {
		resetPersistentFlag(t, cmd, f.name, f.def)
	}

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		for _, f := range []struct{ name, def string }{
			{"org", ""}, {"output", "table"}, {"config", ""}, {"server", ""},
		} {
			resetPersistentFlag(t, cmd, f.name, f.def)
		}
		config.SetPathOverride("")
		apierror.SetOrgScope("", "")
	})

	// The buffer exists only to keep the command's own output off the test
	// log; every assertion here is on the error and on the request log.
	return apierror.FinalizeScopeNote(cmd.Execute())
}

const scopeTestTableManifest = `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
  backup_key_format: "{dataset_id}/{data_source_id}"
  anomaly_threshold: 10
  max_retry_count: 2
  max_tries_to_fix_json: 1
`

// The incident this whole change exists for, and its 404 twin. One request,
// to the endpoint the user asked for, and then a stop -- no organization
// list, no switch-organization-idp, no device-flow poll.
//
// Two mechanisms, not one. The org list and switch-organization-idp go to
// the mock server, so an extra call to either lands in mock.Requests() and
// fails the request-set assertion below -- including for a fallback nobody
// has written yet. A device-flow poll goes to the KINDE host instead, which
// the mock transport refuses without recording (testutil/http.go); it is
// caught because that refusal changes the error, breaking the exit-code and
// note assertions.
func TestAccessFailureMakesExactlyOneRequestAndChangesNothing(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantExit int
		wantNote string
	}{
		{
			name:     "403 the account cannot see this table",
			status:   http.StatusForbidden,
			body:     `{"detail":"User is not authorized to access table 8"}`,
			wantExit: qerrors.ExitUnauthorized,
			wantNote: "No other organization will help.",
		},
		{
			name:     "403 wrong organization",
			status:   http.StatusForbidden,
			body:     `{"detail":"Table 8 is not associated with this organization"}`,
			wantExit: qerrors.ExitUnauthorized,
			wantNote: "does\nnot search other organizations",
		},
		{
			name:     "404 no such table",
			status:   http.StatusNotFound,
			body:     `{"detail":"Table 8 not found"}`,
			wantExit: qerrors.ExitNotFound,
			wantNote: "does\nnot search other organizations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			path := "/api/orgs/" + scopeTestOrgID + "/datasets/8"
			mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})

			env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, scopeTestOrgID, "token")

			before, err := os.ReadFile(env.ConfigPath)
			if err != nil {
				t.Fatalf("reading the config before the run: %v", err)
			}

			runErr := runRootScoped(t, "get", "table", "8")

			if runErr == nil {
				t.Fatal("expected the command to fail")
			}
			if got := qerrors.GetExitCode(runErr); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d", got, tt.wantExit)
			}
			if !strings.Contains(runErr.Error(), "Scoped to organization "+scopeTestOrgID) {
				t.Errorf("error = %q, want it to name the organization it was scoped to", runErr)
			}
			if !strings.Contains(runErr.Error(), tt.wantNote) {
				t.Errorf("error = %q, want the no-other-organizations note", runErr)
			}

			want := []string{"GET " + path}
			if got := mock.Requests(); len(got) != 1 || got[0] != want[0] {
				t.Errorf("requests = %v, want exactly %v.\n"+
					"Any extra request is a cross-organization probe: the org list "+
					"or switch-organization-idp. (A device-flow poll goes to the "+
					"Kinde host, which this recorder never sees -- it fails the "+
					"assertions above instead.)", got, want)
			}

			after, err := os.ReadFile(env.ConfigPath)
			if err != nil {
				t.Fatalf("reading the config after the run: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the config file changed during a failed request:\nbefore:\n%s\nafter:\n%s",
					before, after)
			}
		})
	}
}

// The acceptance criteria name mutating commands explicitly. apply's first
// request is the existence check, so a 403 or 404 there stops it in the same
// one request -- and the config is still untouched.
//
// The two subtests produce BYTE-IDENTICAL error text: the same server
// message, the same note. Only qerrors.GetExitCode separates them, 4 from 3.
// So neither is a duplicate of the other, and neither can be re-expressed as
// an assertion on the message -- deleting one, or "simplifying" the pair into
// a single message check, silently drops half the 403/404 coverage. This is
// CLAUDE.md's "test the code, not the text" with the two shown side by side.
func TestApplyStopsAtTheFirstAccessFailure(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		wantExit int
	}{
		{"403", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"404", http.StatusNotFound, qerrors.ExitNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			path := "/api/orgs/" + scopeTestOrgID + "/datasets"
			mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				w.Write([]byte(`{"detail":"Table orders is not associated with this organization"}`))
			})

			env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, scopeTestOrgID, "token")
			manifest := env.CreateFile("table.yaml", scopeTestTableManifest)

			before, err := os.ReadFile(env.ConfigPath)
			if err != nil {
				t.Fatalf("reading the config before the run: %v", err)
			}

			runErr := runRootScoped(t, "apply", "-f", manifest)

			if runErr == nil {
				t.Fatal("expected apply to fail")
			}
			if got := qerrors.GetExitCode(runErr); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d", got, tt.wantExit)
			}
			if !strings.Contains(runErr.Error(), "Scoped to organization "+scopeTestOrgID) {
				t.Errorf("error = %q, want it to name the organization it was scoped to", runErr)
			}
			if got := mock.Requests(); len(got) != 1 || got[0] != "GET "+path {
				t.Errorf("requests = %v, want exactly [GET %s] -- apply must stop at the "+
					"first access failure, not retry anywhere else", got, path)
			}

			after, err := os.ReadFile(env.ConfigPath)
			if err != nil {
				t.Fatalf("reading the config after the run: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("the config file changed during a failed apply")
			}
		})
	}
}

// The other half of the guarantee: a user who deliberately names another
// organization still gets one. The scope is fixed, not frozen -- it is just
// never qctl's to change.
func TestExplicitOrgFlagStillReachesTheServer(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	otherPath := "/api/orgs/" + scopeOtherOrgID + "/datasets/8"
	mock.RegisterHandler("GET", otherPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":"User is not authorized to access table 8"}`))
	})

	env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, scopeOtherOrgID, "token")

	runErr := runRootScoped(t, "get", "table", "8", "--org", scopeOtherOrgID)

	if runErr == nil {
		t.Fatal("expected the command to fail against the other organization too")
	}
	if got := mock.Requests(); len(got) != 1 || got[0] != "GET "+otherPath {
		t.Errorf("requests = %v, want exactly [GET %s] -- --org must reach the server "+
			"with the organization the user named", got, otherPath)
	}
	if !strings.Contains(runErr.Error(), "(from the --org flag)") {
		t.Errorf("error = %q, want the note to name --org as the source", runErr)
	}
}
