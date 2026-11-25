package diff

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrgID       = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testTableRuleID = "550e8400-e29b-41d4-a716-446655440000"
)

func setupDiffRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newDiffTestEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return env, mock
}

func sampleDetail() dataset_rules.DatasetRuleDetail {
	return dataset_rules.DatasetRuleDetail{
		ID:             uuid.MustParse(testTableRuleID),
		InstanceName:   "email_check",
		DatasetID:      1,
		OrganizationID: uuid.MustParse(testOrgID),
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		CreatedAt:      time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC),
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:      uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
			Name:    "email_validator",
			Slug:    "email_validator",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func registerDetailGet(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleDetail())
		})
}

func execDiff(t *testing.T, manifestPath string) (string, string, error) {
	t.Helper()
	rootCmd := setupDiffRoot()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"diff", "-f", manifestPath})
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// The guide teaches scripts which manifests can be previewed. Deriving the
// exact sentence from diffKinds makes an added, removed, or renamed dispatcher
// entry fail here until the shipped guide changes with it.
func TestDiffSupportedKindsStayInOutputAndScriptingGuide(t *testing.T) {
	body, err := guide.TopicBody("output-and-scripting")
	if err != nil {
		t.Fatalf("load output-and-scripting guide: %v", err)
	}
	_, preview, ok := strings.Cut(body, "## Preview before writing")
	if !ok {
		t.Fatal("output-and-scripting guide has no Preview before writing section")
	}
	preview, _, _ = strings.Cut(preview, "\n## ")

	kinds := diffKindNames()
	quotedKinds := make([]string, len(kinds))
	for i, kind := range kinds {
		quotedKinds[i] = "`" + kind + "`"
	}
	want := "It currently supports exactly these manifest kinds from " +
		"`diff.diffKinds`: " + strings.Join(quotedKinds, ", ") + "."
	flatPreview := strings.Join(strings.Fields(preview), " ")
	if !strings.Contains(flatPreview, want) {
		t.Errorf("Preview before writing must name exactly the supported diff kinds\nwant: %s\ngot:\n%s", want, preview)
	}
}

func TestDiffTableRule_NoDifferences(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: email_check
`
	manifestPath := env.CreateFile("no-change.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)
	if err != nil {
		t.Fatalf("no differences must exit 0, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("no differences must print nothing, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestDiffTableRule_Differences(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerParamsRevisionDetails(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("change.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)

	if err == nil {
		t.Fatal("differences must return the silent exit-1 sentinel")
	}
	if err.Error() != "" {
		t.Errorf("sentinel must be silent, got message %q", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty, got %q", stderr)
	}
	for _, want := range []string{
		`instance_name    : "email_check" -> "renamed"`,
		`params           : {"threshold":0.9} -> {"threshold":0.95}`,
		"merge patch body:",
		`{"instance_name":"renamed","params":{"threshold":0.95}}`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n---\n%s", want, stdout)
		}
	}
}

// TestDiffTableRule_NoOpKeyOmittedFromDiffButKeptInBody locks the deliberate
// divergence between the diff display and the merge-patch body: state is set
// to its current live value (a no-op) while params changes. The field-diff
// region must omit the no-op state line, but the merge patch body printed
// below must still include state. Not instance_name: a restated name is
// dropped from the body as well, which TestDiffTableRule_RestatedNameLeavesTheBody pins.
func TestDiffTableRule_NoOpKeyOmittedFromDiffButKeptInBody(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerParamsRevisionDetails(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  state: enabled
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("no-op-plus-change.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)

	if err == nil {
		t.Fatal("differences must return the silent exit-1 sentinel")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty, got %q", stderr)
	}
	if !strings.Contains(stdout, `params           : {"threshold":0.9} -> {"threshold":0.95}`) {
		t.Errorf("stdout missing changed params line\n---\n%s", stdout)
	}
	if strings.Contains(stdout, `"enabled" -> "enabled"`) {
		t.Errorf("no-op state diff line must not render\n---\n%s", stdout)
	}
	if !strings.Contains(stdout, "merge patch body:") {
		t.Errorf("stdout missing merge patch body marker\n---\n%s", stdout)
	}
	if !strings.Contains(stdout, `{"params":{"threshold":0.95},"state":"enabled"}`) {
		t.Errorf("merge patch body must still include the no-op state key\n---\n%s", stdout)
	}
}

func TestDiffTableRule_InstantiateModeRejected(t *testing.T) {
	env, _ := newDiffTestEnv(t)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 1
  rule_revision_id: aaaa0000-bbbb-cccc-dddd-eeeeeeee0001
`
	manifestPath := env.CreateFile("instantiate.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for instantiate-mode manifest")
	}
	if !strings.Contains(err.Error(), "diff is not yet supported for TableRule/instantiate") {
		t.Errorf("wrong error: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (bad usage)", got)
	}
}

// A spec.rule-only manifest (no rule_revision_id) is still instantiate mode
// (dataset_rules.TableRuleApplyManifest.IsInstantiate widened in Task 7 to
// recognize spec.rule) and must be rejected the same way, with the same
// message: diff never learned a second identifier for "this is instantiate
// mode".
func TestDiffTableRule_SpecRuleOnlyModeRejected(t *testing.T) {
	env, _ := newDiffTestEnv(t)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 1
  rule: email_validator
`
	manifestPath := env.CreateFile("spec-rule-only.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for a spec.rule-only (instantiate-mode) manifest")
	}
	if !strings.Contains(err.Error(), "diff is not yet supported for TableRule/instantiate") {
		t.Errorf("wrong error: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (bad usage)", got)
	}
}

func TestDiffUnsupportedKind(t *testing.T) {
	env, _ := newDiffTestEnv(t)

	manifest := `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: orders
spec:
  host: example.com
`
	manifestPath := env.CreateFile("destination.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
	if !strings.Contains(err.Error(), `diff is not yet supported for kind "Destination"`) {
		t.Errorf("wrong error: %v", err)
	}
	// The supported list is rendered from diffKinds, and "in patch mode" is a
	// TableRule qualifier only -- TableSchema has no instantiate mode to be
	// distinguished from. Pinned so the suffix cannot silently re-generalise
	// over a kind it is not true of.
	if !strings.Contains(err.Error(), "(supported: Deliverable, LinkedTable, StandardField, Table, TableKind, TableRule, TableRuleTemplate, TableSchema, TableSeries; TableRule in patch mode only)") {
		t.Errorf("wrong supported-kinds suffix: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (bad usage)", got)
	}
}

func TestDiffTableRule_NotFound(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		testutil.MockNotFoundHandler("table rule not found"))

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed
`
	manifestPath := env.CreateFile("missing.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if got := qerrors.GetExitCode(err); got != 3 {
		t.Errorf("exit code = %d, want 3 (not found)", got)
	}
}

// A manifest diff cannot parse is the user's file being wrong. Leaving it at 1
// makes it indistinguishable from ErrDiffDifferences -- "there are changes,
// go ahead and apply" -- which is the exact footgun `guide output-and-scripting`
// warns about.
func TestDiffRejectsMalformedManifestsWithExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "YAML syntax error",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  name: [unclosed\n",
			want:     "failed to parse manifest:",
		},
		{
			name:     "missing kind",
			manifest: "apiVersion: qluster.ai/v1\nmetadata:\n  name: x\n",
			want:     "failed to parse manifest:",
		},
		{
			name:     "wrong apiVersion on a TableRule",
			manifest: "apiVersion: v2-bad\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n  dataset_id: 1\n  state: enabled\n",
			want:     `unsupported apiVersion "v2-bad"`,
		},
		{
			name:     "patch with nothing patchable",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n  dataset_id: 1\n",
			want:     "no patchable fields found in",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := newDiffTestEnv(t)
			path := env.CreateFile("manifest.yaml", tc.manifest)
			_, _, err := execDiff(t, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The negative control. Without it, a later change that gave every diff failure
// ExitBadUsage would pass the test above and erase the distinction.
func TestDiffUnreadableFileStillExitsOne(t *testing.T) {
	// Called for the isolated $HOME alone -- genericDiff reads the file before
	// cmdutil.Bootstrap, so neither return value is needed here.
	newDiffTestEnv(t)

	_, _, err := execDiff(t, filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected an error")
	}
	// An I/O failure is not a malformed manifest.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

const testRebindRevID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"

// registerRuleRevisionList answers the rule-revision search resolution uses,
// with a single revision so strict release matching is unambiguous.
func registerRuleRevisionList(mock *testutil.MockAPIServer, id, release string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{
					"id": id, "name": "email_validator",
					"slug": "email_validator", "release": release, "state": "enabled",
				}},
			})
		})
}

// diff previews a rebind named by spec.rule + spec.release the same way apply
// applies it: the pair resolves to a revision UUID before the patch body is
// built, so the preview shows the rebind instead of claiming the file has no
// patchable fields.
func TestDiffTableRule_PreviewsRebindByRuleAndRelease(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerRuleRevisionList(mock, testRebindRevID, "2.1.0")

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule: email_validator
  release: "2.1.0"
`
	manifestPath := env.CreateFile("rebind-by-name.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)

	if err == nil {
		t.Fatal("a rebind is a difference and must return the silent exit-1 sentinel")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (err: %v)", got, err)
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty, got %q", stderr)
	}
	for _, want := range []string{
		`rule_revision_id : "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001" -> "` + testRebindRevID + `"`,
		"merge patch body:",
		`{"rule_revision_id":"` + testRebindRevID + `"}`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n---\n%s", want, stdout)
		}
	}
}

// Restating the revision the rule already runs is not a rebind, even when the
// manifest spells it as a slug: the identifier resolves before comparison, so
// diff reports no difference rather than a fabricated one.
func TestDiffTableRule_RebindToCurrentRevisionIsNoDifference(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerRuleRevisionList(mock, "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "1.0.0")

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: email_validator
`
	manifestPath := env.CreateFile("rebind-noop.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)
	if err != nil {
		t.Fatalf("rebinding to the current revision must exit 0, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("no differences must print nothing, got stdout=%q stderr=%q", stdout, stderr)
	}
}

// spec.release without spec.rule is the same pairing error apply gives -- not
// "no patchable fields found". Both candidate errors exit 2, so the exit code
// alone cannot tell them apart; the message is the behavior under test.
func TestDiffTableRule_OrphanReleaseIsPairingError(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  release: "2.1.0"
`
	manifestPath := env.CreateFile("orphan-release.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("spec.release without spec.rule must be rejected")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.release needs spec.rule") {
		t.Errorf("expected the release-needs-rule pairing error, got: %v", err)
	}
}

// TestGenericDiff_MultiDocumentFileIsRefused pins the guard that closes the
// gap between the two verbs: pkgmanifest.StrictUnmarshal decodes ONE document,
// so before the guard `diff -f` previewed document 1 of a five-document file
// while `apply -f` on the same file applied all five.
//
// Both kinds are covered because the refusal lives in genericDiff, above the
// dispatch table -- a per-kind fix would have left the other kind exposed.
//
// The assertions are on qerrors.GetExitCode, not on the message: a message
// substring matches just as happily when the error carries no exit code at
// all, which is the failure mode the repo's Unwrap contract exists to stop.
func TestGenericDiff_MultiDocumentFileIsRefused(t *testing.T) {
	tableRuleDoc := `apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: email_check
`
	tableSchemaDoc := `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
`
	// A document AFTER THE FIRST that will not parse is the case the guard's
	// first form missed: SplitDocuments fails, so a count-only guard is skipped,
	// and pkgmanifest.LoadBytes then succeeds because it decodes exactly one
	// document -- leaving diff confidently previewing document 1 of a file
	// `apply -f` refuses outright. Both kinds carry the case, because the guard
	// sits above the dispatch table.
	const brokenSecondDoc = "apiVersion: qluster.ai/v1\nkind: TableSchema\n  metadata: broken: here\n"

	cases := []struct{ name, body string }{
		{"TableRule", tableRuleDoc + "---\n" + tableRuleDoc},
		{"TableSchema", tableSchemaDoc + "---\n" + tableSchemaDoc},
		{"mixed kinds", tableSchemaDoc + "---\n" + tableRuleDoc},
		{"second document will not parse", tableSchemaDoc + "---\n" + brokenSecondDoc},
		{"second document will not parse, TableRule first", tableRuleDoc + "---\n" + brokenSecondDoc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			registerDetailGet(mock)
			registerSchemaDetail(mock)
			stdout, _, err := execDiff(t, env.CreateFile("multi.yaml", tc.body))
			if err == nil {
				t.Fatal("a multi-document file must be refused, not previewed")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("a refused file must print no preview; got %q", stdout)
			}
		})
	}
}

// A trailing "---" and a comment-only document are not extra documents. The
// count comes from pkgmanifest.SplitDocuments, the very function apply splits
// by, so the two verbs cannot disagree about how many documents a file holds.
func TestGenericDiff_EmptyTrailingDocumentIsStillOneDocument(t *testing.T) {
	_, mock := newDiffTestEnv(t)
	registerSchemaDetail(mock)
	body := `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    currency:
      type: String
      args: [3]
      is_nullable: false
---
# nothing here
---
`
	stdout, stderr, err := execDiff(t, writeSchemaManifest(t, body))
	if err != nil {
		t.Fatalf("one real document plus empty ones must still preview; got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("this manifest matches live, so exit 0 must be silent; stdout=%q stderr=%q", stdout, stderr)
	}
}

// enrichingRevisionResponse is a revision that writes one new column and
// declares its schema, so a manifest may omit field_schema.
func enrichingRevisionResponse() api.RuleRevisionFullSchema {
	schemas := map[string]map[string]interface{}{
		"first_name": {"type": "String"},
	}
	return api.RuleRevisionFullSchema{
		ID:                   uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Name:                 "split_name",
		Slug:                 "split_name",
		Release:              "1.0.0",
		State:                "enabled",
		InputColumns:         []string{"full_name"},
		EnrichesColumns:      []string{"first_name"},
		EnrichedFieldSchemas: &schemas,
	}
}

func registerRevisionDetails(mock *testutil.MockAPIServer, rev api.RuleRevisionFullSchema) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String()+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, rev)
		})
}

func registerParamsRevisionDetails(mock *testutil.MockAPIServer) {
	rev := enrichingRevisionResponse()
	rev.ParamSchema = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	registerRevisionDetails(mock, rev)
}

// Params are a pre-request verdict for apply, so diff must reach the same
// verdict before it prints a body. Cover both reasons the revision may be in
// hand: params alone, and params sharing the fetch produced_fields already
// needed before this regression was fixed.
func TestDiffTableRule_InvalidParamsAreRefused(t *testing.T) {
	cases := []struct {
		name           string
		producedFields string
	}{
		{name: "params only"},
		{
			name: "params and produced fields",
			producedFields: `
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			registerDetailGet(mock)

			rev := enrichingRevisionResponse()
			rev.ParamSchema = map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"threshold": map[string]interface{}{"type": "number"},
				},
				"required": []interface{}{"threshold"},
			}
			revisionGets := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String()+"/details",
				func(w http.ResponseWriter, _ *http.Request) {
					revisionGets++
					testutil.RespondJSON(w, http.StatusOK, rev)
				})

			manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    threshold: not-a-number
` + tc.producedFields
			stdout, stderr, err := execDiff(t, env.CreateFile("invalid-params.yaml", manifest))
			if err == nil {
				t.Fatal("invalid params must be refused")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "params validation failed") {
				t.Errorf("error must identify params validation, got: %v", err)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("a refused manifest must print no preview, got stdout=%q stderr=%q", stdout, stderr)
			}
			if revisionGets != 1 {
				t.Errorf("rule revision detail requests = %d, want 1", revisionGets)
			}
		})
	}
}

// A rebind's params describe the target revision, not the revision currently
// attached to the table. Only the target endpoint is registered: fetching the
// live revision would either fail the command for the wrong reason or let the
// request count below expose the mistake.
func TestDiffTableRule_RebindValidatesParamsAgainstTargetRevision(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)

	target := enrichingRevisionResponse()
	target.ID = uuid.MustParse(testRebindRevID)
	target.ParamSchema = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	targetGets := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRebindRevID+"/details",
		func(w http.ResponseWriter, _ *http.Request) {
			targetGets++
			testutil.RespondJSON(w, http.StatusOK, target)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: ` + testRebindRevID + `
  params:
    threshold: not-a-number
`
	stdout, _, err := execDiff(t, env.CreateFile("invalid-rebind-params.yaml", manifest))
	if err == nil {
		t.Fatal("params invalid for the rebind target must be refused")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "params validation failed") {
		t.Errorf("error must identify params validation, got: %v", err)
	}
	if stdout != "" {
		t.Errorf("a refused manifest must not be previewed\n---\n%s", stdout)
	}
	if targetGets != 1 {
		t.Errorf("target revision detail requests = %d, want 1", targetGets)
	}
}

// The preview shows the resolved body, not the manifest: field_schema is
// omitted in the file and filled from the revision, so the merge patch body
// printed here is byte-for-byte what apply would send.
func TestDiffTableRule_ProducedFieldsResolveTheirSchema(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerRevisionDetails(mock, enrichingRevisionResponse())

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`
	manifestPath := env.CreateFile("produced.yaml", manifest)
	stdout, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("declaring a column the rule does not have is a difference")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	want := `{"produced_fields":[{"rule_field_name":"first_name","dataset_field_name":"customer_first","field_schema":{"type":"String"}}]}`
	if !strings.Contains(stdout, want) {
		t.Errorf("merge patch body missing the resolved field_schema\nwant %s\n---\n%s", want, stdout)
	}
	if !strings.Contains(stdout, "produced_fields  : [] ->") {
		t.Errorf("a rule with no declarations must render as [], not null\n---\n%s", stdout)
	}
}

// Restating what the rule already declares is not a change: the live rows
// carry an id, timestamps and a generated_by that the manifest cannot express,
// and comparing those would make every unedited round-trip a difference.
func TestDiffTableRule_RestatedProducedFieldsAreNoDifference(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerRevisionDetails(mock, enrichingRevisionResponse())
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			detail := sampleDetail()
			detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
				ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
				RuleFieldName:    "first_name",
				DatasetFieldName: "customer_first",
				FieldSchema: map[string]interface{}{
					"type":         "String",
					"generated_by": map[string]interface{}{"type": "rule_output"},
				},
				CreatedAt: time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
			}}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
      field_schema:
        type: String
`
	manifestPath := env.CreateFile("restated.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)
	if err != nil {
		t.Fatalf("restating the live declarations must exit 0, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("no differences must print nothing, got stdout=%q stderr=%q", stdout, stderr)
	}
}

// Clearing a rule that declares nothing is the same no-op, and it must not
// cost a rule-revision fetch: none is registered here.
func TestDiffTableRule_ClearingNothingIsNoDifference(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("clear-nothing.yaml", manifest)
	stdout, stderr, err := execDiff(t, manifestPath)
	if err != nil {
		t.Fatalf("clearing an empty declaration set must exit 0, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("no differences must print nothing, got stdout=%q stderr=%q", stdout, stderr)
	}
}

// The clear against an enabled rule that HAS declarations is refused, with the
// same exit code apply gives it -- a preview that showed a body apply will not
// send is worse than no preview, and this body is one the BACKEND accepts and
// then cannot promote.
//
// This test used to assert the opposite: that the body carries `[]`, the
// sentinel telling the backend to drop the declarations. The sentinel is real
// and does reach the live rule for a rule with nothing to drop, but a drop the
// rule actually carries strands the candidate at promotion instead of landing.
func TestDiffTableRule_ClearingEnabledLiveDeclarationsIsRefused(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			detail := sampleDetail()
			detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
				ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
				RuleFieldName:    "first_name",
				DatasetFieldName: "customer_first",
				FieldSchema:      map[string]interface{}{"type": "String"},
			}}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("clear.yaml", manifest)
	stdout, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected clearing live declarations to be refused")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the same refusal apply gives)", got, qerrors.ExitBadUsage)
	}
	if strings.Contains(stdout, `"produced_fields"`) {
		t.Errorf("a refused manifest must not have its body previewed\n---\n%s", stdout)
	}
}

// Diff reads the same live state and runs the same reconcile as apply. For a
// paused rule it previews the [] sentinel apply will send to Atlas's direct
// replacement path.
func TestDiffTableRule_PausedRulePreviewsExplicitProducedFieldsClear(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			detail := sampleDetail()
			detail.State = api.DatasetRuleStateDisabled
			detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
				ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
				RuleFieldName:    "first_name",
				DatasetFieldName: "customer_first",
				FieldSchema:      map[string]interface{}{"type": "String"},
			}}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("paused-clear.yaml", manifest)
	stdout, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("clearing a paused rule's live declarations must be a difference")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if !strings.Contains(stdout, `"produced_fields":[]`) {
		t.Errorf("merge patch body must preserve the explicit clear\n---\n%s", stdout)
	}
	if !strings.Contains(stdout, "produced_fields  :") || !strings.Contains(stdout, "-> []") {
		t.Errorf("field-level preview must show the declarations being cleared\n---\n%s", stdout)
	}
}

// A manifest qctl would refuse to apply must be refused by the preview too,
// with the same exit code: a diff that exits 0 on a body apply rejects is
// worse than no preview.
func TestDiffTableRule_RejectsAnUnknownProducedField(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerRevisionDetails(mock, enrichingRevisionResponse())

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: middle_name
      dataset_field_name: customer_middle
      field_schema:
        type: String
`
	manifestPath := env.CreateFile("unknown-produced.yaml", manifest)
	_, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected the unknown rule output field to be rejected")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "is not one of the rule revision's output fields") {
		t.Errorf("unexpected error: %v", err)
	}
}

// `spec.state: error` is refused by apply, so the preview refuses it too and
// with the same exit code. It is not a hand-written value: `qctl get table-rule
// -o yaml` writes the rule's current state into every manifest it renders, so
// a crashed rule's round-trip carries it, and previewing a rebind that apply
// will then refuse is the worst of both.
//
// No detail GET is registered: the refusal reads the manifest alone, so
// reaching the server at all would fail this test.
func TestDiffTableRule_SystemSetStateErrorIsRefused(t *testing.T) {
	env, _ := newDiffTestEnv(t)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  state: error
  instance_name: renamed
`
	manifestPath := env.CreateFile("state-error.yaml", manifest)
	stdout, _, err := execDiff(t, manifestPath)
	if err == nil {
		t.Fatal("expected spec.state: error to be refused by the preview")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the same refusal apply gives)", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.state: error") {
		t.Errorf("the error must name the key to delete, got: %v", err)
	}
	if stdout != "" {
		t.Errorf("a refused manifest must not be previewed\n---\n%s", stdout)
	}
}
