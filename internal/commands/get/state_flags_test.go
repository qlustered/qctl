package get

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// stateFlagListing is one listing whose --state (and --states, where it has
// one) is checked against a generated enum before any request.
type stateFlagListing struct {
	command string
	setup   func() *cobra.Command
	flags   []string
	valid   []string
}

func stateFlagListings() []stateFlagListing {
	return []stateFlagListing{
		{"cloud-sources", setupCloudSourcesTestCommand, []string{"state", "states"}, api.EnumValues[api.DataSourceState]()},
		{"tables", setupTestCommand, []string{"state", "states"}, api.EnumValues[api.DataSetState]()},
		{"ingestion-jobs", setupTestCommandForIngestion, []string{"state", "states"}, ingestion.JobStates()},
		{"rule-revisions", setupRuleRevisionsTestCommand, []string{"state"}, append([]string{"none"}, api.EnumValues[api.RuleState]()...)},
	}
}

// runStateFlagListing runs `get <command> args...` against a mock with no
// handlers, so a request that escapes validation 404s instead of passing.
func runStateFlagListing(t *testing.T, l stateFlagListing, args ...string) (*testutil.MockAPIServer, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := l.setup()
	cmd.SetArgs(append([]string{"get", l.command}, args...))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return mock, cmd.Execute()
}

func TestStateFlagsRefuseValuesOutsideTheEnum(t *testing.T) {
	for _, l := range stateFlagListings() {
		// An uppercased member is refused too: the enums are case-sensitive.
		bad := []string{"failed", strings.ToUpper(l.valid[1])}
		for _, flag := range l.flags {
			values := bad
			if flag == "states" {
				values = []string{l.valid[0] + ",failed", l.valid[0] + ","}
			}
			for _, value := range values {
				t.Run(l.command+" --"+flag+" "+value, func(t *testing.T) {
					mock, err := runStateFlagListing(t, l, "--"+flag, value)
					if err == nil {
						t.Fatalf("expected an error for --%s %q", flag, value)
					}
					if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
						t.Errorf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
					}
					for _, part := range []string{"--" + flag + ":", l.valid[len(l.valid)-1]} {
						if !strings.Contains(err.Error(), part) {
							t.Errorf("error %q does not contain %q", err, part)
						}
					}
					if got := len(mock.Requests()); got != 0 {
						t.Errorf("made %d request(s), want 0: %v", got, mock.Requests())
					}
				})
			}
		}
	}
}

// The help text and the validator read one list, so every value the command
// accepts is one the help advertises.
func TestStateFlagHelpListsEveryAcceptedValue(t *testing.T) {
	for _, l := range stateFlagListings() {
		cmd := l.setup()
		sub, _, err := cmd.Find([]string{"get", l.command})
		if err != nil {
			t.Fatalf("get %s: %v", l.command, err)
		}
		usage := sub.Flags().Lookup("state").Usage
		for _, value := range l.valid {
			if !strings.Contains(usage, value) {
				t.Errorf("get %s --state help %q does not list %q", l.command, usage, value)
			}
		}
	}
}

// Every member reaches the wire, each --states member trimmed and sent as its
// own states parameter.
func TestStatesFlagSendsEveryMember(t *testing.T) {
	for _, tc := range []struct {
		command string
		route   string
		setup   func() *cobra.Command
		valid   []string
	}{
		{"cloud-sources", "/data-sources", setupCloudSourcesTestCommand, api.EnumValues[api.DataSourceState]()},
		{"tables", "/datasets", setupTestCommand, api.EnumValues[api.DataSetState]()},
	} {
		t.Run(tc.command, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var sent []string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+tc.route, func(w http.ResponseWriter, r *http.Request) {
				sent = r.URL.Query()["states"]
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := tc.setup()
			cmd.SetArgs([]string{"get", tc.command, "--states", strings.Join(tc.valid, " , ")})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(sent, tc.valid) {
				t.Errorf("states = %q, want %q", sent, tc.valid)
			}
		})
	}
}

func TestRuleRevisionsStateSendsEveryRuleState(t *testing.T) {
	for _, state := range api.EnumValues[api.RuleState]() {
		t.Run(state, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var sent []string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
				sent = r.URL.Query()["state_filter"]
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupRuleRevisionsTestCommand()
			cmd.SetArgs([]string{"get", "rule-revisions", "--state", state})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := []string{state}; !slices.Equal(sent, want) {
				t.Errorf("state_filter = %q, want %q", sent, want)
			}
		})
	}
}
