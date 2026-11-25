package apply

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// testSiblingRuleID is a rule already on table 1, distinct from
// testTableRuleID.
const testSiblingRuleID = "77770000-1111-2222-3333-444444444444"

// listedTableRule is a table-rule list row on table 1. Its family is
// sampleRuleRevisionFull's unless the caller changes it.
func listedTableRule(id, name string, state api.DatasetRuleState, columns ...string) dataset_rules.DatasetRuleTiny {
	return dataset_rules.DatasetRuleTiny{
		ID:             uuid.MustParse(id),
		InstanceName:   name,
		FamilyID:       sampleRuleRevisionFull(nil).FamilyID,
		FamilyName:     "email_validator",
		RuleRevisionID: uuid.MustParse(testRuleRevID),
		Release:        "1.0.0",
		Position:       1,
		Severity:       api.RuleSeverityAsDeclared,
		State:          state,
		DatasetColumns: &columns,
		CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

// registerSiblingRuleDetail answers the detail GET for testSiblingRuleID with
// the given column mapping and no params.
func registerSiblingRuleDetail(mock *testutil.MockAPIServer, mapping map[string]string) {
	registerSiblingRuleDetailWithParams(mock, mapping, map[string]interface{}{})
}

// registerSiblingRuleDetailWithParams answers the detail GET for
// testSiblingRuleID with the given column mapping and params.
func registerSiblingRuleDetailWithParams(mock *testutil.MockAPIServer, mapping map[string]string, params map[string]interface{}) {
	mock.RegisterHandler("GET", siblingDetailPath, func(w http.ResponseWriter, r *http.Request) {
		detail := sampleDatasetRuleDetailForApply()
		detail.ID = uuid.MustParse(testSiblingRuleID)
		detail.ColumnMappingDict = mapping
		detail.Params = params
		testutil.RespondJSON(w, http.StatusOK, detail)
	})
}

// siblingDetailPath is the detail route for testSiblingRuleID.
const siblingDetailPath = "/api/orgs/" + testOrgID + "/dataset-rules/" + testSiblingRuleID

// siblingDetailFetched reports whether the mock saw the detail GET for
// testSiblingRuleID.
func siblingDetailFetched(mock *testutil.MockAPIServer) bool {
	for _, r := range mock.Requests() {
		if r == "GET "+siblingDetailPath {
			return true
		}
	}
	return false
}

// assertCreatedWithWarning fails unless err is nil, one create was sent, and
// stderr carries the same-family warning.
func assertCreatedWithWarning(t *testing.T, mock *testutil.MockAPIServer, stderr string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("another instance is not refused, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	if !strings.Contains(stderr, "adds another instance") {
		t.Errorf("expected the same-family warning:\n%s", stderr)
	}
}

// instantiatePath is the create route for testRuleRevID.
const instantiatePath = "/rule-revisions/" + testRuleRevID + "/instantiate"

// postCount is how many create requests the mock saw.
func postCount(mock *testutil.MockAPIServer) int {
	n := 0
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST ") && strings.HasSuffix(r, instantiatePath) {
			n++
		}
	}
	return n
}

// namelessCreateDoc is a create manifest with no instance name, binding email
// to email_col.
func namelessCreateDoc() string {
	return `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: email_col
`
}

// namelessIdentityDoc is a create manifest with no instance name and no
// column mapping.
func namelessIdentityDoc() string {
	return `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`
}

// assertRefusedWithoutPost fails unless err is exit 2 and no create was sent.
func assertRefusedWithoutPost(t *testing.T, mock *testutil.MockAPIServer, err error) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
	if n := postCount(mock); n != 0 {
		t.Errorf("a refused create sent %d POSTs: %v", n, mock.Requests())
	}
}

// A create whose spec.instance_name another rule on the table holds is
// refused before the POST, under --yes too, whatever state that rule is in.
func TestApplyTableRuleCreate_TakenInstanceNameExitsTwo(t *testing.T) {
	for _, tc := range []struct {
		state api.DatasetRuleState
		want  []string
	}{
		{api.DatasetRuleStateEnabled, []string{"metadata.id: " + testSiblingRuleID, "spec.upgradedFrom: " + testSiblingRuleID, "choose another spec.instance_name"}},
		{api.DatasetRuleStateDisabled, []string{"metadata.id: " + testSiblingRuleID}},
		{api.DatasetRuleStatePendingValidation, []string{"metadata.id: " + testSiblingRuleID}},
		{api.DatasetRuleStateError, []string{"metadata.id: " + testSiblingRuleID, "omit spec.state so the repair is validated", "spec.state: disabled or deleted"}},
		{api.DatasetRuleStateDeleted, []string{"the name stays taken", "omit it for a generated one"}},
		{api.DatasetRuleStateValidationFailed, []string{"the name stays taken", "omit it for a generated one"}},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var req api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
			registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", tc.state, "email_col"))

			_, err := execApply(t, env.CreateFile("taken.yaml", tableRuleInstantiateDoc("email_check")), "--yes")
			assertRefusedWithoutPost(t, mock, err)
			for _, want := range append([]string{`"email_check"`, "state: " + string(tc.state)}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not contain %q:\n%v", want, err)
				}
			}
		})
	}
}

// The name comparison is exact: a name differing only in case is free, and
// the create goes through.
func TestApplyTableRuleCreate_InstanceNameMatchIsCaseSensitive(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	other := listedTableRule(testSiblingRuleID, "Email_Check", api.DatasetRuleStateEnabled, "email_col")
	other.FamilyID = uuid.MustParse("99990000-1111-2222-3333-444444444444")
	registerTableRulesList(mock, 1, other)

	if _, err := execApply(t, env.CreateFile("case.yaml", tableRuleInstantiateDoc("email_check")), "--yes"); err != nil {
		t.Fatalf("expected the create to go through, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
}

// A taken name that is the predecessor spec.upgradedFrom names is still
// refused, and the message says why.
func TestApplyTableRuleCreate_UpgradeCannotReuseThePredecessorsName(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &req)
	registerTableRulesList(mock, 1, listedTableRule(testTableRuleID, "email_check", api.DatasetRuleStateEnabled, "email_col"))

	manifest := upgradeManifest(testTableRuleID) + "  instance_name: email_check\n"
	_, err := execApply(t, env.CreateFile("upgrade-same-name.yaml", manifest), "--yes")
	assertRefusedWithoutPost(t, mock, err)
	if want := "spec.upgradedFrom names that rule"; !strings.Contains(err.Error(), want) {
		t.Errorf("error does not contain %q:\n%v", want, err)
	}
}

// spec.force with a taken name is the server's update-in-place path: not a
// refusal, but a warning on stderr saying what will happen instead of a
// create. No same-family warning follows, since nothing new is created.
func TestApplyTableRuleCreate_ForcedTakenNameWarns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", api.DatasetRuleStateDisabled, "email_col"))

	path := env.CreateFile("forced.yaml", tableRuleInstantiateDoc("email_check")+"  force: true\n")
	_, stderr, err := execApplyRoot(t, "apply", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("a forced create is not refused, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	want := `table 1 already has a rule named "email_check" (` + testSiblingRuleID + `, state: disabled): the server updates that rule in place and tries to enable it, with no validation dry run, instead of creating a new rule; a create staged for review is instead renamed when it is accepted`
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "adds another instance") {
		t.Errorf("a forced update must not warn about another instance:\n%s", stderr)
	}
}

func TestApplyTableRuleCreate_ForcedOutcomeMatchesReturnedID(t *testing.T) {
	for _, tc := range []struct {
		name, returnedID, header string
		existing                 bool
	}{
		{"updated", testSiblingRuleID, "Table rule updated:", false},
		{"created after a concurrent rename", testTableRuleID, "Table rule created:", true},
	} {
		for _, format := range []string{"table", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				env, mock := setupTableRuleApplyEnv(t)
				defer env.Cleanup()
				defer mock.Close()
				var req api.InstantiateRuleRequestSchema
				registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
				registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", api.DatasetRuleStateDisabled, "email_col"))
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+instantiatePath, func(w http.ResponseWriter, _ *http.Request) {
					rule := sampleDatasetRuleDetailForApply()
					rule.ID = uuid.MustParse(tc.returnedID)
					rule.State = api.DatasetRuleStateEnabled
					testutil.RespondJSON(w, http.StatusCreated, rule)
				})
				path := env.CreateFile("forced.yaml", tableRuleInstantiateDoc("email_check")+"  force: true\n")
				stdout, stderr, err := execApplyRoot(t, "apply", "-f", path, "--yes", "--wait", "-o", format)
				requireExit(t, err, qerrors.ExitSuccess)
				human := stdout
				if format == "json" {
					human = stderr
					var result dataset_rules.DatasetRuleDetail
					if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.ID.String() != tc.returnedID {
						t.Fatalf("JSON result does not carry the returned rule: %s (%v)", stdout, err)
					}
				}
				if !strings.Contains(human, tc.header) {
					t.Errorf("output lacks %q:\n%s", tc.header, human)
				}
				if strings.Contains(human, "  existing    :") != tc.existing {
					t.Errorf("existing-row note mismatch:\n%s", human)
				}
				if !tc.existing && strings.Contains(human, "Table rule created:") {
					t.Errorf("a forced update claimed a creation:\n%s", human)
				}
				if !strings.Contains(stderr, "--wait: nothing to wait for:") {
					t.Errorf("--wait output lacks its explanation:\n%s", stderr)
				}
			})
		}
	}
}

func TestApplyTableRuleCreate_ForcedTakenNameActivationFailure(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", api.DatasetRuleStatePendingValidation, "email_col"))
	message := "Cannot enable DatasetRule " + testSiblingRuleID + ": produced field 'checked_email' is not yet present in the dataset schema — apply the draft migration first"
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+instantiatePath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
			"detail": map[string]interface{}{"msg": message},
		})
	})

	path := env.CreateFile("forced-pending.yaml", tableRuleInstantiateDoc("email_check")+"  force: true\n")
	stdout, stderr, err := execApplyRoot(t, "apply", "-f", path, "--yes")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2; error: %v", qerrors.GetExitCode(err), err)
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error missing activation failure: %v", err)
	}
	if !strings.Contains(stderr, "tries to enable it") {
		t.Errorf("warning missing conditional enable:\n%s", stderr)
	}
	if strings.Contains(stdout, "Table rule created") {
		t.Errorf("activation failure reported a create:\n%s", stdout)
	}
}

// A rule in the error state is not what spec.force can enable, so it gets no
// warning claiming otherwise.
func TestApplyTableRuleCreate_ForcedNameOfAnErroredRuleDoesNotClaimAnEnable(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", api.DatasetRuleStateError, "email_col"))

	path := env.CreateFile("forced-error.yaml", tableRuleInstantiateDoc("email_check")+"  force: true\n")
	_, stderr, err := execApplyRoot(t, "apply", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "tries to enable it") || strings.Contains(stderr, "enables it") {
		t.Errorf("stderr claims an enable for an errored rule:\n%s", stderr)
	}
}

// A nameless create repeating a live rule of the same family on the same
// columns is refused before the POST. Each case reaches the same final
// mapping a different way: written out, and a name or concept binding the
// server's preview predicts.
func TestApplyTableRuleCreate_NamelessRepeatExitsTwo(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		column   string
		revision func() api.RuleRevisionFullSchema
	}{
		"explicit mapping": {
			manifest: namelessCreateDoc(),
			column:   "email_col",
			revision: func() api.RuleRevisionFullSchema { return sampleRuleRevisionFull(nil) },
		},
		"server binding by name": {
			manifest: namelessIdentityDoc(),
			column:   "email",
			revision: func() api.RuleRevisionFullSchema {
				rev := sampleRuleRevisionFull(nil)
				rev.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{boundCandidate("email", "email", api.RuleFieldBindingSourceOriginalName)}
				return rev
			},
		},
		"concept fill": {
			manifest: namelessIdentityDoc(),
			column:   "customer_email",
			revision: func() api.RuleRevisionFullSchema {
				rev := sampleRuleRevisionFull(nil)
				rev.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{conceptCandidate("email", "contact-email", "customer_email")}
				return rev
			},
		},
		"severity written out": {
			manifest: namelessCreateDoc() + "  severity: as_declared\n",
			column:   "email_col",
			revision: func() api.RuleRevisionFullSchema { return sampleRuleRevisionFull(nil) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var req api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, tc.revision(), &req)
			registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on "+tc.column, api.DatasetRuleStateEnabled, tc.column))
			registerSiblingRuleDetail(mock, map[string]string{"email": tc.column})

			_, err := execApply(t, env.CreateFile("repeat.yaml", tc.manifest), "--yes")
			assertRefusedWithoutPost(t, mock, err)
			for _, want := range []string{
				"metadata.id: " + testSiblingRuleID,
				"spec.upgradedFrom: " + testSiblingRuleID,
				"set spec.instance_name",
				"email -> " + tc.column,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not contain %q:\n%v", want, err)
				}
			}
		})
	}
}

// A repeat's params are compared as JSON values, so the spelling YAML and the
// server each give a number does not make them differ: 1.0 in the manifest is
// the 1 the server sends back.
func TestApplyTableRuleCreate_NamelessRepeatWithParamsExitsTwo(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on email_col", api.DatasetRuleStateEnabled, "email_col"))
	registerSiblingRuleDetailWithParams(mock, map[string]string{"email": "email_col"}, map[string]interface{}{
		"threshold": 0.9, "max_length": 1, "domains": []string{"a.com"},
	})

	manifest := namelessCreateDoc() + "  params:\n    threshold: 0.9\n    max_length: 1.0\n    domains: [a.com]\n"
	_, err := execApply(t, env.CreateFile("repeat-params.yaml", manifest), "--yes")
	assertRefusedWithoutPost(t, mock, err)
	if want := "has the same rule revision, params, severity, row filter and column mapping (email -> email_col)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error does not contain %q:\n%v", want, err)
	}
}

// A nameless create that differs from a live instance on the same columns
// only in what it sets on the rule -- its params, its severity, or its
// revision -- is a second instance, not a repeat: it warns and goes through.
// The revision and severity are on the list row, so a mismatch there needs no
// detail read.
func TestApplyTableRuleCreate_SameColumnsOtherSettingsWarns(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest      string
		listed        func(*dataset_rules.DatasetRuleTiny)
		params        map[string]interface{}
		fetchesDetail bool
	}{
		"params differ": {
			manifest:      namelessCreateDoc() + "  params:\n    threshold: 0.1\n",
			params:        map[string]interface{}{"threshold": 0.9},
			fetchesDetail: true,
		},
		"params unset beside a rule with params": {
			manifest:      namelessCreateDoc(),
			params:        map[string]interface{}{"threshold": 0.9},
			fetchesDetail: true,
		},
		"severity differs": {
			manifest: namelessCreateDoc() + "  severity: blocker\n",
		},
		"default severity differs": {
			manifest: namelessCreateDoc(),
			listed:   func(r *dataset_rules.DatasetRuleTiny) { r.Severity = api.RuleSeverityWarning },
		},
		"revision differs": {
			manifest: namelessCreateDoc(),
			listed:   func(r *dataset_rules.DatasetRuleTiny) { r.RuleRevisionID = uuid.MustParse(testRebindRevID) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var req api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
			listed := listedTableRule(testSiblingRuleID, "Email validator on email_col", api.DatasetRuleStateEnabled, "email_col")
			if tc.listed != nil {
				tc.listed(&listed)
			}
			registerTableRulesList(mock, 1, listed)
			params := tc.params
			if params == nil {
				params = map[string]interface{}{}
			}
			registerSiblingRuleDetailWithParams(mock, map[string]string{"email": "email_col"}, params)

			_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("second.yaml", tc.manifest), "--yes")
			assertCreatedWithWarning(t, mock, stderr, err)
			if got := siblingDetailFetched(mock); got != tc.fetchesDetail {
				t.Errorf("detail fetched = %v, want %v: %v", got, tc.fetchesDetail, mock.Requests())
			}
		})
	}
}

// A rule family that maps no columns is the same on every table, so only its
// settings tell two instances apart: a second instance with other params goes
// through with the warning, and only an exact repeat is refused.
func TestApplyTableRuleCreate_NoColumnFamilyRefusesOnlyAnExactRepeat(t *testing.T) {
	for name, tc := range map[string]struct {
		params  string
		refused bool
	}{
		"same params":  {params: "    min_rows: 10\n", refused: true},
		"other params": {params: "    min_rows: 500\n"},
	} {
		t.Run(name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			revision := sampleRuleRevisionFull(nil)
			revision.InputColumns = []string{}
			var req api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, revision, &req)
			registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Row count", api.DatasetRuleStateEnabled))
			registerSiblingRuleDetailWithParams(mock, map[string]string{}, map[string]interface{}{"min_rows": 10})

			manifest := namelessIdentityDoc() + "  params:\n" + tc.params
			_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("row-count.yaml", manifest), "--yes")
			if !tc.refused {
				assertCreatedWithWarning(t, mock, stderr, err)
				return
			}
			assertRefusedWithoutPost(t, mock, err)
			if want := "column mapping (none)"; !strings.Contains(err.Error(), want) {
				t.Errorf("error does not contain %q:\n%v", want, err)
			}
		})
	}
}

// With no spec.severity and no default_severity on the revision, the severity
// the server will pick is unknown, so a create on a live instance's columns
// warns instead of refusing, and reads no detail to decide.
func TestApplyTableRuleCreate_UnknownSeverityWarnsInsteadOfRefusing(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	revision.DefaultSeverity = nil
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on email_col", api.DatasetRuleStateEnabled, "email_col"))
	registerSiblingRuleDetail(mock, map[string]string{"email": "email_col"})

	_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("unknown-severity.yaml", namelessCreateDoc()), "--yes")
	assertCreatedWithWarning(t, mock, stderr, err)
	if siblingDetailFetched(mock) {
		t.Errorf("fetched the detail of a rule it could not compare: %v", mock.Requests())
	}
}

// The same family on other columns is another instance, not a repeat: the
// create goes through with a warning naming the rule already there. The
// column sets differ, so the rule's detail is never fetched.
func TestApplyTableRuleCreate_SameFamilyOtherColumnsWarns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on backup_email", api.DatasetRuleStateEnabled, "backup_email"))

	_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("other-cols.yaml", namelessCreateDoc()), "--yes")
	if err != nil {
		t.Fatalf("another instance is not refused, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	for _, want := range []string{
		`adds another instance of rule family "email_validator" to table 1, which already has:`,
		`"Email validator on backup_email" (` + testSiblingRuleID + `, state: enabled) on backup_email`,
		"metadata.id",
		"spec.upgradedFrom",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if siblingDetailFetched(mock) {
		t.Errorf("fetched the detail of a rule whose columns already differ")
	}
}

// The same columns bound to different rule fields is a different check, so
// it warns rather than refuses.
func TestApplyTableRuleCreate_SameColumnsOnOtherFieldsWarns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	revision.InputColumns = []string{"left", "right"}
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Compare on a, b", api.DatasetRuleStateEnabled, "b", "a"))
	registerSiblingRuleDetail(mock, map[string]string{"left": "b", "right": "a"})

	manifest := namelessIdentityDoc() + "  column_mapping:\n    left: a\n    right: b\n"
	_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("swapped.yaml", manifest), "--yes")
	if err != nil {
		t.Fatalf("a swapped mapping is not a repeat, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	if !strings.Contains(stderr, "adds another instance") {
		t.Errorf("expected the same-family warning:\n%s", stderr)
	}
}

// spec.instance_name is the deliberate way to add a second instance on the
// same columns: no refusal, only the warning.
func TestApplyTableRuleCreate_NamedSecondInstanceOnSameColumnsWarns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on email_col", api.DatasetRuleStateEnabled, "email_col"))
	registerSiblingRuleDetail(mock, map[string]string{"email": "email_col"})

	manifest := namelessCreateDoc() + "  instance_name: strict email check\n"
	_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("named.yaml", manifest), "--yes")
	if err != nil {
		t.Fatalf("a named second instance is not refused, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	if !strings.Contains(stderr, "adds another instance") {
		t.Errorf("expected the same-family warning:\n%s", stderr)
	}
}

// The rule spec.upgradedFrom names is the one being replaced: it is neither a
// repeat nor worth a warning.
func TestApplyTableRuleCreate_UpgradedFromRuleIsNotADuplicate(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &req)
	registerTableRulesList(mock, 1, listedTableRule(testTableRuleID, "Email validator on email", api.DatasetRuleStateEnabled, "email"))

	_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("upgrade.yaml", upgradeManifest(testTableRuleID)), "--yes")
	if err != nil {
		t.Fatalf("an upgrade of the same rule is not a repeat, got: %v", err)
	}
	if n := postCount(mock); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
	if strings.Contains(stderr, "Warning:") {
		t.Errorf("an upgrade must not warn about its own predecessor:\n%s", stderr)
	}
}

// A deleted or validation_failed rule is not an instance on the table:
// re-creating after a failed validation is the retry, so neither refuses nor
// warns.
func TestApplyTableRuleCreate_RetiredInstancesDoNotCount(t *testing.T) {
	for _, state := range []api.DatasetRuleState{api.DatasetRuleStateValidationFailed, api.DatasetRuleStateDeleted} {
		t.Run(string(state), func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var req api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
			registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on email_col", state, "email_col"))
			registerSiblingRuleDetail(mock, map[string]string{"email": "email_col"})

			_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("retry.yaml", namelessCreateDoc()), "--yes")
			if err != nil {
				t.Fatalf("a %s rule must not block the create, got: %v", state, err)
			}
			if n := postCount(mock); n != 1 {
				t.Errorf("POSTs = %d, want 1", n)
			}
			if strings.Contains(stderr, "Warning:") {
				t.Errorf("a %s rule must not draw a warning:\n%s", state, stderr)
			}
		})
	}
}

// A failed list is returned with its exit code intact, and nothing is
// created.
func TestApplyTableRuleCreate_ListFailureKeepsItsExitCode(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	mock.RegisterHandler("GET", tableRulesListPath(1), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "no access to this table")
	})

	_, err := execApply(t, env.CreateFile("list-fails.yaml", namelessCreateDoc()), "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitUnauthorized, err)
	}
	if n := postCount(mock); n != 0 {
		t.Errorf("a failed check sent %d POSTs", n)
	}
}

// A failed detail fetch during the column comparison is returned with its
// exit code intact, and nothing is created.
func TestApplyTableRuleCreate_DetailFailureKeepsItsExitCode(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "Email validator on email_col", api.DatasetRuleStateEnabled, "email_col"))
	mock.RegisterHandler("GET", siblingDetailPath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "gone")
	})

	_, err := execApply(t, env.CreateFile("detail-fails.yaml", namelessCreateDoc()), "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitNotFound, err)
	}
	if n := postCount(mock); n != 0 {
		t.Errorf("a failed check sent %d POSTs", n)
	}
}
