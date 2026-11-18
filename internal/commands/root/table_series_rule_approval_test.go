package root

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const (
	ruleApprovalPath    = seriesTestPath + "/" + seriesTestID + "/rule-set-approval"
	ruleFingerprint     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	approvedFingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ruleApproverID      = "cccccccc-2222-3333-4444-555555555555"
)

// ruleApprovalRecord is seriesTestRecord's rule set at ruleFingerprint: the
// big-ID table with two rules and table 42 with one, last approved at
// approvedFingerprint.
const ruleApprovalRecord = `{
  "approved":false,
  "fingerprint":"` + ruleFingerprint + `",
  "rule_set":[
    {"dataset_id":9007199254740993,"rules":[
      {"dataset_rule_id":"d1","instance_name":"Premium positive","position":1,"severity":"error","params":{"limit":9007199254740993},"state":"enabled"},
      {"dataset_rule_id":"d2","instance_name":"Dates","position":2,"severity":"warning","params":{},"state":"enabled"}]},
    {"dataset_id":42,"rules":[
      {"dataset_rule_id":"d3","instance_name":"Premium positive","position":1,"severity":"error","params":{},"state":"enabled"}]}
  ],
  "approvals":[
    {"id":7,"approved_by_id":"` + ruleApproverID + `","approved_at":"2026-10-01T09:00:00Z",
     "fingerprint":"` + approvedFingerprint + `","rule_set":[],"change_proposal_id":null}
  ]
}`

func approvedRuleRecord(t *testing.T) string {
	t.Helper()
	return replaceAll(t, ruleApprovalRecord,
		`"approved":false`, `"approved":true`,
		`"approvals":[`, `"approvals":[{"id":8,"approved_by_id":"`+ruleApproverID+`","approved_at":"2026-11-01T09:00:00Z","fingerprint":"`+ruleFingerprint+`","rule_set":[],"change_proposal_id":null},`)
}

// ruleApprovalServer answers the series reads and a rule-set approval GET with
// record, unprotects the current context and resets approve-rules' flags.
func ruleApprovalServer(t *testing.T, record string) *testutil.MockAPIServer {
	t.Helper()
	mock := tableSeriesTestServer(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts[cfg.CurrentContext].Protected = testutil.BoolPtr(false)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, path := range [][]string{{"get", "table-series"}, {"table-series", "approve-rules"}} {
			cmd, _, err := GetRootCmd().Find(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"rule-approval", "periods", "fingerprint", "yes"} {
				if f := cmd.Flags().Lookup(name); f != nil {
					_ = f.Value.Set(f.DefValue)
					f.Changed = false
				}
			}
		}
	}
	reset()
	t.Cleanup(reset)
	mock.RegisterHandler("GET", ruleApprovalPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
			t.Errorf("Authorization = %q", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(record))
	})
	return mock
}

func answerApproval(t *testing.T, mock *testutil.MockAPIServer, status int, body string) {
	t.Helper()
	mock.RegisterHandler("POST", ruleApprovalPath, func(w http.ResponseWriter, r *http.Request) {
		var sent map[string]any
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		if want := map[string]any{"fingerprint": ruleFingerprint}; !reflect.DeepEqual(sent, want) {
			t.Errorf("body = %v, want %v", sent, want)
		}
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
}

func runApproveRules(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"table-series", "approve-rules"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
		resetPersistentFlag(t, cmd, "context", "")
		config.SetContextOverride("")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestGetTableSeriesRuleApprovalFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := ruleApprovalServer(t, ruleApprovalRecord)
			ref, want := seriesTestID, []string{"GET " + ruleApprovalPath}
			if format == "table" {
				ref, want = "Monthly bordereau", append([]string{"GET " + seriesTestPath}, want...)
			}
			out, err := runRoot(t, "get", "table-series", ref, "--rule-approval", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			switch format {
			case "table":
				for _, s := range []string{"APPROVED", "FINGERPRINT", "false", ruleFingerprint, ruleApproverID, "2026-10-01T09:00:00Z"} {
					if !strings.Contains(out, s) {
						t.Errorf("output lacks %q: %s", s, out)
					}
				}
				var fields []string
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, ruleFingerprint) {
						fields = strings.Fields(line)
					}
				}
				if len(fields) != 7 || fields[2] != "2" || fields[3] != "3" || fields[6] != "1" {
					t.Errorf("row = %v, want 2 tables, 3 rules, 1 approval", fields)
				}
			case "name":
				if out != "table-series/"+seriesTestID+"\n" {
					t.Errorf("output = %q", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, ruleApprovalRecord)
				if !strings.Contains(out, "9007199254740993") {
					t.Errorf("YAML rounded a big table ID: %s", out)
				}
			default:
				assertSeriesJSON(t, out, ruleApprovalRecord)
				if strings.Count(out, "9007199254740993") != 2 {
					t.Errorf("JSON rounded a big integer: %s", out)
				}
			}
		})
	}
}

func TestGetTableSeriesRuleApprovalUsageFailsBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"--rule-approval"},
		{seriesTestID, "--rule-approval", "--periods"},
		{" ", "--rule-approval"},
		{seriesTestID, "--rule-approval", "--columns", "nope"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ruleApprovalServer(t, ruleApprovalRecord)
			if err := runOutsideGetWithoutContext(t, append([]string{"get", "table-series"}, args...)...); qerrors.GetExitCode(err) != 2 {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
		})
	}
}

func TestApproveRulesPreviewsThenApprovesTheReviewedFingerprint(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := ruleApprovalServer(t, ruleApprovalRecord)
			approved := approvedRuleRecord(t)
			answerApproval(t, mock, http.StatusOK, approved)
			out, stderr, err := runApproveRules(t, "Monthly bordereau", "--yes", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"GET " + seriesTestPath, "GET " + ruleApprovalPath, "POST " + ruleApprovalPath}
			if got := mock.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			for _, s := range []string{ruleFingerprint, "table 9007199254740993: 2 rules", "table 42: 1 rules",
				"Tables:      2, 3 rules", "Last approval: 2026-10-01T09:00:00Z by " + ruleApproverID} {
				if !strings.Contains(stderr, s) {
					t.Errorf("preview lacks %q: %s", s, stderr)
				}
			}
			name := "table-series/" + seriesTestID
			switch format {
			case "table":
				if out != name+" rules approved (fingerprint "+ruleFingerprint+")\n" {
					t.Errorf("output = %q", out)
				}
			case "name":
				if out != name+"\n" {
					t.Errorf("output = %q", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, approved)
			default:
				assertSeriesJSON(t, out, approved)
			}
		})
	}
}

func TestApproveRulesWritesNothingWhenItShouldNot(t *testing.T) {
	for _, tc := range []struct {
		name, record, want string
		args               []string
		exit               int
	}{
		{"already approved", "", "already approved", []string{seriesTestID}, 0},
		{"stale fingerprint", ruleApprovalRecord, approvedFingerprint, []string{seriesTestID, "--fingerprint", approvedFingerprint, "--yes"}, 2},
		{"no tables", `{"approved":false,"fingerprint":"` + ruleFingerprint + `","rule_set":[],"approvals":[]}`, "no tables", []string{seriesTestID, "--yes"}, 2},
		{"unconfirmed", ruleApprovalRecord, "--yes", []string{seriesTestID}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "unconfirmed" && term.IsTerminal(int(os.Stdin.Fd())) {
				t.Skip("requires non-interactive stdin")
			}
			record := tc.record
			if record == "" {
				record = approvedRuleRecord(t)
			}
			mock := ruleApprovalServer(t, record)
			out, _, err := runApproveRules(t, tc.args...)
			if qerrors.GetExitCode(err) != tc.exit {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
			text := out
			if err != nil {
				text = err.Error()
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("output = %q, want %q", text, tc.want)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Errorf("writes = %v", writes)
			}
		})
	}
}

func TestApproveRulesAcceptsTheMatchingFingerprintInAnyCase(t *testing.T) {
	mock := ruleApprovalServer(t, ruleApprovalRecord)
	answerApproval(t, mock, http.StatusOK, approvedRuleRecord(t))
	if _, _, err := runApproveRules(t, seriesTestID, "--fingerprint", strings.ToUpper(ruleFingerprint), "--yes"); err != nil {
		t.Fatal(err)
	}
	if writes := seriesMutationRequests(mock); !reflect.DeepEqual(writes, []string{"POST " + ruleApprovalPath}) {
		t.Errorf("writes = %v", writes)
	}
}

func TestApproveRulesRefusalsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		hint         bool
	}{{409, 2, true}, {403, 4, false}, {404, 3, false}, {500, 1, false}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock := ruleApprovalServer(t, ruleApprovalRecord)
			answerApproval(t, mock, tc.status, `{"detail":"The series rules changed. Review the current rules before approving them."}`)
			_, _, err := runApproveRules(t, seriesTestID, "--yes")
			if qerrors.GetExitCode(err) != tc.exit {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
			if got := strings.Contains(err.Error(), "qctl get table-series "+seriesTestID+" --rule-approval"); got != tc.hint {
				t.Errorf("hint = %t, want %t: %v", got, tc.hint, err)
			}
		})
	}
}

func TestApproveRulesUsageFailsBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{}, {" "}, {seriesTestID, "extra"},
		{seriesTestID, "--fingerprint", ""}, {seriesTestID, "--fingerprint", "abc"},
		{seriesTestID, "--fingerprint", strings.Repeat("g", 64)},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ruleApprovalServer(t, ruleApprovalRecord)
			if err := runOutsideGetWithoutContext(t, append([]string{"table-series", "approve-rules"}, args...)...); qerrors.GetExitCode(err) != 2 {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
		})
	}
}

func TestApproveRulesRequiresANamedProtectedContext(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	cmd, _, err := GetRootCmd().Find([]string{"table-series", "approve-rules"})
	if err != nil || !cmdutil.IsDestructive(cmd) {
		t.Fatalf("approve-rules is not destructive: %v", err)
	}
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "unnamed", true: "named"}[named], func(t *testing.T) {
			mock := ruleApprovalServer(t, ruleApprovalRecord)
			answerApproval(t, mock, http.StatusOK, approvedRuleRecord(t))
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Contexts[cfg.CurrentContext].Protected = nil
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			args := []string{seriesTestID, "--yes"}
			if named {
				args = append(args, "--context", cfg.CurrentContext)
			}
			_, _, err = runApproveRules(t, args...)
			if named {
				if err != nil || len(seriesMutationRequests(mock)) != 1 {
					t.Fatalf("err = %v; requests = %v", err, mock.Requests())
				}
			} else if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "--context") || len(mock.Requests()) != 0 {
				t.Fatalf("err = %v; requests = %v, want protected refusal and no requests", err, mock.Requests())
			}
		})
	}
}
