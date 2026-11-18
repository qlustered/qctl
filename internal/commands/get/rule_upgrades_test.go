package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const ruleUpgradesTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	ruFamilyID = uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	ruNewRevID = uuid.MustParse("0190eeee-1111-2222-3333-444444444444")
	ruOldRevID = uuid.MustParse("0190aaaa-9999-2222-3333-444444444444")
	ruRuleID   = uuid.MustParse("0190ffff-1111-2222-3333-444444444444")
)

func setupRuleUpgradesTestCommand() *cobra.Command {
	// SilenceUsage/SilenceErrors mirror the real root so a RunE error does not
	// dump the usage block into the stdout these tests assert on.
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewRuleUpgradesCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func newRuleUpgradesTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", ruleUpgradesTestOrgID)
	env.SetupCredential(endpointKey, ruleUpgradesTestOrgID, "test-token")
	return mock
}

func ruRevision(id uuid.UUID, release string, isDefault bool) api.RuleRevisionTinySchema {
	return api.RuleRevisionTinySchema{
		ID: id, FamilyID: ruFamilyID, Name: "phone_format", Slug: "phone_format",
		Release: release, IsDefault: isDefault, State: api.RuleState("enabled"),
	}
}

// registerResolution stubs the two requests ResolveFamily makes.
func registerResolution(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	revs := []api.RuleRevisionTinySchema{
		ruRevision(ruOldRevID, "2.1.0", false),
		ruRevision(ruNewRevID, "3.0.0", true),
	}
	mock.RegisterHandler("GET", "/api/orgs/"+ruleUpgradesTestOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{
				Results: revs, TotalRows: testutil.IntPtr(len(revs)),
			})
		})
	// Registered for EVERY revision id so the family lookup succeeds whichever
	// one the resolver picks. That is not the same as leaving the choice free:
	// TestGetRuleUpgradesRequestBoundary asserts the all-releases request goes
	// to ruOldRevID -- see the note on its `want` list.
	for _, rev := range revs {
		mock.RegisterHandler("GET",
			"/api/orgs/"+ruleUpgradesTestOrgID+"/rule-revisions/"+rev.ID.String(),
			func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionsListSchema{
					FamilyID: ruFamilyID, Name: "phone_format", Results: revs,
				})
			})
	}
}

func registerDetails(t *testing.T, mock *testutil.MockAPIServer, d api.RuleFamilyUpgradeDetailsSchema) {
	t.Helper()
	mock.RegisterHandler("GET",
		"/api/orgs/"+ruleUpgradesTestOrgID+"/rule-families/"+ruFamilyID.String()+"/upgrade-details",
		func(w http.ResponseWriter, r *http.Request) { testutil.RespondJSON(w, http.StatusOK, d) })
}

func fullDetails() api.RuleFamilyUpgradeDetailsSchema {
	release := "3.0.0"
	return api.RuleFamilyUpgradeDetailsSchema{
		FamilyID: ruFamilyID, FamilyName: "phone_format",
		NewDefaultRelease: &release, NewDefaultRevisionID: &ruNewRevID,
		OutdatedItems: []api.OutdatedDatasetRuleSchema{{
			DatasetID: 42, DatasetName: "orders", DatasetRuleID: ruRuleID,
			InstanceName: "phone_check", CurrentRevisionID: ruOldRevID, CurrentRelease: "2.1.0",
		}},
	}
}

func runRuleUpgradesCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupRuleUpgradesTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "rule-upgrades"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestGetRuleUpgradesTable(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	out, _, err := runRuleUpgradesCmd(t, "phone_format")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"TABLE", "RULE", "ON", "UPGRADE-TO", "orders", "phone_check", "2.1.0", "3.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// table_id and id are reachable only through --columns; they must not be
	// in the default view.
	if strings.Contains(out, ruRuleID.String()) {
		t.Errorf("default columns leaked the dataset_rule_id\n%s", out)
	}
}

// -o name must print the dataset_rule_ids: they are what `qctl describe
// table-rule` takes, and the row struct deliberately has no name/email field
// so the shared renderer falls through to id.
func TestGetRuleUpgradesOutputName(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	out, _, err := runRuleUpgradesCmd(t, "phone_format", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, ruRuleID.String()) {
		t.Errorf("-o name did not print the dataset rule id\n%s", out)
	}
}

// -o json is the ONLY view carrying new_default_revision_id and each item's
// current_revision_id, so it must be the raw wire object, not the table rows.
func TestGetRuleUpgradesOutputJSONIsRaw(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	out, _, err := runRuleUpgradesCmd(t, "phone_format", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"new_default_revision_id", "current_revision_id", "family_id", "outdated_items"} {
		if !strings.Contains(out, want) {
			t.Errorf("-o json missing %q\n%s", want, out)
		}
	}
}

// -o yaml goes through a different encoder than -o json and must be raw too.
func TestGetRuleUpgradesOutputYAMLIsRaw(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	out, _, err := runRuleUpgradesCmd(t, "phone_format", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"new_default_revision_id:", "outdated_items:", "current_revision_id:"} {
		if !strings.Contains(out, want) {
			t.Errorf("-o yaml missing %q\n%s", want, out)
		}
	}
}

// table_id and id are in the full column set even though the default view
// omits them; --columns must reach them.
func TestGetRuleUpgradesColumnsReachTheIDs(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	out, _, err := runRuleUpgradesCmd(t, "phone_format", "--columns", "table_id,id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"TABLE-ID", "42", ruRuleID.String()} {
		if !strings.Contains(out, want) {
			t.Errorf("--columns output missing %q\n%s", want, out)
		}
	}
}

// The command must reach exactly the three endpoints it claims: the catalog
// listing and all-releases to find the family, then upgrade-details. A fourth
// would mean the addressing step grew a request nobody costed.
func TestGetRuleUpgradesRequestBoundary(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, fullDetails())

	if _, _, err := runRuleUpgradesCmd(t, "phone_format"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Filtered to the rule routes: Bootstrap's own traffic, if it ever grows
	// any, is not what this test is about.
	var got []string
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/rule-revisions") || strings.Contains(req, "/rule-families") {
			got = append(got, req)
		}
	}
	// want[1] pins matches[0], the FIRST entry of the catalog listing that
	// registerResolution serves: ResolveRuleAny -> selectAnyFromMatches
	// (internal/rule_versions/resolver.go) returns matches[0] for a family with
	// several releases, and that listing puts ruOldRevID first. A resolver that
	// instead preferred the is_default revision would legitimately break this
	// line; update it deliberately rather than reading the failure as a bug.
	want := []string{
		"GET /api/orgs/" + ruleUpgradesTestOrgID + "/rule-revisions",
		"GET /api/orgs/" + ruleUpgradesTestOrgID + "/rule-revisions/" + ruOldRevID.String(),
		"GET /api/orgs/" + ruleUpgradesTestOrgID + "/rule-families/" + ruFamilyID.String() + "/upgrade-details",
	}
	if len(got) != len(want) {
		t.Fatalf("rule requests = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A name that resolves to nothing must fail in the resolver, before any
// upgrade-details request is built from an empty family id.
func TestGetRuleUpgradesUnresolvableNameNeverCallsUpgradeDetails(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+ruleUpgradesTestOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{
				Results: []api.RuleRevisionTinySchema{}, TotalRows: testutil.IntPtr(0),
			})
		})

	if _, _, err := runRuleUpgradesCmd(t, "no_such_rule"); err == nil {
		t.Fatal("expected an error")
	}
	for _, req := range mock.Requests() {
		if strings.Contains(req, "upgrade-details") {
			t.Errorf("upgrade-details was called after resolution failed: %v", mock.Requests())
		}
	}
}

// Nothing outdated is the happy ending, and it must SAY so — including that
// the answer is scoped to what this user can see.
func TestGetRuleUpgradesEmptyWhenEverythingIsCurrent(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	d := fullDetails()
	d.OutdatedItems = nil
	registerDetails(t, mock, d)

	_, errOut, err := runRuleUpgradesCmd(t, "phone_format")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"phone_format", "already on 3.0.0", "you can see"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q\n%s", want, errOut)
		}
	}
}

// A family with no default revision cannot have anything outdated, and saying
// "everything is current" there would be a different, wrong claim.
func TestGetRuleUpgradesEmptyWhenThereIsNoDefaultRevision(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	registerDetails(t, mock, api.RuleFamilyUpgradeDetailsSchema{
		FamilyID: ruFamilyID, FamilyName: "phone_format",
	})

	_, errOut, err := runRuleUpgradesCmd(t, "phone_format")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"no default revision", "qctl set-default rule phone_format"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q\n%s", want, errOut)
		}
	}
}

func TestGetRuleUpgradesUnknownFamilyIsExitThree(t *testing.T) {
	mock := newRuleUpgradesTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("GET",
		"/api/orgs/"+ruleUpgradesTestOrgID+"/rule-families/"+ruFamilyID.String()+"/upgrade-details",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"msg":"RuleFamily not found"}}`))
		})

	if _, _, err := runRuleUpgradesCmd(t, "phone_format"); err == nil {
		t.Fatal("expected an error")
	} else if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}
