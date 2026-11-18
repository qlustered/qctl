package table_rule //nolint:revive // Matches the command directory.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

var (
	proposalsPath   = "/api/orgs/" + testOrgID + "/change-proposals"
	proposedID      = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	ruleDetailPath  = func(name string) string { return "/api/orgs/" + testOrgID + "/dataset-rules/" + ruleID(name).String() }
	liveEmailParams = map[string]any{"threshold": 5, "mode": "strict", "window": "7d"}
)

// proposeEnv serves email_check's detail with liveEmailParams and records
// each create body; the server answers with a pending_review proposal.
func proposeEnv(t *testing.T) *[]map[string]any {
	t.Helper()
	return proposeEnvWith(t, liveEmailParams)
}

// proposeEnvWith is proposeEnv with email_check's live params set to live.
func proposeEnvWith(t *testing.T, live map[string]any) *[]map[string]any {
	t.Helper()
	e := newEnv(t, 42)
	e.mock.RegisterHandler("GET", ruleDetailPath("email_check"), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"id": ruleID("email_check"), "dataset_id": 42, "instance_name": "email_check", "params": live,
		})
	})
	var bodies []map[string]any
	e.mock.RegisterHandler("POST", proposalsPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		testutil.RespondJSON(w, http.StatusCreated, map[string]any{
			"id": proposedID, "organization_id": testOrgID, "state": "pending_review", "version_id": 1,
			"operation": "dataset_rule_patch", "dataset_rule_id": ruleID("email_check"), "dataset_id": 42,
			"payload": body["payload"], "addressed_alert_item_ids": []int{}, "addressed_warning_item_ids": []int{},
			"allowed_actions": []string{"withdraw", "edit"},
			"created_at":      "2026-11-02T00:00:00Z", "updated_at": "2026-11-02T00:00:00Z",
		})
	})
	return &bodies
}

func proposeCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"table-rule", "propose"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestPropose_SendsTheMergedLiveParams: the payload is the rule's complete
// live params with the edits applied, the target is dataset_rule_id, and the
// proposal is submitted.
func TestPropose_SendsTheMergedLiveParams(t *testing.T) {
	bodies := proposeEnv(t)
	stdout, stderr, err := proposeCmd(t, "email_check", "--table-id", "42",
		"--set-param", "threshold=8", "--set-param", "label=high", "--unset-param", "mode")
	if err != nil {
		t.Fatalf("propose: %v\n%s%s", err, stdout, stderr)
	}
	if len(*bodies) != 1 {
		t.Fatalf("%d creates, want 1", len(*bodies))
	}
	body := (*bodies)[0]
	want := map[string]any{"params": map[string]any{"threshold": float64(8), "window": "7d", "label": "high"}}
	if !reflect.DeepEqual(body["payload"], any(want)) {
		t.Errorf("payload = %v, want %v", body["payload"], want)
	}
	if body["operation"] != "dataset_rule_patch" || body["dataset_rule_id"] != ruleID("email_check").String() ||
		body["submit"] != true || body["alert_item_id"] != nil || body["warning_item_id"] != nil {
		t.Errorf("create body = %v", body)
	}
	for _, line := range []string{
		"Params change for table-rule/email_check (its live value -> the proposed one):",
		"- label: (unset) -> \"high\"",
		"- mode: \"strict\" -> (unset)",
		"- threshold: 5 -> 8",
		"change-proposal/" + proposedID.String() + " created (state: pending_review, version 1)",
		"Next: qctl change-proposal edit " + proposedID.String() + " --set-param <key>=<value>",
		"Next: qctl describe change-proposal " + proposedID.String(),
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q\n%s", line, stdout)
		}
	}
}

// TestPropose_DraftAddressedAndDependency: --draft sends submit false, and
// the --also-* lists and --depends-on go on the create body.
func TestPropose_DraftAddressedAndDependency(t *testing.T) {
	bodies := proposeEnv(t)
	dep := "0190cccc-0000-4000-8000-0000000000aa"
	_, _, err := proposeCmd(t, "email_check", "--table", "orders", "--set-param", "threshold=9",
		"--draft", "--also-alert", "7", "--also-warning", "8", "--depends-on", dep)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	body := (*bodies)[0]
	if body["submit"] != false || body["depends_on_id"] != dep ||
		!reflect.DeepEqual(body["addressed_alert_item_ids"], []any{float64(7)}) ||
		!reflect.DeepEqual(body["addressed_warning_item_ids"], []any{float64(8)}) {
		t.Errorf("create body = %v", body)
	}
}

// TestPropose_JSONPrintsTheProposalAndMovesTheProseToStderr.
func TestPropose_JSONPrintsTheProposalAndMovesTheProseToStderr(t *testing.T) {
	proposeEnv(t)
	stdout, stderr, err := proposeCmd(t, "email_check", "--table-id", "42", "--set-param", "threshold=8", "-o", "json")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["id"] != proposedID.String() {
		t.Fatalf("stdout = %s (%v)", stdout, err)
	}
	if !strings.Contains(stderr, "- threshold: 5 -> 8") || !strings.Contains(stderr, "created (state: pending_review") {
		t.Errorf("stderr = %s", stderr)
	}
}

// TestPropose_RefusalsBeforeAnyRequest: every usage refusal decidable from
// the command line exits 2 with no request.
func TestPropose_RefusalsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"no edit":         {"email_check", "--table-id", "42"},
		"no table":        {"email_check", "--set-param", "a=1"},
		"no equals":       {"email_check", "--table-id", "42", "--set-param", "threshold"},
		"empty key":       {"email_check", "--table-id", "42", "--set-param", "=1"},
		"empty unset":     {"email_check", "--table-id", "42", "--unset-param", ""},
		"set and unset":   {"email_check", "--table-id", "42", "--set-param", "a=1", "--unset-param", "a"},
		"bad depends-on":  {"email_check", "--table-id", "42", "--set-param", "a=1", "--depends-on", "nope"},
		"bad also-alert":  {"email_check", "--table-id", "42", "--set-param", "a=1", "--also-alert", "0"},
		"missing operand": {"--table-id", "42", "--set-param", "a=1"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 42)
			_, _, err := proposeCmd(t, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2 (err: %v)", got, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %v, want none", reqs)
			}
		})
	}
}

// TestPropose_RefusesAnUnknownUnsetKey: after reading the live params, a
// --unset-param key the rule lacks exits 2 with no create.
func TestPropose_RefusesAnUnknownUnsetKey(t *testing.T) {
	bodies := proposeEnv(t)
	_, _, err := proposeCmd(t, "email_check", "--table-id", "42", "--set-param", "threshold=8", "--unset-param", "nope")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2 (err: %v)", got, err)
	}
	if want := `has no param "nope" (it has: mode, threshold, window)`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q lacks %q", err, want)
	}
	if len(*bodies) != 0 {
		t.Errorf("creates = %v, want none", *bodies)
	}
}

// TestPropose_SendsAnEditThatChangesNothing: atlas accepts a params patch
// equal to the live params, so it is created, and the result says so.
func TestPropose_SendsAnEditThatChangesNothing(t *testing.T) {
	bodies := proposeEnv(t)
	stdout, _, err := proposeCmd(t, "email_check", "--table-id", "42", "--set-param", "threshold=5", "--set-param", `mode="strict"`)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("%d creates, want 1", len(*bodies))
	}
	if want := "Params change for table-rule/email_check: (none — the proposed params equal its live params)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q\n%s", want, stdout)
	}
}

// TestPropose_ServerRefusalKeepsItsExitCode: a 422 from the create route (a
// deleted rule) exits 2 with the server's message.
func TestPropose_ServerRefusalKeepsItsExitCode(t *testing.T) {
	e := newEnv(t, 42)
	e.mock.RegisterHandler("GET", ruleDetailPath("email_check"), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": ruleID("email_check"), "params": liveEmailParams})
	})
	e.mock.RegisterHandler("POST", proposalsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "The target dataset rule is deleted."}})
	})
	_, _, err := proposeCmd(t, "email_check", "--table-id", "42", "--set-param", "threshold=8")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2 (err: %v)", got, err)
	}
	if !strings.Contains(err.Error(), "The target dataset rule is deleted.") {
		t.Errorf("error = %v", err)
	}
}

// TestPropose_RefusesToCarryMaskedLiveParams: a masked viewer reads every
// param as mask text, so an edit that leaves one untouched exits 2 naming it,
// and nothing is created.
func TestPropose_RefusesToCarryMaskedLiveParams(t *testing.T) {
	bodies := proposeEnvWith(t, map[string]any{
		"threshold": "•••••", "mode": "•••••rict", "allowed": []any{"•••••"}, "window": "•••••",
	})
	_, _, err := proposeCmd(t, "email_check", "--table-id", "42", "--set-param", "threshold=8", "--unset-param", "window")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "the value of: allowed, mode.") {
		t.Errorf("error = %v, want it to name allowed and mode", err)
	}
	if len(*bodies) != 0 {
		t.Errorf("%d creates, want 0", len(*bodies))
	}
}

// TestPropose_AllowsReplacingEveryMaskedParam: setting or unsetting every
// masked param leaves no mask text in the payload, so the proposal is created.
func TestPropose_AllowsReplacingEveryMaskedParam(t *testing.T) {
	bodies := proposeEnvWith(t, map[string]any{"threshold": "•••••", "mode": "•••••rict"})
	if _, _, err := proposeCmd(t, "email_check", "--table-id", "42",
		"--set-param", "threshold=8", "--unset-param", "mode"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	want := map[string]any{"params": map[string]any{"threshold": float64(8)}}
	if len(*bodies) != 1 || !reflect.DeepEqual((*bodies)[0]["payload"], any(want)) {
		t.Errorf("creates = %v, want one with payload %v", *bodies, want)
	}
}
