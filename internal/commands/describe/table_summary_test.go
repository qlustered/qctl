package describe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

// quarantineByHoldKey is the wire key the structured payload promises for the
// hold breakdown, named once so the assertions below cannot disagree about it.
const quarantineByHoldKey = "quarantine_by_hold"

// The hold buckets get their own line under Rows, in status order, as the
// counter sent them: they sum to 33 of the 41 quarantined rows in the shared
// fixture, which is normal and neither hedged nor warned about.
func TestDescribeTable_QuarantineLineCarriesTheHoldBreakdown(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "\nQuarantine:   on_hold=2 approved=7 waiting_for_approval=4 held_for_approval=20 " +
		"(possible_duplicate=5 review_policy=15 table_setting=0)\n"
	if !strings.Contains(out, want) {
		t.Errorf("Quarantine line missing.\n got:\n%s\nwant: %q", out, want)
	}
	if !strings.Contains(out, "Rows:         9182 clean / 41 quarantined (6 passed validation, 35 failed validation)\n") {
		t.Errorf("the validation clause must stay on the Rows line:\n%s", out)
	}
	if strings.Contains(errOut, "quarantine counts") {
		t.Errorf("buckets that do not sum are normal, not a degraded section; stderr = %q", errOut)
	}
}

// The breakdown in -o json|yaml, read as the payload KEY, for the reason
// TestDescribeTable_StructuredPayloadCarriesTheValidationBreakdown gives.
func TestDescribeTable_StructuredPayloadCarriesTheHoldBreakdown(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		t.Run(format, func(t *testing.T) {
			out, _, err := runDescribeTable(t, "12", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			section, ok := structuredPayload(t, format, out)[quarantineByHoldKey].(map[string]any)
			if !ok {
				t.Fatalf("-o %s payload carries no `%s` object\n%s", format, quarantineByHoldKey, out)
			}
			for key, want := range map[string]string{"on_hold": "2", "approved": "7", "waiting_for_approval": "4", "held_for_approval": "20"} {
				if got := fmt.Sprintf("%v", section[key]); got != want {
					t.Errorf("-o %s: %s.%s = %v, want %s", format, quarantineByHoldKey, key, section[key], want)
				}
			}
			byReason, ok := section["held_for_approval_by_reason"].(map[string]any)
			if !ok {
				t.Fatalf("-o %s: %s has no held_for_approval_by_reason object: %v", format, quarantineByHoldKey, section)
			}
			for key, want := range map[string]string{"possible_duplicate": "5", "review_policy": "15", "table_setting": "0"} {
				if got := fmt.Sprintf("%v", byReason[key]); got != want {
					t.Errorf("-o %s: held_for_approval_by_reason.%s = %v, want %s", format, key, byReason[key], want)
				}
			}
		})
	}
}

// waiting_for_approval is optional within the breakdown: a server that sends no
// waiting count keeps the Quarantine line and the payload object, without the
// token or the key, while a count of zero is printed and carried as 0.
func TestDescribeTable_WaitingCountIsShownOnlyWhenSent(t *testing.T) {
	const reasons = `"held_for_approval_by_reason":{"possible_duplicate":5,"review_policy":15,"table_setting":0},` +
		`"passed_validation":6,"failed_validation":35}`
	for _, tc := range []struct {
		name        string
		body        string
		wantLine    string
		wantWaiting string // "" = the key must be absent
	}{
		{"a server without the waiting count",
			`{"on_hold":2,"approved":7,"held_for_approval":20,` + reasons,
			"\nQuarantine:   on_hold=2 approved=7 held_for_approval=20 " +
				"(possible_duplicate=5 review_policy=15 table_setting=0)\n",
			""},
		{"no rows waiting",
			`{"on_hold":2,"approved":7,"waiting_for_approval":0,"held_for_approval":20,` + reasons,
			"\nQuarantine:   on_hold=2 approved=7 waiting_for_approval=0 held_for_approval=20 " +
				"(possible_duplicate=5 review_policy=15 table_setting=0)\n",
			"0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.body))
				})

			out, _, err := runDescribeTable(t, "12")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("Quarantine line wrong.\n got:\n%s\nwant: %q", out, tc.wantLine)
			}
			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if err != nil {
					t.Fatalf("-o %s: %v", format, err)
				}
				section, ok := structuredPayload(t, format, out)[quarantineByHoldKey].(map[string]any)
				if !ok {
					t.Fatalf("-o %s payload carries no `%s` object\n%s", format, quarantineByHoldKey, out)
				}
				got, present := section["waiting_for_approval"]
				switch {
				case tc.wantWaiting == "" && present:
					t.Errorf("-o %s emitted waiting_for_approval = %v for a count the server did not send", format, got)
				case tc.wantWaiting != "" && fmt.Sprintf("%v", got) != tc.wantWaiting:
					t.Errorf("-o %s: waiting_for_approval = %v (present %v), want %s", format, got, present, tc.wantWaiting)
				}
				if got := fmt.Sprintf("%v", section["held_for_approval"]); got != "20" {
					t.Errorf("-o %s: held_for_approval = %v, want 20", format, section["held_for_approval"])
				}
			}
		})
	}
}

// The breakdown is absent — no line, no key — whenever it is unknown: nothing
// quarantined (no request), the counter forbidden, or a counter that answers
// without the hold buckets. The last one keeps the validation clause: the two
// projections come from one response but stand or fall separately.
func TestDescribeTable_UnknownHoldBreakdownIsOmitted(t *testing.T) {
	counter := func(respond http.HandlerFunc) func(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv) {
		return func(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv) {
			mock, env := describeTableMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status", respond)
			return mock, env
		}
	}
	for _, tc := range []struct {
		name           string
		setup          func(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv)
		wantValidation bool
	}{
		{"no quarantined rows", describeTableMockWithNoBadRows, false},
		{"the counter is forbidden", counter(func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusForbidden, "forbidden")
		}), false},
		{"no hold buckets in the answer", counter(func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"passed_validation":6,"failed_validation":35}`))
		}), true},
		{"one hold bucket missing", counter(func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				json.RawMessage(`{"on_hold":2,"approved":7,"passed_validation":6,"failed_validation":35}`))
		}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := tc.setup(t)
			defer mock.Close()
			defer env.Cleanup()

			out, _, err := runDescribeTable(t, "12")
			if err != nil {
				t.Fatalf("a missing breakdown must not fail the command: %v", err)
			}
			if strings.Contains(out, "Quarantine:") {
				t.Errorf("printed a hold breakdown it does not have:\n%s", out)
			}
			if got := strings.Contains(out, "passed validation"); got != tc.wantValidation {
				t.Errorf("validation clause present = %v, want %v:\n%s", got, tc.wantValidation, out)
			}
			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if err != nil {
					t.Fatalf("-o %s: %v", format, err)
				}
				if _, present := structuredPayload(t, format, out)[quarantineByHoldKey]; present {
					t.Errorf("-o %s emitted `%s` for a breakdown it does not have:\n%s", format, quarantineByHoldKey, out)
				}
			}
		})
	}
}

func TestDescribeTableSharingLevelsAndCallerAccess(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			out, _, err := runDescribeTable(t, "12", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "table" {
				for _, want := range []string{"alice@example.com (owner; effective editor)", "My access:    editor"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				return
			}
			payload := structuredPayload(t, format, out)
			if payload["my_permission_level"] != "editor" {
				t.Errorf("caller level = %v", payload["my_permission_level"])
			}
			shared := payload["shared_with"].(map[string]any)
			members := shared["members"].([]any)
			member := members[0].(map[string]any)
			if member["email"] != "alice@example.com" || member["permission_level"] != "owner" || member["effective_permission_level"] != "editor" {
				t.Errorf("member = %v", member)
			}
		})
	}
}

// not_loaded gets a line of its own only when it is above zero, and the payload
// carries it whenever the server sent it.
func TestDescribeTable_NotLoadedCountIsShownWhenAboveZero(t *testing.T) {
	const counts = `{"on_hold":2,"approved":7,"waiting_for_approval":4,"held_for_approval":20,` +
		`"passed_validation":6,"failed_validation":35`
	for _, tc := range []struct {
		name     string
		body     string
		wantLine bool
		wantKey  string // "" = the key must be absent
	}{
		{"three copies", counts + `,"not_loaded":3}`, true, "3"},
		{"none", counts + `,"not_loaded":0}`, false, "0"},
		{"a server without the count", counts + `}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.body))
				})

			out, _, err := runDescribeTable(t, "12")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			const line = "\nNot loaded:   3 duplicate copies not loaded: a reviewer kept another copy\n"
			if got := strings.Contains(out, line); got != tc.wantLine {
				t.Errorf("Not loaded line present = %v, want %v:\n%s", got, tc.wantLine, out)
			}
			if !tc.wantLine && strings.Contains(out, "Not loaded:") {
				t.Errorf("unexpected Not loaded line:\n%s", out)
			}
			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if err != nil {
					t.Fatalf("-o %s: %v", format, err)
				}
				got, present := structuredPayload(t, format, out)["quarantine_not_loaded"]
				if tc.wantKey == "" {
					if present {
						t.Errorf("-o %s: quarantine_not_loaded = %v, want absent", format, got)
					}
					continue
				}
				if fmt.Sprintf("%v", got) != tc.wantKey {
					t.Errorf("-o %s: quarantine_not_loaded = %v, want %s", format, got, tc.wantKey)
				}
			}
		})
	}
}
