package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func keyStepDetail(step *api.KeyStep, keys ...string) *datasets.TableSchemaDetail {
	detail := &datasets.TableSchemaDetail{DatasetID: 42, KeyStep: step}
	if len(keys) > 0 {
		detail.KeyColumns = &keys
	}
	if step != nil {
		detail.KeyColumnCandidates = &api.KeyColumnCandidatesSchema{
			RowsChecked: 500, WholeFile: true,
			Candidates: []api.KeyColumnSetCountsSchema{{Columns: []string{"order_id"}, DuplicateRows: 2, IdenticalRows: 2}},
		}
	}
	return detail
}

func keyStepPtr(step api.KeyStep) *api.KeyStep { return &step }

func TestCheckKeyStepRefusesWhatTheKeyStepRefuses(t *testing.T) {
	yes := true
	neither := AlertResolutionSpec{Action: "apply_migration"}
	keys := AlertResolutionSpec{Action: "apply_migration", KeyColumns: []string{"order_id"}}
	keyless := AlertResolutionSpec{Action: "apply_migration", NoKeyColumns: &yes}
	required, optional := keyStepPtr(api.Required), keyStepPtr(api.Optional)
	for _, tc := range []struct {
		name   string
		spec   AlertResolutionSpec
		detail *datasets.TableSchemaDetail
		want   int
	}{
		{"required, neither", neither, keyStepDetail(required), qerrors.ExitBadUsage},
		{"required, key columns", keys, keyStepDetail(required), qerrors.ExitSuccess},
		{"required, keyless", keyless, keyStepDetail(required), qerrors.ExitSuccess},
		{"optional, neither", neither, keyStepDetail(optional, "id"), qerrors.ExitSuccess},
		{"optional, key columns", keys, keyStepDetail(optional, "id"), qerrors.ExitSuccess},
		{"optional, keyless", keyless, keyStepDetail(optional, "id"), qerrors.ExitBadUsage},
		{"null, neither", neither, keyStepDetail(nil, "id"), qerrors.ExitSuccess},
		{"null, key columns", keys, keyStepDetail(nil, "id"), qerrors.ExitBadUsage},
		{"null, keyless", keyless, keyStepDetail(nil), qerrors.ExitBadUsage},
		{"unread", neither, nil, qerrors.ExitSuccess},
		{"another action", AlertResolutionSpec{Action: "discard_migration"}, keyStepDetail(required), qerrors.ExitSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.CheckKeyStep(tc.detail, KeyChoiceFlags)
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Fatalf("exit = %d, want %d: %v", got, tc.want, err)
			}
		})
	}
}

func TestRequiredStepRefusalListsTheCandidatesAndBothForms(t *testing.T) {
	spec := AlertResolutionSpec{Action: "apply_migration"}
	detail := keyStepDetail(keyStepPtr(api.Required))
	for form, want := range map[KeyChoiceForm][]string{
		KeyChoiceFlags:    {"--key-columns <a,b>", "--no-key-columns", "order_id"},
		KeyChoiceManifest: {"spec.key_columns", "spec.no_key_columns: true", "order_id"},
	} {
		err := spec.CheckKeyStep(detail, form)
		for _, w := range want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("refusal missing %q: %v", w, err)
			}
		}
	}
}

func TestRequiredStepRefusalSuggestsExplicitPreviousKeysOnlyWhenReusable(t *testing.T) {
	yes, no := true, false
	for _, allowed := range []*bool{&yes, &no, nil} {
		for _, form := range []KeyChoiceForm{KeyChoiceFlags, KeyChoiceManifest} {
			detail := keyStepDetail(keyStepPtr(api.Required))
			previous := []string{"old_id"}
			detail.PreviousKeyColumns, detail.PreselectPreviousKeyColumns = &previous, allowed
			spec := AlertResolutionSpec{Action: "apply_migration"}
			err := spec.CheckKeyStep(detail, form)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("must still require an explicit key choice: %v", err)
			}
			want := allowed != nil && *allowed
			if strings.Contains(err.Error(), "Previous key columns (old_id) can be reused: set "+form.Columns+" explicitly") != want {
				t.Errorf("reusable=%v: %v", allowed, err)
			}
		}
	}
}

func TestKeyOutcomeNamesWhatTheApplyDoesToTheKeys(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name   string
		spec   AlertResolutionSpec
		detail *datasets.TableSchemaDetail
		want   string
	}{
		{"set", AlertResolutionSpec{KeyColumns: []string{"a", "b"}}, keyStepDetail(keyStepPtr(api.Required)), "sets them to a, b. Applying locks"},
		{"keyless", AlertResolutionSpec{NoKeyColumns: &yes}, keyStepDetail(keyStepPtr(api.Required)), "keyless. Applying locks"},
		{"keep", AlertResolutionSpec{}, keyStepDetail(keyStepPtr(api.Optional), "id"), "keeps id. Applying locks"},
		{"replace", AlertResolutionSpec{KeyColumns: []string{"a"}}, keyStepDetail(keyStepPtr(api.Optional), "id"), "replaces id with a."},
		{"locked", AlertResolutionSpec{}, keyStepDetail(nil, "id"), ""},
		{"unread, nothing sent", AlertResolutionSpec{}, nil, ""},
		{"unread, keys sent", AlertResolutionSpec{KeyColumns: []string{"a"}}, nil, "if the table is at its key step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.spec.KeyOutcome(tc.detail)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Errorf("KeyOutcome = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestApplyMigrationSkeletonAtTheRequiredStep(t *testing.T) {
	a := &AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders"}
	out := Skeleton(a, "apply_migration", keyStepDetail(keyStepPtr(api.Required)))
	for _, want := range []string{"\n  key_columns:\n  # no_key_columns: true\n", "# key_step: required", "#   order_id"} {
		if !strings.Contains(out, want) {
			t.Errorf("skeleton missing %q:\n%s", want, out)
		}
	}
	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("skeleton does not parse: %v\n%s", err, out)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Fatalf("unedited skeleton fails Validate: %v", err)
	}
	if code := qerrors.GetExitCode(res.Spec.CheckKeyStep(keyStepDetail(keyStepPtr(api.Required)), KeyChoiceManifest)); code != qerrors.ExitBadUsage {
		t.Errorf("unedited skeleton passes the key pre-flight: exit %d", code)
	}
	if strings.Contains(out, "  # key_columns:\n") {
		t.Errorf("key_columns is also left commented:\n%s", out)
	}
}

func TestApplyMigrationSkeletonDropsKeyFieldsAtANullStep(t *testing.T) {
	out := Skeleton(&AlertFull{ID: 9}, "apply_migration", keyStepDetail(nil, "id"))
	if strings.Contains(out, "  # key_columns:") || strings.Contains(out, "  # no_key_columns:") || !strings.Contains(out, "# key_step: none") {
		t.Errorf("a null key step still offers the key fields:\n%s", out)
	}
	unread := Skeleton(&AlertFull{ID: 9}, "apply_migration", nil)
	if !strings.Contains(unread, "  # key_columns:\n") || !strings.Contains(unread, "  # no_key_columns:\n") {
		t.Errorf("an unread key step must leave both fields commented:\n%s", unread)
	}
}

func TestApplyMigrationKeyArgsFollowTheStep(t *testing.T) {
	if suffix, _ := ApplyMigrationKeyArgs(42, keyStepPtr(api.Required), true); suffix != " --key-columns <a,b>" {
		t.Errorf("required suffix = %q", suffix)
	}
	for name, step := range map[string]*api.KeyStep{"optional": keyStepPtr(api.Optional), "null": nil} {
		if suffix, _ := ApplyMigrationKeyArgs(42, step, true); suffix != "" {
			t.Errorf("%s suffix = %q, want none", name, suffix)
		}
	}
	if _, note := ApplyMigrationKeyArgs(42, nil, false); !strings.Contains(note, "qctl describe table-schema 42") {
		t.Errorf("unread note = %q", note)
	}
}
