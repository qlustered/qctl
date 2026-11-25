package warning

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/warnings"
)

// ruleWarning is an unresolved rule_validation warning of cloud source 7 on
// table 42, offering actions, whose rule writes region and segment.
func ruleWarning(actions ...api.WarningAction) warnings.WarningFull {
	w := anomalyWarning(actions...)
	w.IssueType = api.IssueTypeEnumRuleValidation
	outputs := []string{"region", "segment"}
	w.RuleOutputColumns = &outputs
	ruleID := uuid.MustParse("aaaa1111-0000-0000-0000-000000000001")
	w.DatasetRuleID = &ruleID
	w.DataSourceModelID = intPtr(7)
	return w
}

// resolveBody decodes a recorded resolve request.
func resolveBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	return body
}

// The flags become ids, field_name_to_value and reason: --row-id
// de-duplicated in order, a --set value keeping any '=' after the first.
func TestResolveOverwriteRuleOutputsSendsTheFlags(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, ruleWarning(api.OverwriteRuleOutputs))
	var raw []byte
	registerResolvePost(mock, &raw)

	stdout, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "overwrite_rule_outputs",
		"--row-id", "1042", "--row-id", "1043,1042", "--set", "region=EMEA", "--set", "segment=a=b",
		"--reason", "known account")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stdout)
	}
	body := resolveBody(t, raw)
	if got := body["ids"]; !reflect.DeepEqual(got, []any{1042.0, 1043.0}) {
		t.Errorf("ids = %v, want [1042 1043]", got)
	}
	if got := body["field_name_to_value"]; !reflect.DeepEqual(got, map[string]any{"region": "EMEA", "segment": "a=b"}) {
		t.Errorf("field_name_to_value = %v", got)
	}
	if body["reason"] != "known account" {
		t.Errorf("reason = %v, want %q", body["reason"], "known account")
	}
	for _, want := range []string{
		"applied to the named rows; the warning stays open while another row carries it",
		"The values were written into the named rows",
		"qctl get data 42 --warning-item-id 55",
		"qctl get rule-ignores 42 --cloud-source-id 7 --rule aaaa1111-0000-0000-0000-000000000001",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
	// Neither row action calls resolve_one_warning, so the generic republish
	// note would be false.
	if strings.Contains(stdout, "republished") {
		t.Errorf("a row action reported the warning-wide republish:\n%s", stdout)
	}
}

func TestResolveIgnoreRuleForRowSendsIDsWithoutAReason(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, ruleWarning(api.IgnoreRuleForRow))
	var raw []byte
	registerResolvePost(mock, &raw)

	stdout, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "ignore_rule_for_row", "--row-id", "1042")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stdout)
	}
	body := resolveBody(t, raw)
	if got := body["ids"]; !reflect.DeepEqual(got, []any{1042.0}) {
		t.Errorf("ids = %v, want [1042]", got)
	}
	if v, ok := body["reason"]; ok && v != nil {
		t.Errorf("reason = %v, want none sent", v)
	}
	if body["field_name_to_value"] != nil {
		t.Errorf("field_name_to_value = %v, want none sent", body["field_name_to_value"])
	}
	for _, want := range []string{"warning 55 resolved", "no longer carry this warning"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
}

// A --set column the rule does not write is refused against the fetched
// warning, before anything is sent.
func TestResolveRefusesASetColumnTheRuleDoesNotWrite(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, ruleWarning(api.OverwriteRuleOutputs))
	posts := registerResolvePost(mock, nil)

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "overwrite_rule_outputs",
		"--row-id", "1042", "--set", "country=DE")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "region, segment") {
		t.Errorf("the refusal must name the output columns: %v", err)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; want none", *posts)
	}
}

// The same check runs on the -f path.
func TestResolveFileRefusesAFieldTheRuleDoesNotWrite(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, ruleWarning(api.OverwriteRuleOutputs))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: \"55\"\n"+
		"spec:\n  action: overwrite_rule_outputs\n  ids: [1042]\n  field_name_to_value:\n    country: DE\n")

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; want none", *posts)
	}
}

// Every flag-matrix refusal is exit 2 and costs no request: the mock serves
// nothing, so a request would fail with a different code.
func TestResolveValueFlagRefusals(t *testing.T) {
	manifest := "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: \"55\"\nspec:\n  action: ignore_rule_for_row\n  ids: [1]\n"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"missing --set":        {[]string{"--action", "overwrite_rule_outputs", "--row-id", "1"}, "needs --set"},
		"missing --row-id":     {[]string{"--action", "ignore_rule_for_row", "--reason", "x"}, "needs --row-id"},
		"--set not read":       {[]string{"--action", "ignore_rule_for_row", "--row-id", "1", "--set", "a=b"}, "does not read it"},
		"--row-id not read":    {[]string{"--action", "not_anomaly", "--row-id", "1"}, "does not read it"},
		"manifest-only action": {[]string{"--action", "overwrite_value", "--row-id", "1"}, "new_value"},
		"--set without =":      {[]string{"--action", "overwrite_rule_outputs", "--row-id", "1", "--set", "region"}, "<column>=<value>"},
		"--set empty column":   {[]string{"--action", "overwrite_rule_outputs", "--row-id", "1", "--set", "=x"}, "<column>=<value>"},
		"--set repeated":       {[]string{"--action", "overwrite_rule_outputs", "--row-id", "1", "--set", "a=1", "--set", "a=2"}, "twice"},
		"empty --reason":       {[]string{"--action", "ignore_rule_for_row", "--row-id", "1", "--reason", ""}, "cannot be empty"},
		"zero --row-id":        {[]string{"--action", "ignore_rule_for_row", "--row-id", "0"}, "positive"},
		"with --dry-run":       {[]string{"--action", "ignore_rule_for_row", "--row-id", "1", "--dry-run"}, "--dry-run"},
	} {
		t.Run(name, func(t *testing.T) {
			newWarningTestEnv(t)
			_, _, err := runWarningCommandSplit(t, append([]string{"warning", "resolve", "55"}, tc.args...)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message lacks %q: %v", tc.want, err)
			}
		})
	}
	t.Run("with -f", func(t *testing.T) {
		newWarningTestEnv(t)
		path := writeWarningManifest(t, manifest)
		_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--row-id", "1")
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.ids") {
			t.Errorf("err = %v, want exit 2 naming spec.ids", err)
		}
	})
}
