package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	rowDescribeOrgID  = "c3d4e5f6-a7b8-9012-cdef-345678901234"
	rowDescribeRuleA  = "0190aaaa-1111-2222-3333-444444444444"
	rowDescribeRuleB  = "0190aaaa-5555-6666-7777-888888888888"
	rowDescribeRevA   = "0190eeee-1111-2222-3333-444444444444"
	rowDescribeMfstID = "0190ffff-1111-2222-3333-444444444444"
	rowDescribeTable  = "orders"
	rowDescribeTblID  = 42
)

func setupRowDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewRowCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func newRowDescribeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", rowDescribeOrgID)
	env.SetupCredential(endpointKey, rowDescribeOrgID, "test-token")
	return mock
}

// rowRegisterTableResolve answers the table lookup --table makes. Every test
// here resolves the one table the fixtures use, so it takes no arguments.
func rowRegisterTableResolve(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+rowDescribeOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":`+strconv.Itoa(rowDescribeTblID)+`,"name":"`+rowDescribeTable+`","state":"active"}]}`))
	})
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// sampleEvidence is one row with one ingestion run: a blocker, a passed rule
// the default view summarises, and one token no manifest entry claimed.
func sampleEvidence() api.RowRuleEvidenceSchema {
	evaluated := time.Now().Add(-2 * time.Hour)
	return api.RowRuleEvidenceSchema{
		DatasetID:        42,
		RowID:            1042,
		InitialSignature: strPtr("3f9a1c2b"),
		Results: []api.RuleRunEvidenceSchema{{
			RuleRunID:        118,
			RowID:            1042,
			InitialSignature: "3f9a1c2b",
			EvaluatedAt:      evaluated,
			StartedAt:        evaluated.Add(-time.Minute),
			RunKind:          api.RuleRunKindIngestion,
			IngestionJobID:   intPtr(900),
			ManifestID:       uuid.MustParse(rowDescribeMfstID),
			ManifestSha256:   "4b2c8f",
			Verdicts: []api.RuleVerdictEvidenceSchema{
				{
					Position:          0,
					DatasetRuleID:     rowDescribeRuleA,
					InstanceName:      "email_check",
					FamilySlug:        "email_format",
					RuleKind:          "validation",
					Release:           "1.2.0",
					RuleRevisionID:    rowDescribeRevA,
					Verdict:           strPtr("blocker"),
					VerdictCode:       intPtr(3),
					AlertItemTokens:   &[]string{"8817-email"},
					WarningItemTokens: &[]string{},
				},
				{
					Position:       1,
					DatasetRuleID:  rowDescribeRuleB,
					InstanceName:   "phone_check",
					FamilySlug:     "phone_format",
					RuleKind:       "validation",
					Release:        "1.0.0",
					RuleRevisionID: "0190eeee-9999-2222-3333-444444444444",
					Verdict:        strPtr("passed"),
					VerdictCode:    intPtr(1),
				},
			},
			UnattributedAlertTokens: &[]string{"99-"},
		}},
	}
}

func registerRowEvidence(mock *testutil.MockAPIServer, body interface{}) {
	mock.RegisterHandler("GET", "/api/orgs/"+rowDescribeOrgID+"/datasets/42/rows/1042/rule-evidence",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, body)
		})
}

func runDescribeRow(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupRowDescribeCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"describe", "row"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// The default payload names the run, the verdict that quarantined the row, the
// rule by its r-* alias and the revision by its short ID.
func TestDescribeRowRendersTheEvidencePayload(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, sampleEvidence())

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{
		"# ROW 1042 RULE EVIDENCE",
		"Table: 42",
		"Initial signature: 3f9a1c2b",
		"Runs: 1 (newest first)",
		"## RUN 118 — ingestion, evaluated ",
		"Jobs: ingestion 900",
		"Manifest: " + rowDescribeMfstID + " (sha256 4b2c8f)",
		"Verdicts: 2 configured rules — 1 blocker, 1 passed",
		"| r-1 | email_check | email_format | 0190eeee | blocker | [A8817] email |",
		"1 rule(s) with no effect on this row are not listed",
		"Unattributed issues (no manifest entry claimed them): [A99] whole-row",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q:\n%s", want, out)
		}
	}
	// The rule that passed is summarised, not listed.
	if strings.Contains(out, "phone_check") {
		t.Errorf("default view listed a passed rule:\n%s", out)
	}
}

// The evaluated_at line carries an absolute timestamp, not only "2 hours ago":
// this view answers an audit question and a relative time is not a date.
func TestDescribeRowPrintsAbsoluteTimestamps(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	ev := sampleEvidence()
	ev.Results[0].EvaluatedAt = time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	registerRowEvidence(mock, ev)

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2026-09-08T10:00:00Z") {
		t.Errorf("expected an absolute evaluated_at:\n%s", out)
	}
}

// -v lists every manifest entry, including the ones the default view counts.
func TestDescribeRowVerboseListsEveryManifestEntry(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, sampleEvidence())

	out, err := runDescribeRow(t, "1042", "--table", "orders", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "phone_check") {
		t.Errorf("-v did not list the passed rule:\n%s", out)
	}
	if strings.Contains(out, "not listed") {
		t.Errorf("-v still printed the hidden-rule count:\n%s", out)
	}
}

// A row the server has no evidence for is a real answer, not an error: the
// dataset may have no verdicts table at all. Say so rather than printing a
// header and nothing, which would read as a truncated payload.
func TestDescribeRowWithNoEvidenceSaysSo(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, api.RowRuleEvidenceSchema{DatasetID: 42, RowID: 1042})

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No rule run has recorded a verdict for this row.") {
		t.Errorf("expected the empty-evidence line:\n%s", out)
	}
	if !strings.Contains(out, "Runs: 0") {
		t.Errorf("expected a zero run count:\n%s", out)
	}
}

// A run recorded under a different row id evaluated an earlier incarnation of
// this row (a composite recompute re-keys a row but keeps its signature). Name
// the old id rather than folding it into the new one.
func TestDescribeRowNamesAPriorRowID(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	ev := sampleEvidence()
	ev.Results[0].RowID = 77
	registerRowEvidence(mock, ev)

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Recorded under row id 77") {
		t.Errorf("expected the prior-id line:\n%s", out)
	}
}

// The two "no verdict name" cases are different failures and must not collapse
// into one another: an entry the stored vector was too short to describe is
// unrecorded, while a code this build has no name for keeps its number.
func TestDescribeRowDistinguishesUnrecordedFromUnnamedVerdict(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	ev := sampleEvidence()
	ev.Results[0].Verdicts[0].Verdict = nil
	ev.Results[0].Verdicts[0].VerdictCode = nil
	ev.Results[0].Verdicts[1].Verdict = nil
	ev.Results[0].Verdicts[1].VerdictCode = intPtr(9)
	registerRowEvidence(mock, ev)

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "| unrecorded |") {
		t.Errorf("expected an unrecorded verdict:\n%s", out)
	}
	if !strings.Contains(out, "| unnamed code 9 |") {
		t.Errorf("expected the unnamed code to keep its number:\n%s", out)
	}
	// An unrecognised verdict is LISTED, never summarised away.
	if strings.Contains(out, "not listed") {
		t.Errorf("an unnamed verdict was summarised instead of listed:\n%s", out)
	}
}

// A suppression names the row-rule ignore that dropped what the rule raised.
func TestDescribeRowNamesTheSuppression(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	ev := sampleEvidence()
	ev.Results[0].Verdicts[0].Verdict = strPtr("suppressed")
	ev.Results[0].Verdicts[0].SuppressionHistoryID = intPtr(77)
	registerRowEvidence(mock, ev)

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "suppressed (ignore 77)") {
		t.Errorf("expected the suppression id:\n%s", out)
	}
}

// A token qctl cannot parse keeps its raw text under [A?] rather than
// disappearing, exactly as the get data Issues column does.
func TestDescribeRowKeepsAMalformedToken(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	ev := sampleEvidence()
	ev.Results[0].Verdicts[0].AlertItemTokens = &[]string{"not-an-id"}
	registerRowEvidence(mock, ev)

	out, err := runDescribeRow(t, "1042", "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[A?] not-an-id") {
		t.Errorf("expected the raw token:\n%s", out)
	}
}

// The same dataset rule keeps the same alias across invocations — that is the
// whole point of sharing get data's store.
func TestDescribeRowAliasesAreStable(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, sampleEvidence())

	first, err := runDescribeRow(t, "1042", "--table", "orders", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, first)
	}
	second, err := runDescribeRow(t, "1042", "--table", "orders", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, second)
	}
	if !strings.Contains(first, "| r-1 | email_check") || !strings.Contains(second, "| r-1 | email_check") {
		t.Errorf("alias moved between runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(second, "| r-2 | phone_check") {
		t.Errorf("expected the second rule to keep its own alias:\n%s", second)
	}
}

// -o json is the raw backend response, wire keys and all.
func TestDescribeRowJSONIsTheRawResponse(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, sampleEvidence())

	out, err := runDescribeRow(t, "1042", "--table", "orders", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"dataset_id", "row_id", "initial_signature", "results"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("raw response missing %q:\n%s", key, out)
		}
	}
	if strings.Contains(out, "ROW 1042 RULE EVIDENCE") {
		t.Errorf("-o json rendered the payload:\n%s", out)
	}
}

// -o yaml keeps the backend's snake_case keys, not yaml.v3's lowercased Go
// field names.
func TestDescribeRowYAMLKeepsWireKeys(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	registerRowEvidence(mock, sampleEvidence())

	out, err := runDescribeRow(t, "1042", "--table", "orders", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{"dataset_id:", "row_id:", "rule_run_id:", "dataset_rule_id:"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing wire key %q:\n%s", want, out)
		}
	}
}

// A row id the table does not hold is not found (exit 3), and the code must
// ride the Unwrap chain rather than degrade to a generic failure.
func TestDescribeRowNotFoundExitsThree(t *testing.T) {
	mock := newRowDescribeEnv(t)
	rowRegisterTableResolve(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+rowDescribeOrgID+"/datasets/42/rows/1042/rule-evidence",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound,
				json.RawMessage(`{"detail":"Row id 1042 not found in this table."}`))
		})

	_, err := runDescribeRow(t, "1042", "--table", "orders")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("expected exit %d, got %d (%v)", qerrors.ExitNotFound, code, err)
	}
}

// The table is required — the route is dataset-scoped — and omitting it is bad
// usage, not a failed request.
func TestDescribeRowWithoutATableExitsTwo(t *testing.T) {
	newRowDescribeEnv(t)

	_, err := runDescribeRow(t, "1042")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("expected exit %d, got %d (%v)", qerrors.ExitBadUsage, code, err)
	}
}

// A non-numeric row id is decidable locally and is refused before anything
// touches the network — including the table lookup --table would have cost.
func TestDescribeRowRejectsANonNumericIDBeforeAnyRequest(t *testing.T) {
	mock := newRowDescribeEnv(t)
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+rowDescribeOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":0,"page":1,"results":[]}`))
	})

	_, err := runDescribeRow(t, "not-a-row", "--table", "orders")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("expected exit %d, got %d (%v)", qerrors.ExitBadUsage, code, err)
	}
	if calls != 0 {
		t.Errorf("a bad row id cost %d request(s)", calls)
	}
}
