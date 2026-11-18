//nolint:revive // Package name intentionally matches the table-rule command directory.
package table_rule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupTestCommand builds the root the real one builds, minus what this package
// does not exercise. The FlagErrorFunc and TypeArgsErrors are the real root's:
// without them an unknown flag or a missing positional exits 1 here and 2 in the
// shipped binary, and a test asserting the contract would assert the harness.
func setupTestCommand() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	root.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(root)
	return root
}

// runSplit is run with stdout and stderr kept apart.
func runSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"table-rule", "move"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"table-rule", "move"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

type fixtureRule struct {
	name string
	pos  int
}

func ruleID(name string) uuid.UUID { return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)) }

// The table every test runs over: gapped, the shape a cascade delete leaves,
// and a deleted rule still holding its number.
var fixture = []fixtureRule{{"email_check", 1}, {"phone_check", 2}, {"zip_check", 4}, {"old_rule", 5}, {"name_check", 7}}

type env struct {
	mock      *testutil.MockAPIServer
	bodies    []string
	listHit   int
	listQuery []string
}

func newEnv(t *testing.T, datasetID int) *env {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")

	e := &env{mock: mock}
	base := fmt.Sprintf("/api/orgs/%s/datasets/%d/dataset-rules", testOrgID, datasetID)
	mock.RegisterHandler("GET", base, func(w http.ResponseWriter, req *http.Request) {
		e.listHit++
		e.listQuery = append(e.listQuery, req.URL.RawQuery)
		results := make([]map[string]any, len(fixture))
		for i, r := range fixture {
			state := "enabled"
			if r.name == "old_rule" {
				state = "deleted"
			}
			results[i] = map[string]any{
				"id": ruleID(r.name), "instance_name": r.name, "position": r.pos, "state": state,
				"severity": "as_declared", "release": "1.0.0", "family_name": r.name,
				"family_id": uuid.Nil, "rule_revision_id": uuid.Nil, "has_multiple_releases": false,
				"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
			}
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
	})
	for _, r := range fixture {
		mock.RegisterHandler("POST", base+"/"+ruleID(r.name).String()+"/reorder", func(w http.ResponseWriter, req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			e.bodies = append(e.bodies, r.name+" "+strings.TrimSpace(string(b)))
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			fmt.Sprintf(`{"total_rows":1,"page":1,"results":[{"id":%d,"name":"orders","state":"active"}]}`, datasetID)))
	})
	return e
}

func TestMove_PositionSendsTheNumberAndReportsTheShift(t *testing.T) {
	e := newEnv(t, 42)
	out, err := run(t, "email_check", "--table", "orders", "--position", "4")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if len(e.bodies) != 1 || e.bodies[0] != `email_check {"new_position":4}` {
		t.Fatalf("requests = %q", e.bodies)
	}
	for _, want := range []string{
		"table-rule/email_check moved from position 1 to 4 in table orders (id: 42)",
		"2 rule(s) moved one place earlier to fill the gap: phone_check, zip_check",
		"qctl table-rule move " + ruleID("email_check").String() + " --table-id 42 --position 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

// --position past the end is the server's clamp, reported rather than hidden:
// the value SENT is the last position, so the result line and the wire agree.
func TestMove_PastTheEndMovesLastAndSaysSo(t *testing.T) {
	e := newEnv(t, 42)
	out, err := run(t, "phone_check", "--table-id", "42", "--position", "999")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if len(e.bodies) != 1 || e.bodies[0] != `phone_check {"new_position":7}` {
		t.Fatalf("requests = %q", e.bodies)
	}
	for _, want := range []string{
		"moved from position 2 to 7 in table 42",
		"--position 999 is past the last position, so the rule moved last.",
		// The deleted rule holds a position and moves with the rest.
		"3 rule(s) moved one place earlier to fill the gap: zip_check, old_rule, name_check",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

// Moving DOWN before a rule sends that rule's position minus one; sending its
// own position would land the rule after it.
func TestMove_BeforeMovingDownSendsOneLess(t *testing.T) {
	e := newEnv(t, 42)
	out, err := run(t, "email_check", "--table-id", "42", "--before", "name_check")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if len(e.bodies) != 1 || e.bodies[0] != `email_check {"new_position":6}` {
		t.Fatalf("requests = %q", e.bodies)
	}
}

func TestMove_AfterMovingUpSendsOneMore(t *testing.T) {
	e := newEnv(t, 42)
	out, err := run(t, "name_check", "--table-id", "42", "--after", "email_check")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if len(e.bodies) != 1 || e.bodies[0] != `name_check {"new_position":2}` {
		t.Fatalf("requests = %q", e.bodies)
	}
	if !strings.Contains(out, "3 rule(s) moved one place later to make room: phone_check, zip_check, old_rule") {
		t.Errorf("output:\n%s", out)
	}
}

func TestMove_UnchangedSendsNoRequest(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"same position":      {[]string{"zip_check", "--table-id", "42", "--position", "4"}, "table-rule/zip_check unchanged: already at position 4 in table 42"},
		"already last":       {[]string{"name_check", "--table-id", "42", "--position", "50"}, "table-rule/name_check unchanged: already at the last position (7) in table 42"},
		"already before":     {[]string{"email_check", "--table-id", "42", "--before", "phone_check"}, "table-rule/email_check unchanged: already immediately before phone_check (position 1) in table 42"},
		"already after, gap": {[]string{"zip_check", "--table-id", "42", "--after", "phone_check"}, "table-rule/zip_check unchanged: already immediately after phone_check (position 4) in table 42"},
		"target by short id": {[]string{"email_check", "--table-id", "42", "--before", ruleID("phone_check").String()[:8]}, "unchanged: already immediately before phone_check"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 42)
			out, err := run(t, tc.args...)
			if err != nil {
				t.Fatalf("move: %v\n%s", err, out)
			}
			if len(e.bodies) != 0 {
				t.Errorf("sent %q for a move that changes nothing", e.bodies)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q\n%s", tc.want, out)
			}
		})
	}
}

// The list must carry every rule the server shifts, and the server's shift has
// no state clause: a state filter on the read would drop the deleted rule that
// TestMove_PastTheEndMovesLastAndSaysSo counts, but only against a mock that
// honours it. Pin the request instead.
func TestMove_ListIsNotStateFiltered(t *testing.T) {
	e := newEnv(t, 42)
	if out, err := run(t, "email_check", "--table-id", "42", "--position", "2"); err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	for _, q := range e.listQuery {
		if strings.Contains(q, "state") {
			t.Errorf("rule list request filters by state: %q", q)
		}
	}
}

// Every refusal decidable from the command line exits 2 before any request —
// the rule list included.
func TestMove_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no destination", []string{"email_check", "--table-id", "42"}, "must specify one of: --position or --before or --after"},
		{"two destinations", []string{"email_check", "--table-id", "42", "--position", "2", "--after", "zip_check"}, "--position"},
		{"zero position", []string{"email_check", "--table-id", "42", "--position", "0"}, "--position must be a positive integer"},
		{"negative position", []string{"email_check", "--table-id", "42", "--position", "-1"}, "--position must be a positive integer"},
		{"empty before", []string{"email_check", "--table-id", "42", "--before", ""}, "--before cannot be empty"},
		{"no table", []string{"email_check", "--position", "2"}, "r-* alias"},
		{"no rule", []string{"--table-id", "42", "--position", "2"}, "accepts 1 arg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 42)
			out, err := run(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (%v), want 2\n%s", code, err, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if e.listHit != 0 || len(e.bodies) != 0 {
				t.Errorf("a usage refusal cost %d list and %d reorder request(s)", e.listHit, len(e.bodies))
			}
		})
	}
}

func TestMove_ResolutionRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"unknown rule", []string{"nope", "--table-id", "42", "--position", "1"}, qerrors.ExitNotFound},
		// A fragment is not a match for a write: no substring fallback.
		{"partial name", []string{"email", "--table-id", "42", "--position", "1"}, qerrors.ExitNotFound},
		{"unknown target", []string{"email_check", "--table-id", "42", "--after", "nope"}, qerrors.ExitNotFound},
		{"target is the rule", []string{"email_check", "--table-id", "42", "--after", "email_check"}, qerrors.ExitBadUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 42)
			out, err := run(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit code = %d (%v), want %d\n%s", code, err, tc.code, out)
			}
			if len(e.bodies) != 0 {
				t.Errorf("sent %q", e.bodies)
			}
		})
	}
}

func TestMove_UnknownAliasIsNotFound(t *testing.T) {
	e := newEnv(t, 42)
	allocateAlias(t, e, ruleID("email_check").String())
	out, err := run(t, "r-99", "--position", "1")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3\n%s", code, err, out)
	}
	if e.listHit != 0 || len(e.bodies) != 0 {
		t.Errorf("unknown alias cost %d list and %d reorder request(s)", e.listHit, len(e.bodies))
	}
}

// allocateAlias caches r-1 for the rule id on the mock's server.
func allocateAlias(t *testing.T, e *env, id string) {
	t.Helper()
	key, _ := config.NormalizeEndpointKey(e.mock.Server.URL)
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open alias store: %v", err)
	}
	if _, err := store.Allocate(key, aliasstore.ResourceDatasetRule, id); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()
}

// An r-* alias names the table through the rule's detail, so it needs no table
// flag and the reorder lands on the rule's own dataset.
func TestMove_AliasNeedsNoTable(t *testing.T) {
	e := newEnv(t, 9)
	id := ruleID("zip_check").String()
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+id, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": id, "dataset_id": 9, "instance_name": "zip_check"})
	})
	allocateAlias(t, e, id)

	out, err := run(t, "r-1", "--position", "1")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if len(e.bodies) != 1 || e.bodies[0] != `zip_check {"new_position":1}` {
		t.Fatalf("requests = %q", e.bodies)
	}
	if !strings.Contains(out, "qctl table-rule move "+id+" --table-id 9 --position 4") {
		t.Errorf("output:\n%s", out)
	}
}

// A table flag beside an alias is not decoration: the rule must be on THAT
// table. Before, the flag was format-checked and then ignored, so an alias for a
// rule elsewhere reordered a table the user never named and printed the flag's
// name beside the other table's id.
func TestMove_AliasWithATableFlagMustBeOnThatTable(t *testing.T) {
	t.Run("on another table", func(t *testing.T) {
		e := newEnv(t, 42)
		detailHit := false
		other := uuid.NewSHA1(uuid.NameSpaceOID, []byte("elsewhere")).String()
		e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+other, func(w http.ResponseWriter, _ *http.Request) {
			detailHit = true
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": other, "dataset_id": 9, "instance_name": "elsewhere"})
		})
		allocateAlias(t, e, other)
		out, err := run(t, "r-1", "--table", "orders", "--position", "1")
		if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
			t.Fatalf("exit code = %d (%v), want %d\n%s", code, err, qerrors.ExitNotFound, out)
		}
		if !strings.Contains(err.Error(), "alias r-1 is table-rule "+other+", which is not on this table") {
			t.Errorf("error = %v", err)
		}
		if len(e.bodies) != 0 || detailHit {
			t.Errorf("sent %q (detail read: %v) for a rule not on the named table", e.bodies, detailHit)
		}
	})
	t.Run("on that table", func(t *testing.T) {
		e := newEnv(t, 42)
		allocateAlias(t, e, ruleID("zip_check").String())
		out, err := run(t, "r-1", "--table-id", "42", "--position", "1")
		if err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if len(e.bodies) != 1 || e.bodies[0] != `zip_check {"new_position":1}` {
			t.Fatalf("requests = %q", e.bodies)
		}
	})
}

// -o json writes the move built from the plan, -o name the moved rule, and the
// prose, the move-back line included, goes to stderr.
func TestMove_WritesTheMoveDocument(t *testing.T) {
	newEnv(t, 42)
	stdout, stderr, err := runSplit(t, "phone_check", "--table-id", "42", "--position", "999", "-o", "json")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Move it back with:") {
		t.Errorf("stderr lacks the move-back line:\n%s", stderr)
	}
	var got struct {
		ID           string `json:"id"`
		InstanceName string `json:"instance_name"`
		TableID      int    `json:"table_id"`
		From, To     int
		Requested    *int `json:"requested"`
		Clamped      bool `json:"clamped"`
		Unchanged    bool `json:"unchanged"`
		Shifted      []struct {
			ID           string `json:"id"`
			InstanceName string `json:"instance_name"`
		} `json:"shifted"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.ID != ruleID("phone_check").String() || got.InstanceName != "phone_check" || got.TableID != 42 ||
		got.From != 2 || got.To != 7 || got.Requested == nil || *got.Requested != 999 || !got.Clamped || got.Unchanged {
		t.Errorf("document = %+v", got)
	}
	if len(got.Shifted) != 3 || got.Shifted[0].InstanceName != "zip_check" || got.Shifted[0].ID != ruleID("zip_check").String() {
		t.Errorf("shifted = %+v, want zip_check, old_rule, name_check", got.Shifted)
	}

	stdout, _, err = runSplit(t, "zip_check", "--table-id", "42", "--after", "phone_check", "-o", "json")
	if err != nil {
		t.Fatalf("unchanged move: %v", err)
	}
	var unchanged map[string]any
	if err := json.Unmarshal([]byte(stdout), &unchanged); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if unchanged["unchanged"] != true || unchanged["requested"] != nil {
		t.Errorf("unchanged document = %v, want unchanged and no requested position", unchanged)
	}
	if shifted, ok := unchanged["shifted"].([]any); !ok || len(shifted) != 0 {
		t.Errorf("shifted = %v, want an empty list", unchanged["shifted"])
	}

	stdout, _, err = runSplit(t, "email_check", "--table-id", "42", "--position", "4", "-o", "name")
	if err != nil {
		t.Fatalf("-o name: %v", err)
	}
	if want := "table-rule/" + ruleID("email_check").String() + "\n"; stdout != want {
		t.Errorf("-o name stdout = %q, want %q", stdout, want)
	}
}
