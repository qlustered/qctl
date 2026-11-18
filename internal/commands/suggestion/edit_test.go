package suggestion

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

var editURL = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/" + testSuggestionUUID.String()

func TestSuggestionEditRefusesRevalidationCandidatesBeforePatching(t *testing.T) {
	filterFile := writeRowFilterFile(t, "filter.yaml", euRowFilterYAML)
	for _, args := range [][]string{
		{"--set-param", "threshold=5"},
		{"--unset-param", "threshold"},
		{"--map-column", "email=customer_email"},
		{"--unset-column", "email"},
		{"--severity", "warning"},
		{"--row-filter-file", filterFile},
		{"--remove-row-filter"},
	} {
		t.Run(args[0], func(t *testing.T) {
			mock := newSuggestionTestEnv(t)
			gets, patches := 0, 0
			mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
				gets++
				s := sampleResolvedSuggestion(api.SuggestionStatePending)
				candidate := true
				s.IsRevalidationCandidate = &candidate
				testutil.RespondJSON(w, http.StatusOK, s)
			})
			mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
				patches++
				testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
			})

			command := []string{"suggestion", "edit", testSuggestionUUID.String(), "--table-id", "8"}
			out, err := runSuggestionCommand(t, append(command, args...)...)
			if err == nil {
				t.Fatal("expected a candidate refusal")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			if gets != 1 || patches != 0 {
				t.Errorf("sent %d GETs and %d PATCHes, want 1 GET and no PATCH", gets, patches)
			}
			for _, want := range []string{testSuggestionUUID.String(), "revalidation candidate", "TableRule", "qctl apply -f"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not name %q: %v", want, err)
				}
			}
			if out != "" {
				t.Errorf("refused edit printed a result: %q", out)
			}
		})
	}
}

func TestSuggestionEditPreservesBackendCandidateRefusal(t *testing.T) {
	ordinary := false
	for _, marker := range []*bool{nil, &ordinary} {
		name := "absent"
		if marker != nil {
			name = "false"
		}
		t.Run(name, func(t *testing.T) {
			mock := newSuggestionTestEnv(t)
			mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
				s := sampleResolvedSuggestion(api.SuggestionStatePending)
				s.IsRevalidationCandidate = marker
				testutil.RespondJSON(w, http.StatusOK, s)
			})
			mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
					"detail": "This is a revalidation candidate. Edit it by patching its rule.",
				})
			})

			out, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
				"--table-id", "8", "--severity", "warning")
			if err == nil {
				t.Fatal("expected the backend refusal")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			if !strings.Contains(err.Error(), "Edit it by patching its rule") {
				t.Errorf("lost the backend advice: %v", err)
			}
			if out != "" {
				t.Errorf("refused edit printed a result: %q", out)
			}
		})
	}
}

func TestSuggestionEditPreservesReadFailuresBeforePatching(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := newSuggestionTestEnv(t)
			mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, status, map[string]interface{}{"detail": "read refused"})
			})
			patches := 0
			mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
				patches++
				testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
			})

			_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
				"--table-id", "8", "--severity", "warning")
			if err == nil {
				t.Fatal("expected the read failure")
			}
			want := qerrors.GetExitCode(qerrors.FromHTTPStatus(status, "read refused"))
			if got := qerrors.GetExitCode(err); got != want {
				t.Errorf("exit code = %d, want %d", got, want)
			}
			if patches != 0 {
				t.Errorf("sent %d PATCHes after a failed read", patches)
			}
		})
	}
}

func TestSuggestionEditMergesParams(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.Params = &map[string]interface{}{"threshold": 3.0, "keep": "yes"}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "threshold=5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	params, ok := body["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("params missing, body: %s", raw)
	}
	// The backend REPLACES params outright, so an unnamed key must survive by
	// being read back and re-sent — otherwise --set-param silently drops it.
	if params["keep"] != "yes" {
		t.Errorf("unnamed key was dropped, params: %v", params)
	}
	if params["threshold"] != 5.0 {
		t.Errorf("threshold = %v, want 5 as a number", params["threshold"])
	}
}

// TestSuggestionEditMergesColumnMapping mirrors TestSuggestionEditMergesParams:
// rule_column_mapping is assigned outright by the same view, so --map-column
// has to merge locally for the same reason --set-param does.
func TestSuggestionEditMergesColumnMapping(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.RuleColumnMapping = map[string]string{"email": "old_email", "keep": "untouched_col"}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--map-column", "email=customer_email"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	mapping, ok := body["rule_column_mapping"].(map[string]interface{})
	if !ok {
		t.Fatalf("rule_column_mapping missing, body: %s", raw)
	}
	if mapping["keep"] != "untouched_col" {
		t.Errorf("unnamed mapping key was dropped, mapping: %v", mapping)
	}
	if mapping["email"] != "customer_email" {
		t.Errorf("email = %v, want customer_email", mapping["email"])
	}
}

func TestSuggestionEditUnsetsParamsAndColumnMappings(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.Params = &map[string]interface{}{"remove": "old", "keep": "yes"}
		s.RuleColumnMapping = map[string]string{"remove": "old_col", "keep": "untouched_col"}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--unset-param", "remove", "--unset-column", "remove"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body struct {
		Params  map[string]interface{} `json:"params"`
		Mapping map[string]string      `json:"rule_column_mapping"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if _, found := body.Params["remove"]; found {
		t.Errorf("removed param is still present: %s", raw)
	}
	if body.Params["keep"] != "yes" {
		t.Errorf("unnamed param was changed: %s", raw)
	}
	if _, found := body.Mapping["remove"]; found {
		t.Errorf("removed mapping is still present: %s", raw)
	}
	if body.Mapping["keep"] != "untouched_col" {
		t.Errorf("unnamed mapping was changed: %s", raw)
	}
}

func TestSuggestionEditRejectsSetUnsetConflictsBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"param", []string{"--set-param", "same=1", "--unset-param", "same"}},
		{"column", []string{"--map-column", "same=orders", "--unset-column", "same"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newSuggestionTestEnv(t)
			requests := 0
			mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
			})
			mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
			})

			args := []string{"suggestion", "edit", testSuggestionUUID.String(), "--table-id", "8"}
			args = append(args, tc.args...)
			_, err := runSuggestionCommand(t, args...)
			if err == nil {
				t.Fatal("expected a conflict refusal")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			if requests != 0 {
				t.Errorf("sent %d requests; flag conflicts must fail locally", requests)
			}
			if !strings.Contains(err.Error(), "both set and unset") {
				t.Errorf("error does not explain the conflict: %v", err)
			}
		})
	}
}

func TestSuggestionEditRejectsEmptyUnsetKeys(t *testing.T) {
	for _, flag := range []string{"--unset-param", "--unset-column"} {
		t.Run(flag, func(t *testing.T) {
			newSuggestionTestEnv(t)
			_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
				"--table-id", "8", flag, "")
			if err == nil {
				t.Fatal("expected an empty-key refusal")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
		})
	}
}

// TestSuggestionEditReadsBackOnce pins the restructure: both merges are served
// by a single GET, not one apiece.
func TestSuggestionEditReadsBackOnce(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	gets := 0
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		gets++
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.Params = &map[string]interface{}{"keep": "yes"}
		s.RuleColumnMapping = map[string]string{"keep": "untouched_col"}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "threshold=5", "--map-column", "email=customer_email"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gets != 1 {
		t.Errorf("read the suggestion back %d times, want exactly 1", gets)
	}
}

// TestSuggestionEditMergeToleratesEmptyCurrent covers the suggestion whose
// params and mapping are both absent: the merge must still send what was typed
// rather than failing on the nil.
func TestSuggestionEditMergeToleratesEmptyCurrent(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "threshold=5", "--map-column", "email=customer_email"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	p, ok := body["params"].(map[string]interface{})
	if !ok || p["threshold"] != 5.0 {
		t.Errorf("params = %v, want threshold 5", body["params"])
	}
	m, ok := body["rule_column_mapping"].(map[string]interface{})
	if !ok || m["email"] != "customer_email" {
		t.Errorf("rule_column_mapping = %v, want email customer_email", body["rule_column_mapping"])
	}
}

func TestSuggestionEditParsesValueTypes(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "n=5", "--set-param", "flag=true",
		"--set-param", "label=hello", "--set-param", "note=5 apples",
		// Exactly the argv the shell hands qctl for the string escape hatch
		// the Long documents (--set-param threshold='"5"'): the outer quotes
		// are the shell's, so the process sees key="5" and parseScalar's
		// json.Unmarshal reads a JSON string, not the number.
		"--set-param", `quoted="5"`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	p, ok := body["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("params missing, body: %s", raw)
	}
	if p["n"] != 5.0 {
		t.Errorf("n = %#v, want the number 5", p["n"])
	}
	if p["flag"] != true {
		t.Errorf("flag = %#v, want the boolean true", p["flag"])
	}
	if p["label"] != "hello" {
		t.Errorf("label = %#v, want the string hello", p["label"])
	}
	if p["note"] != "5 apples" {
		t.Errorf("note = %#v, want the string \"5 apples\"", p["note"])
	}
	if p["quoted"] != "5" {
		t.Errorf("quoted = %#v, want the string \"5\" — the Long's escape hatch must work as spelled", p["quoted"])
	}
}

func TestSuggestionEditPreservesNumberLexemes(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.Params = &map[string]interface{}{
			"untouched_big":     json.Number("9007199254740993"),
			"untouched_decimal": json.Number("1.20"),
		}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "typed_big=9007199254740993",
		"--set-param", "typed_decimal=1.20"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	for key, want := range map[string]string{
		"untouched_big":     "9007199254740993",
		"untouched_decimal": "1.20",
		"typed_big":         "9007199254740993",
		"typed_decimal":     "1.20",
	} {
		if got := string(body.Params[key]); got != want {
			t.Errorf("params[%q] = %s, want exact JSON number %s; body: %s", key, got, want, raw)
		}
	}
}

func TestSuggestionEditOmitsUntouchedFields(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	gets := 0
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		gets++
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})
	var raw []byte
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--severity", "warning"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if gets != 1 {
		t.Errorf("read the suggestion back %d times, want 1 to check for a candidate", gets)
	}
	// The severity the user typed must be SENT, as the enum WORD -- the wire
	// field is a RuleSeverity, not the boolean it replaced -- while params,
	// which was not typed, must stay null.
	if body["severity"] != string(api.RuleSeverityWarning) {
		t.Errorf("severity = %#v, want %q", body["severity"], api.RuleSeverityWarning)
	}
	if v, present := body["params"]; present && v != nil {
		t.Errorf("params must stay null when --set-param was not given, body: %s", raw)
	}
	if v, present := body["rule_column_mapping"]; present && v != nil {
		t.Errorf("rule_column_mapping must stay null when --map-column was not given, body: %s", raw)
	}
}

func TestSuggestionEditRequiresAFlag(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(), "--table-id", "8")
	if err == nil {
		t.Fatal("expected a refusal when no field flag is given")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	// The message enumerates the editable flags, so it goes stale silently
	// when one is renamed -- which is exactly what happened to the boolean
	// --severity replaced.
	for _, want := range []string{
		"--set-param", "--unset-param", "--map-column", "--unset-column", "--severity",
		"--row-filter-file", "--remove-row-filter",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the nothing-to-edit message must name %s: %v", want, err)
		}
	}
}

// TestSuggestionEditRejectsAnUnknownSeverity: --severity is sent verbatim as
// a RuleSeverity, and the backend answers an unknown value with a 422 whose
// object-shaped body the generated client cannot surface. Refusing locally
// costs no request and names the vocabulary.
func TestSuggestionEditRejectsAnUnknownSeverity(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	patched := 0
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
		patched++
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--severity", "alert")
	if err == nil {
		t.Fatal("expected a refusal for a severity outside the enum")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if patched != 0 {
		t.Errorf("PATCH was sent %d times; the refusal must come before any request", patched)
	}
	for _, want := range []string{"warning", "blocker", "as_declared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection does not name %q: %v", want, err)
		}
	}
}

// TestSuggestionEditRefusesTheRetiredBoolean: --treat-as-alert wrote a field
// PatchSuggestionRequest no longer has. Cobra would answer "unknown flag",
// which is a correct exit 2 that tells a script nothing about what to send
// instead, so the flag stays registered-but-hidden and the refusal carries
// the mapping -- the same treatment dataset_rules.ValidateManifestSeverity
// gives spec.treat_as_alert, with the same mapping and for the same reason.
func TestSuggestionEditRefusesTheRetiredBoolean(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want string
	}{
		{flag: "--treat-as-alert=true", want: "--severity as_declared"},
		{flag: "--treat-as-alert=false", want: "--severity warning"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			mock := newSuggestionTestEnv(t)
			patched := 0
			mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
				patched++
				testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
			})

			_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
				"--table-id", "8", tc.flag)
			if err == nil {
				t.Fatal("expected a refusal for the retired flag")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			if patched != 0 {
				t.Errorf("PATCH was sent %d times; the retired flag must reach no request", patched)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not name %q: %v", tc.want, err)
			}
		})
	}
}

// TestSuggestionEditSeverityFlagIsVisibleAndTheBooleanIsNot: the replacement
// has to be discoverable from --help, and the retired flag has to stay OUT of
// it -- a hidden flag that --help advertises would read as still supported.
func TestSuggestionEditSeverityFlagIsVisibleAndTheBooleanIsNot(t *testing.T) {
	cmd := newEditCommand()
	severity := cmd.Flags().Lookup("severity")
	if severity == nil {
		t.Fatal("suggestion edit must declare --severity")
	}
	if severity.Hidden {
		t.Error("--severity must be visible in --help")
	}
	for _, want := range []string{"warning", "blocker", "as_declared"} {
		if !strings.Contains(severity.Usage, want) {
			t.Errorf("--severity usage does not list %q\n%s", want, severity.Usage)
		}
	}
	retired := cmd.Flags().Lookup("treat-as-alert")
	if retired == nil {
		t.Fatal("--treat-as-alert must stay registered so its refusal can name the replacement")
	}
	if !retired.Hidden {
		t.Error("--treat-as-alert must be hidden: it cannot work, so --help must not offer it")
	}
	for _, name := range []string{"unset-param", "unset-column"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("suggestion edit must declare --%s", name)
		}
		if flag.Hidden {
			t.Errorf("--%s must be visible in --help", name)
		}
	}
}

func TestSuggestionEditBreadcrumbPreservesTableSelector(t *testing.T) {
	t.Run("table id", func(t *testing.T) {
		mock := newSuggestionTestEnv(t)
		registerPending(mock, testSuggestionUUID.String())
		mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
		})
		out, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
			"--table-id", "8", "--severity", "warning")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "Next: qctl suggestion accept " + testSuggestionUUID.String() + " --table-id 8"
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	})

	t.Run("table name", func(t *testing.T) {
		mock := newSuggestionTestEnv(t)
		registerPending(mock, testSuggestionUUID.String())
		const tableName = "O'Brien orders"
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("name"); got != tableName {
				t.Errorf("dataset name query = %q, want %q", got, tableName)
			}
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{"id": 8, "name": tableName}},
			})
		})
		mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
		})
		out, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
			"--table", tableName, "--severity", "warning")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "Next: qctl suggestion accept " + testSuggestionUUID.String() + ` --table 'O'\''Brien orders'`
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	})
}

func TestSuggestionEditRejectsMalformedSetParam(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "noequalssign")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("message must show the shape, got %q", err.Error())
	}
}

func TestSuggestionEditRejectsMalformedMapColumn(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--map-column", "noequalssign")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("message must show the shape, got %q", err.Error())
	}
}

// TestSuggestionEditRejectsAnEmptyKey covers splitKV's second guard
// (edit.go): strings.Cut finds the "=" in "=5" and reports found, so
// without the k == "" half qctl would happily send a parameter whose name is
// the empty string.
func TestSuggestionEditRejectsAnEmptyKey(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "=5")
	if err == nil {
		t.Fatal("expected a refusal for an empty parameter name")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("message must show the shape, got %q", err.Error())
	}
}

// TestSuggestionEditRejectsMalformedID keeps edit's refusal identical to the
// one accept and dismiss give for the same typo: parseIDs
// (internal/commands/suggestion/selector.go), exit 2, before anything is sent.
// Without it the id would first be parsed inside the client, whose fmt.Errorf
// carries no exit code and degrades to 1 — and only after Bootstrap had already
// run.
//
// The EXIT CODE is the assertion that carries this test. The substring cannot:
// "expected a full UUID" is in BOTH candidate messages — parseIDs
// (internal/commands/suggestion/selector.go) and the client's parseSuggestionID
// (internal/rule_suggestions/client.go) — so it only catches a drift into some
// unrelated message, never a fall-through to the client's own parser.
func TestSuggestionEditRejectsMalformedID(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "edit", "not-a-uuid",
		"--table-id", "8", "--set-param", "threshold=5")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "expected a full UUID") {
		t.Errorf("message must match accept/dismiss, got %q", err.Error())
	}
}

// euRowFilterYAML is a saved row filter: a description, one condition and a
// predicate.
const euRowFilterYAML = `description: EU orders
conditions:
  - column_name: region
    operator: eq
    value: EU
    case_sensitive: true
predicate:
  source: |
    def applies(row):
        return row["amount"] > 0
  columns: [amount]
`

// euRowFilterJSON is euRowFilterYAML spelled as JSON.
const euRowFilterJSON = `{"description": "EU orders",
 "conditions": [{"column_name": "region", "operator": "eq", "value": "EU", "case_sensitive": true}],
 "predicate": {"source": "def applies(row):\n    return row[\"amount\"] > 0\n", "columns": ["amount"]}}`

func writeRowFilterFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// editServer counts the reads and patches of editURL and keeps the last PATCH
// body.
type editServer struct {
	mock    *testutil.MockAPIServer
	gets    int
	patches int
	body    []byte
}

func newEditServer(t *testing.T) *editServer {
	t.Helper()
	s := &editServer{mock: newSuggestionTestEnv(t)}
	s.mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, _ *http.Request) {
		s.gets++
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})
	s.mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		s.patches++
		s.body, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})
	return s
}

// patchKeys decodes a PATCH body one level deep, so a test can tell an absent
// key from one sent as null.
func patchKeys(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	return body
}

func runEdit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := []string{"suggestion", "edit", testSuggestionUUID.String(), "--table-id", "8"}
	return runSuggestionCommand(t, append(command, args...)...)
}

func TestSuggestionEditReplacesTheRowFilterFromAFile(t *testing.T) {
	for _, tc := range []struct{ name, file, content string }{
		{"yaml", "filter.yaml", euRowFilterYAML},
		{"json", "filter.json", euRowFilterJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newEditServer(t)
			if _, err := runEdit(t, "--row-filter-file", writeRowFilterFile(t, tc.file, tc.content)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The read that refuses a revalidation candidate still runs.
			if s.gets != 1 || s.patches != 1 {
				t.Fatalf("sent %d GETs and %d PATCHes, want 1 of each", s.gets, s.patches)
			}

			body := patchKeys(t, s.body)
			raw, present := body["row_filter"]
			if !present {
				t.Fatalf("row_filter missing, body: %s", s.body)
			}
			var got api.DescribedRowFilter
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("row_filter is not a DescribedRowFilter: %v (%s)", err, raw)
			}
			if got.Description != "EU orders" {
				t.Errorf("description = %q, want %q", got.Description, "EU orders")
			}
			if got.Conditions == nil || len(*got.Conditions) != 1 {
				t.Fatalf("conditions = %s, want one", raw)
			}
			c := (*got.Conditions)[0]
			if c.ColumnName != "region" || c.Operator != "eq" || c.Value == nil || *c.Value != "EU" ||
				c.CaseSensitive == nil || !*c.CaseSensitive {
				t.Errorf("condition = %s, want region eq EU, case sensitive", raw)
			}
			wantSource := "def applies(row):\n    return row[\"amount\"] > 0\n"
			if got.Predicate == nil || got.Predicate.Source != wantSource ||
				len(got.Predicate.Columns) != 1 || got.Predicate.Columns[0] != "amount" {
				t.Errorf("predicate = %s, want the file's source over [amount]", raw)
			}

			// Fields the edit did not name keep their pre-existing wire shape.
			for _, key := range []string{"params", "rule_column_mapping"} {
				if v, present := body[key]; !present || string(v) != "null" {
					t.Errorf("%s = %s (present %t), want null; body: %s", key, v, present, s.body)
				}
			}
			if _, present := body["severity"]; present {
				t.Errorf("severity must be absent when --severity was not given, body: %s", s.body)
			}
		})
	}
}

func TestSuggestionEditRemovesTheRowFilter(t *testing.T) {
	s := newEditServer(t)
	if _, err := runEdit(t, "--remove-row-filter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.gets != 1 || s.patches != 1 {
		t.Fatalf("sent %d GETs and %d PATCHes, want 1 of each", s.gets, s.patches)
	}
	body := patchKeys(t, s.body)
	if v, present := body["row_filter"]; !present || string(v) != "null" {
		t.Errorf("row_filter = %s (present %t), want null; body: %s", v, present, s.body)
	}
	if _, present := body["severity"]; present {
		t.Errorf("severity must be absent when --severity was not given, body: %s", s.body)
	}
}

// An absent row_filter keeps the stored filter; null would delete it.
func TestSuggestionEditLeavesTheRowFilterOutUnlessAFlagNamesIt(t *testing.T) {
	for name, args := range map[string][]string{
		"severity only":           {"--severity", "warning"},
		"remove-row-filter=false": {"--severity", "warning", "--remove-row-filter=false"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newEditServer(t)
			if _, err := runEdit(t, args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, present := patchKeys(t, s.body)["row_filter"]; present {
				t.Errorf("row_filter must be absent, body: %s", s.body)
			}
		})
	}
}

func TestSuggestionEditRefusesBadRowFilterInputBeforeAnyRequest(t *testing.T) {
	valid := writeRowFilterFile(t, "filter.yaml", euRowFilterYAML)
	file := func(content string) string { return writeRowFilterFile(t, "filter.yaml", content) }
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"both flags", []string{"--row-filter-file", valid, "--remove-row-filter"}, "cannot use both"},
		{"both flags, remove false", []string{"--row-filter-file", valid, "--remove-row-filter=false"}, "cannot use both"},
		{"empty path", []string{"--row-filter-file", ""}, "names no file"},
		{"no description", []string{"--row-filter-file",
			file("conditions: [{column_name: region, operator: eq, value: EU}]\n")}, "needs a description"},
		{"unknown key", []string{"--row-filter-file", file(euRowFilterYAML + "limit: 5\n")}, "limit"},
		{"two documents", []string{"--row-filter-file", file(euRowFilterYAML + "---\n" + euRowFilterYAML)}, "2 documents"},
		{"empty file", []string{"--row-filter-file", file("")}, "is empty"},
		{"remove-row-filter=false alone", []string{"--remove-row-filter=false"}, "nothing to edit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newEditServer(t)
			_, err := runEdit(t, tc.args...)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
			}
			if s.gets != 0 || s.patches != 0 || len(s.mock.Requests()) != 0 {
				t.Errorf("sent %d GETs and %d PATCHes (%v), want no request at all",
					s.gets, s.patches, s.mock.Requests())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// row_filters.ReadFile reports an unreadable path as an I/O failure, exit 1,
// as 'table-rule test-filter --row-filter-file' does; it is still refused
// before any request.
func TestSuggestionEditUnreadableRowFilterFileExitsOneBeforeAnyRequest(t *testing.T) {
	s := newEditServer(t)
	_, err := runEdit(t, "--row-filter-file", filepath.Join(t.TempDir(), "missing.yaml"))
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitGenericError, err)
	}
	if len(s.mock.Requests()) != 0 {
		t.Errorf("requests = %v, want none", s.mock.Requests())
	}
}

// The backend compiles a replacement against the table's columns and answers
// 422 with an object-shaped detail when it does not compile.
func TestSuggestionEditRowFilter422ExitsTwo(t *testing.T) {
	s := newEditServer(t)
	s.mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, _ *http.Request) {
		s.patches++
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "Row filter invalid: unknown column 'region'",
				"error_code": "ERR_VALIDATION",
				"context":    map[string]interface{}{"column": "region"},
			},
		})
	})

	out, err := runEdit(t, "--row-filter-file", writeRowFilterFile(t, "filter.yaml", euRowFilterYAML))
	if err == nil {
		t.Fatal("expected the 422")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
	if s.gets != 1 || s.patches != 1 {
		t.Errorf("sent %d GETs and %d PATCHes, want 1 of each", s.gets, s.patches)
	}
	if !strings.Contains(err.Error(), "Row filter invalid") {
		t.Errorf("lost the backend message: %v", err)
	}
	if out != "" {
		t.Errorf("refused edit printed a result: %q", out)
	}
}

// TestSuggestionEditRefusesToCarryMaskedParams: a param the edit leaves alone
// that reads back as mask text exits 2 before any PATCH.
func TestSuggestionEditRefusesToCarryMaskedParams(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", editURL, func(w http.ResponseWriter, r *http.Request) {
		s := sampleResolvedSuggestion(api.SuggestionStatePending)
		s.Params = &map[string]interface{}{"threshold": "•••••", "keep": "•••••"}
		testutil.RespondJSON(w, http.StatusOK, s)
	})
	patched := false
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		patched = true
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	_, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "threshold=5")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "the value of: keep.") {
		t.Errorf("error = %v, want it to name keep", err)
	}
	if patched {
		t.Error("PATCH sent, want none")
	}

	if _, err := runSuggestionCommand(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--set-param", "threshold=5", "--unset-param", "keep"); err != nil {
		t.Fatalf("every masked param edited: %v", err)
	}
	if !patched {
		t.Error("no PATCH, want one once every masked param is edited")
	}
}
