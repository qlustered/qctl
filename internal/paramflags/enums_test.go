package paramflags

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// generatedClient is independently generated from the same OpenAPI spec as
// metadata.gen.go. Comparing the two outputs catches either generator drifting.
const generatedClient = "../api/client.gen.go"

// dataParamsTypes are the two generated params structs the data commands
// derive their flags from. Both, not one: `download data` denies four
// parameters `get data` offers, so a check over only GetDataParams would miss
// an enum parameter that arrives on the export route alone.
func dataParamsTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(api.GetDataParams{}),
		reflect.TypeOf(api.PutDataForDownloadParams{}),
	}
}

// TestEveryNamedStringParamHasItsValues is the reason a new enum-valued query
// parameter cannot arrive unvalidated.
//
// KindForField flattens every named string type into KindString and Register
// gives it a plain string flag, so an enum parameter added by a regen grows a
// flag that accepts anything and reports the backend's 422 about a query
// parameter the user never typed. That is exactly how quarantine_hold arrived.
func TestEveryNamedStringParamHasItsValues(t *testing.T) {
	plainString := reflect.TypeOf("")
	checked := 0
	for _, pt := range dataParamsTypes() {
		for i := 0; i < pt.NumField(); i++ {
			f := pt.Field(i)
			wire := WireNameFromTag(f)
			if wire == "" || KindForField(f.Type) != KindString {
				continue
			}
			el := f.Type.Elem()
			if el == plainString {
				continue // free-form text: search_query, filter_by_field_value, …
			}
			checked++
			if _, ok := enumValuesForType(el); !ok {
				t.Errorf("%s.%s is the named string type %s with no api.StringEnumValues entry: "+
					"--%s would accept any value and report the backend's 422 instead",
					pt.Name(), f.Name, el.Name(), FlagNameForWire(wire))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no named string parameter was examined; this check cannot fail as written")
	}
}

func TestEnumValuesMatchTheGeneratedConstants(t *testing.T) {
	generated := generatedEnumMembers(t)
	if len(generated) == 0 {
		t.Fatalf("no enum constants parsed out of %s; this check cannot fail as written", generatedClient)
	}
	checked := map[string]bool{}
	for _, pt := range dataParamsTypes() {
		for i := 0; i < pt.NumField(); i++ {
			field := pt.Field(i)
			if KindForField(field.Type) != KindString || field.Type.Elem() == reflect.TypeOf("") {
				continue
			}
			name := field.Type.Elem().Name()
			if checked[name] {
				continue
			}
			checked[name] = true
			declared, ok := api.StringEnumValues[name]
			if !ok {
				continue // TestEveryNamedStringParamHasItsValues reports the missing key.
			}
			got, ok := generated[name]
			if !ok {
				t.Errorf("StringEnumValues[%q] has no constants in %s", name, generatedClient)
				continue
			}
			if !equalSets(declared, got) {
				t.Errorf("StringEnumValues[%q] has %v, %s declares %v", name, sorted(declared), generatedClient, sorted(got))
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no enum-valued data parameter was compared")
	}
}

// generatedEnumMembers collects every `const ( X SomeType = "value" )` in the
// generated client, grouped by the named type.
func generatedEnumMembers(t *testing.T) map[string][]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), generatedClient, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", generatedClient, err)
	}
	out := map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out[ident.Name] = append(out[ident.Name], value)
		}
	}
	return out
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := sorted(a), sorted(b)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// TestQuarantineHoldFlagMatchesTheWireName pins the hand-written constant to
// the derivation. The flag is derived, so nothing else would notice the two
// drifting apart: the validation would stop finding a flag that is registered
// under another name and would then never refuse anything.
func TestQuarantineHoldFlagMatchesTheWireName(t *testing.T) {
	if got := FlagNameForWire(QuarantineHoldWire); got != QuarantineHoldFlag {
		t.Errorf("FlagNameForWire(%q) = %q, want %q", QuarantineHoldWire, got, QuarantineHoldFlag)
	}
	found := false
	for _, f := range Derive(reflect.TypeOf(api.GetDataParams{}), nil) {
		if f.WireName == QuarantineHoldWire {
			found = true
			if f.FlagName != QuarantineHoldFlag {
				t.Errorf("derived flag name = %q, want %q", f.FlagName, QuarantineHoldFlag)
			}
			if _, ok := enumValuesForType(f.Type); !ok {
				t.Errorf("the derived %s flag carries type %s, which has no api.StringEnumValues entry",
					QuarantineHoldFlag, f.Type)
			}
		}
	}
	if !found {
		t.Errorf("GetDataParams has no %q parameter", QuarantineHoldWire)
	}
}

// The approval filters are derived from both data routes' params, so nothing
// else notices one of them leaving the bad-data-only inventory, or the reason
// help falling behind the enum it spells out.
func TestApprovalHoldFiltersAreBadDataOnlyOnBothRoutes(t *testing.T) {
	for _, pt := range dataParamsTypes() {
		byWire := map[string]Flag{}
		for _, f := range Derive(pt, nil) {
			byWire[f.WireName] = f
		}
		for wire, kind := range map[string]Kind{
			"held_for_approval": KindBool, "waiting_for_approval": KindBool, "approval_hold_reason": KindString,
		} {
			f, ok := byWire[wire]
			if !ok {
				t.Errorf("%s derives no %s flag", pt.Name(), wire)
				continue
			}
			if f.Kind != kind {
				t.Errorf("%s.%s: kind = %v, want %v", pt.Name(), wire, f.Kind, kind)
			}
			if help, ok := BadDataOnlyHelp(wire); !ok || !strings.Contains(help, "requires --is-bad-data") {
				t.Errorf("%s is not in the bad-data-only inventory with actionable help: %q", wire, help)
			}
		}
	}
	values, ok := api.StringEnumValues["ApprovalHoldReason"]
	if !ok || len(values) == 0 {
		t.Fatal("api.StringEnumValues has no ApprovalHoldReason members; this check cannot fail as written")
	}
	help, _ := BadDataOnlyHelp("approval_hold_reason")
	for _, v := range values {
		if !strings.Contains(help, v) {
			t.Errorf("--approval-hold-reason help does not name the value %q: %q", v, help)
		}
	}
}

// dataCmd is a stand-in for `get data` / `download data`: every derived flag,
// registered exactly as those two register them.
func dataCmd(t *testing.T) (*cobra.Command, []Flag) {
	t.Helper()
	cmd := &cobra.Command{Use: "data"}
	flags := Derive(reflect.TypeOf(api.GetDataParams{}), nil)
	Register(cmd, flags, func(string) string { return "" })
	return cmd, flags
}

func TestValidateDataFilterFlagsRefusesAValueOutsideTheEnum(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{QuarantineHoldFlag, "yes"},
		{QuarantineHoldFlag, "Hold"}, // the wire is lowercase; a near miss is still a miss
		{"filter-operator", "equal"},
		{"review-status", "approved"},
		{"approval-hold-reason", "duplicate"},
		{"approval-hold-reason", "held_for_approval"}, // a status bucket, not a reason
	} {
		cmd, flags := dataCmd(t)
		if err := cmd.Flags().Set("is-bad-data", "true"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Flags().Set(tc.flag, tc.value); err != nil {
			t.Fatal(err)
		}
		err := ValidateDataFilterFlags(cmd, flags)
		if err == nil {
			t.Errorf("--%s %q was accepted", tc.flag, tc.value)
			continue
		}
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("--%s %q: GetExitCode = %d, want %d", tc.flag, tc.value, got, qerrors.ExitBadUsage)
		}
	}
}

func TestValidateDataFilterFlagsAcceptsEveryDeclaredValue(t *testing.T) {
	seen := map[reflect.Type]bool{}
	for _, pt := range dataParamsTypes() {
		for _, enumFlag := range Derive(pt, nil) {
			values, ok := enumValuesForType(enumFlag.Type)
			if !ok || seen[enumFlag.Type] {
				continue
			}
			seen[enumFlag.Type] = true
			for _, v := range values {
				cmd := &cobra.Command{Use: "data"}
				flags := Derive(pt, nil)
				Register(cmd, flags, func(string) string { return "" })
				if err := cmd.Flags().Set("is-bad-data", "true"); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Flags().Set(enumFlag.FlagName, v); err != nil {
					t.Fatal(err)
				}
				if err := ValidateDataFilterFlags(cmd, flags); err != nil {
					t.Errorf("--%s %q is a declared value but was refused: %v", enumFlag.FlagName, v, err)
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no enum-valued data parameter was exercised")
	}
}

// An untouched enum flag holds pflag's empty default, which is not a member of
// two of the three sets. Only a flag the user changed is checked, or every
// plain `qctl get data <table>` would be refused.
func TestValidateDataFilterFlagsIgnoresUntouchedFlags(t *testing.T) {
	cmd, flags := dataCmd(t)
	if err := ValidateDataFilterFlags(cmd, flags); err != nil {
		t.Errorf("a command line with no filter flags was refused: %v", err)
	}
}

func TestEveryBadDataOnlyFlagRequiresIsBadData(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{QuarantineHoldFlag, string(api.QuarantineHoldHold)},
		{"passes-validation", "false"}, // false is a meaningful row filter
		{"awaiting-processing", "false"},
		{"dataset-rule-ids", "550e8400-e29b-41d4-a716-446655440000"},
		{"include-dataset-rule-ids", "true"},
		{"review-status", string(api.ReviewStatusFilterAccepted)},
		{"review-group-id", "550e8400-e29b-41d4-a716-446655440000"},
		{"review-change-history-id", "17"},
		{"held-for-approval", "false"}, // false selects every other row, and atlas refuses it too
		{"approval-hold-reason", string(api.ApprovalHoldReasonPossibleDuplicate)},
		{"waiting-for-approval", "false"}, // false selects every other row, and atlas refuses it too
		{"request-state", string(api.RequestStateFilterToReview)},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			cmd, flags := dataCmd(t)
			if err := cmd.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			err := ValidateDataFilterFlags(cmd, flags)
			if err == nil {
				t.Fatalf("--%s without --is-bad-data was accepted", tc.flag)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "--"+tc.flag) {
				t.Errorf("error does not name --%s: %v", tc.flag, err)
			}
		})
	}
}

// The pairing is checked only when the flag was actually typed: --is-bad-data
// is not made mandatory for every other filter by the presence of this check.
func TestBadDataOnlyPairingIgnoresUntouchedFlags(t *testing.T) {
	cmd, flags := dataCmd(t)
	if err := cmd.Flags().Set("search-query", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataFilterFlags(cmd, flags); err != nil {
		t.Errorf("a clean-rows search was refused: %v", err)
	}
}

func TestBadDataOnlyPairingAcceptsEveryFlagWithIsBadData(t *testing.T) {
	cmd, flags := dataCmd(t)
	for flag, value := range map[string]string{
		QuarantineHoldFlag:         string(api.QuarantineHoldRelease),
		"passes-validation":        "false",
		"awaiting-processing":      "true",
		"dataset-rule-ids":         "550e8400-e29b-41d4-a716-446655440000",
		"include-dataset-rule-ids": "true",
		"review-status":            string(api.ReviewStatusFilterAwaitingDecision),
		"review-group-id":          "550e8400-e29b-41d4-a716-446655440000",
		"review-change-history-id": "17",
		"held-for-approval":        "true",
		"approval-hold-reason":     string(api.ApprovalHoldReasonTableSetting),
		"waiting-for-approval":     "true",
		"request-state":            string(api.RequestStateFilterToReview),
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatalf("--%s: %v", flag, err)
		}
	}
	if err := cmd.Flags().Set("is-bad-data", "true"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataFilterFlags(cmd, flags); err != nil {
		t.Errorf("bad-data request was refused: %v", err)
	}
}

// request_view only chooses which buckets request_state reads. Atlas gives it
// a default and _reject_bad_data_only_params never names it, so a clean-rows
// request carrying it is not refused and it stays out of the inventory.
func TestRequestViewIsNotBadDataOnly(t *testing.T) {
	cmd, flags := dataCmd(t)
	if cmd.Flags().Lookup("request-view") == nil {
		t.Fatal("GetDataParams derives no --request-view flag; this check cannot fail as written")
	}
	if _, ok := BadDataOnlyHelp("request_view"); ok {
		t.Error("request_view is in the bad-data-only inventory, but atlas accepts it without is_bad_data")
	}
	if err := cmd.Flags().Set("request-view", string(api.Sender)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataFilterFlags(cmd, flags); err != nil {
		t.Errorf("--request-view without --is-bad-data was refused: %v", err)
	}
}

func TestFalseIncludeDatasetRuleIDsIsInactive(t *testing.T) {
	cmd, flags := dataCmd(t)
	if err := cmd.Flags().Set("include-dataset-rule-ids", "false"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataFilterFlags(cmd, flags); err != nil {
		t.Errorf("an explicitly disabled response option was refused: %v", err)
	}
}

func TestBadDataOnlyPairingAggregatesEveryOffendingFlag(t *testing.T) {
	cmd, flags := dataCmd(t)
	for _, flag := range []string{"passes-validation", "awaiting-processing"} {
		if err := cmd.Flags().Set(flag, "false"); err != nil {
			t.Fatal(err)
		}
	}
	err := ValidateDataFilterFlags(cmd, flags)
	if err == nil {
		t.Fatal("two bad-data-only flags without --is-bad-data were accepted")
	}
	for _, flag := range []string{"--passes-validation", "--awaiting-processing"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error does not name %s: %v", flag, err)
		}
	}
}

// A command that never registered --is-bad-data must not be told to pass it.
// The internal error is NOT exit 2: it is a qctl bug, not a user's.
func TestBadDataOnlyPairingReportsAMissingIsBadDataFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	cmd.Flags().String(QuarantineHoldFlag, "", "")
	if err := cmd.Flags().Set(QuarantineHoldFlag, "hold"); err != nil {
		t.Fatal(err)
	}
	err := validateBadDataOnlyPairings(cmd, []Flag{{
		WireName: QuarantineHoldWire,
		FlagName: QuarantineHoldFlag,
	}})
	if err == nil {
		t.Fatal("a command with no --is-bad-data flag silently accepted the pairing")
	}
	if got := qerrors.GetExitCode(err); got == qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d: an internal wiring error must not read as the user's mistake", got)
	}
}
