package get

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func TestDataFlagsDerivation(t *testing.T) {
	flags := dataFlags()
	byWire := map[string]paramflags.Flag{}
	for _, f := range flags {
		byWire[f.WireName] = f
	}

	// Denylisted params must be absent.
	for _, denied := range []string{"full_values", "oauth_session", "alert_item_id"} {
		if _, ok := byWire[denied]; ok {
			t.Errorf("denylisted param %q was derived as a flag", denied)
		}
	}
	// A representative scalar/bool/slice set must be present with the right kind.
	wantKinds := map[string]paramflags.Kind{
		"page":              paramflags.KindInt,
		"limit":             paramflags.KindInt,
		"max_cell_chars":    paramflags.KindInt,
		"is_bad_data":       paramflags.KindBool,
		"show_metadata":     paramflags.KindBool,
		"search_query":      paramflags.KindString,
		"filter_operator":   paramflags.KindString, // *FilterOperator (named string)
		"order_by":          paramflags.KindStringSlice,
		"fields":            paramflags.KindStringSlice,
		"history_ids_":      paramflags.KindIntSlice,
		"dataset_rule_ids_": paramflags.KindUUIDSlice,
	}
	for wire, kind := range wantKinds {
		f, ok := byWire[wire]
		if !ok {
			t.Errorf("expected flag for %q to be derived", wire)
			continue
		}
		if f.Kind != kind {
			t.Errorf("%q: kind = %v, want %v", wire, f.Kind, kind)
		}
	}
}

func TestRegisterDataFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)

	mustHave := []string{
		"show-metadata", "include-history-flags", "include-dataset-rule-ids",
		"is-bad-data", "dataset-rule-ids", "max-cell-chars", "page", "limit",
		"order-by", "fields",
	}
	for _, name := range mustHave {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s was not registered", name)
		}
	}
	mustNotHave := []string{"full-values", "oauth-session"}
	for _, name := range mustNotHave {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("denylisted flag --%s should not be registered", name)
		}
	}
	// alert_item_id is the one denylist entry that still gets a flag. It is
	// denylisted because its GetDataParams field cannot carry a value, not
	// because the parameter is unwanted, so RegisterDataFlags hand-registers it
	// and datasets.WithAlertItemID puts it on the wire.
	if cmd.Flags().Lookup(paramflags.AlertItemIDFlag) == nil {
		t.Errorf("--%s must be hand-registered even though it is denylisted", paramflags.AlertItemIDFlag)
	}
}

// The denylist's real job for alert_item_id: keep it out of the params struct.
// Writing it there is what panics the generated request builder.
func TestBuildDataParamsNeverSetsAlertItemID(t *testing.T) {
	cmd := buildTestDataCmd(t, []string{"--is-bad-data", "--alert-item-id", "8817"})
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.AlertItemID != nil {
		t.Error("BuildDataParams wrote GetDataParams.AlertItemID; that field is unsendable " +
			"and the generated builder panics on it")
	}
}

// buildTestDataCmd registers the data flags and parses args against them.
func buildTestDataCmd(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd
}

func TestBuildDataParamsDefaults(t *testing.T) {
	cmd := buildTestDataCmd(t, nil)
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.Page == nil || *params.Page != 1 {
		t.Errorf("Page = %v, want 1", params.Page)
	}
	if params.Limit == nil || *params.Limit != 20 {
		t.Errorf("Limit = %v, want 20", params.Limit)
	}
	// Unset flags stay nil so backend defaults are preserved.
	if params.MaxCellChars != nil {
		t.Errorf("MaxCellChars = %v, want nil (omitted when not provided)", params.MaxCellChars)
	}
	if params.ShowMetadata != nil {
		t.Errorf("ShowMetadata = %v, want nil", params.ShowMetadata)
	}
}

func TestBuildDataParamsSetsChangedFlags(t *testing.T) {
	u := "550e8400-e29b-41d4-a716-446655440000"
	cmd := buildTestDataCmd(t, []string{
		"--show-metadata",
		"--is-bad-data",
		"--include-dataset-rule-ids",
		"--max-cell-chars", "50",
		"--page", "3",
		"--order-by", "a", "--order-by", "b",
		"--dataset-rule-ids", u,
	})
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.ShowMetadata == nil || !*params.ShowMetadata {
		t.Errorf("ShowMetadata = %v, want true", params.ShowMetadata)
	}
	if params.IsBadData == nil || !*params.IsBadData {
		t.Errorf("IsBadData = %v, want true", params.IsBadData)
	}
	if params.IncludeDatasetRuleIds == nil || !*params.IncludeDatasetRuleIds {
		t.Errorf("IncludeDatasetRuleIds = %v, want true", params.IncludeDatasetRuleIds)
	}
	if params.MaxCellChars == nil || *params.MaxCellChars != 50 {
		t.Errorf("MaxCellChars = %v, want 50", params.MaxCellChars)
	}
	if params.Page == nil || *params.Page != 3 {
		t.Errorf("Page = %v, want 3", params.Page)
	}
	if params.Limit == nil || *params.Limit != 20 {
		t.Errorf("Limit = %v, want 20 (default when unchanged)", params.Limit)
	}
	if params.OrderBy == nil || len(*params.OrderBy) != 2 || (*params.OrderBy)[0] != "a" {
		t.Errorf("OrderBy = %v, want [a b]", params.OrderBy)
	}
	if params.DatasetRuleIds == nil || len(*params.DatasetRuleIds) != 1 ||
		(*params.DatasetRuleIds)[0] != uuid.MustParse(u) {
		t.Errorf("DatasetRuleIds = %v, want [%s]", params.DatasetRuleIds, u)
	}
}

func TestBuildDataParamsRejectsBadUUID(t *testing.T) {
	cmd := buildTestDataCmd(t, []string{"--dataset-rule-ids", "not-a-uuid"})
	if _, err := BuildDataParams(cmd); err == nil {
		t.Errorf("expected error for invalid UUID")
	}
}

func TestBuildDataParamsStringAndIntSlice(t *testing.T) {
	cmd := buildTestDataCmd(t, []string{
		"--search-query", "hello world",
		"--filter-operator", "equals",
		"--history-ids", "1,2",
	})
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	// KindString: SearchQuery (*string)
	if params.SearchQuery == nil || *params.SearchQuery != "hello world" {
		t.Errorf("SearchQuery = %v, want %q", params.SearchQuery, "hello world")
	}
	// KindString: FilterOperator (*FilterOperator, named string type)
	if params.FilterOperator == nil || *params.FilterOperator != api.FilterOperator("equals") {
		t.Errorf("FilterOperator = %v, want %q", params.FilterOperator, "equals")
	}
	// KindIntSlice: HistoryIds (*[]int)
	if params.HistoryIds == nil || len(*params.HistoryIds) != 2 ||
		(*params.HistoryIds)[0] != 1 || (*params.HistoryIds)[1] != 2 {
		t.Errorf("HistoryIds = %v, want [1 2]", params.HistoryIds)
	}
}

func TestBuildDataParamsStripsKindRefs(t *testing.T) {
	const u = "5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"
	cmd := buildTestDataCmd(t, []string{
		"--history-ids", "history/1,2",
		"--dataset-rule-ids", "table-rule/" + u,
		"--stored-item-id", "file/9",
		"--comment-thread-id", "comment-thread/" + u,
	})
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.HistoryIds == nil || len(*params.HistoryIds) != 2 || (*params.HistoryIds)[0] != 1 {
		t.Errorf("HistoryIds = %v, want [1 2]", params.HistoryIds)
	}
	if params.DatasetRuleIds == nil || len(*params.DatasetRuleIds) != 1 || (*params.DatasetRuleIds)[0] != uuid.MustParse(u) {
		t.Errorf("DatasetRuleIds = %v, want [%s]", params.DatasetRuleIds, u)
	}
	if params.StoredItemID == nil || *params.StoredItemID != 9 {
		t.Errorf("StoredItemID = %v, want 9", params.StoredItemID)
	}
	if params.CommentThreadID == nil || *params.CommentThreadID != uuid.MustParse(u) {
		t.Errorf("CommentThreadID = %v, want %s", params.CommentThreadID, u)
	}
}

// TestNoParamIsSilentlyUnsupported is a tripwire. The reflection was silently
// lossy: a scalar UUID parameter classified as KindUnsupported and vanished
// with no flag, no error and no test failure. An unsupported kind must be as
// visible as a denylist entry.
func TestNoParamIsSilentlyUnsupported(t *testing.T) {
	tp := reflect.TypeOf(api.GetDataParams{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		wire := paramflags.WireNameFromTag(f)
		if wire == "" || dataFlagDenylist[wire] {
			continue
		}
		if paramflags.KindForField(f.Type) == paramflags.KindUnsupported {
			t.Errorf("GetDataParams.%s (%s, %s) classifies as paramflags.KindUnsupported and is dropped "+
				"with no flag and no error.\nEither add support to paramflags.KindForField or record it "+
				"in dataFlagDenylist with a reason.", f.Name, wire, f.Type)
		}
	}
}

func TestScalarUUIDParamsDeriveFlags(t *testing.T) {
	byWire := map[string]paramflags.Flag{}
	for _, f := range dataFlags() {
		byWire[f.WireName] = f
	}
	for _, wire := range []string{"comment_thread_id", "review_group_id"} {
		f, ok := byWire[wire]
		if !ok {
			t.Errorf("no flag derived for scalar UUID param %q", wire)
			continue
		}
		if f.Kind != paramflags.KindUUID {
			t.Errorf("%q: kind = %v, want KindUUID", wire, f.Kind)
		}
	}
}

func TestBuildDataParamsSetsScalarUUID(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)
	id := "0190bbbb-1111-2222-3333-444444444444"
	if err := cmd.Flags().Set("comment-thread-id", id); err != nil {
		t.Fatalf("Set: %v", err)
	}
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.CommentThreadID == nil || params.CommentThreadID.String() != id {
		t.Errorf("CommentThreadID = %v, want %s", params.CommentThreadID, id)
	}
}

// TestBuildDataParamsRejectsBadScalarUUID asserts the exit code, not the
// message: a client-side flag validation failure is exit 2, and a bare
// fmt.Errorf here would silently degrade it to 1 while still reading fine.
func TestBuildDataParamsRejectsBadScalarUUID(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)
	if err := cmd.Flags().Set("comment-thread-id", "not-a-uuid"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, err := BuildDataParams(cmd)
	if err == nil {
		t.Fatal("expected an error for a malformed UUID")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (bad usage)", got, qerrors.ExitBadUsage)
	}
}

// --quarantine-hold replaced the boolean --keep-in-quarantine in the
// 2026-09-10 spec. Both commands derive it from the params struct, so the
// rename took no code at all -- and arrived with the generic passthrough help
// and no client-side check. These tests are what the rename should have cost.
func TestQuarantineHoldFlagIsCurated(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)

	if cmd.Flags().Lookup("keep-in-quarantine") != nil {
		t.Errorf("--keep-in-quarantine is registered; the parameter it derived from is gone")
	}
	f := cmd.Flags().Lookup(paramflags.QuarantineHoldFlag)
	if f == nil {
		t.Fatalf("--%s was not registered", paramflags.QuarantineHoldFlag)
	}
	if f.Usage != paramflags.QuarantineHoldHelp {
		t.Errorf("--%s help = %q, want the shared curated line %q",
			paramflags.QuarantineHoldFlag, f.Usage, paramflags.QuarantineHoldHelp)
	}
	for _, want := range []string{
		string(api.QuarantineHoldHold),
		string(api.QuarantineHoldRelease),
		string(api.QuarantineHoldUndecided),
	} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("--%s help does not name the value %q: %q", paramflags.QuarantineHoldFlag, want, f.Usage)
		}
	}
}

func TestEveryBadDataOnlyFlagHasActionableHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)
	for _, name := range []string{
		"quarantine-hold", "passes-validation", "awaiting-processing", "dataset-rule-ids",
		"include-dataset-rule-ids", "review-status", "review-group-id", "review-change-history-id",
		"held-for-approval", "approval-hold-reason", "waiting-for-approval", "request-state",
		"problem-kind", "problem-issue-type", "problem-dataset-rule-id", "problem-field-names",
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("--%s is not registered", name)
			continue
		}
		if !strings.Contains(flag.Usage, "requires --is-bad-data") {
			t.Errorf("--%s help does not explain its pairing: %q", name, flag.Usage)
		}
	}
}

func TestBuildDataParamsSetsQuarantineHold(t *testing.T) {
	cmd := buildTestDataCmd(t, []string{"--is-bad-data", "--quarantine-hold", "release"})
	params, err := BuildDataParams(cmd)
	if err != nil {
		t.Fatalf("BuildDataParams: %v", err)
	}
	if params.QuarantineHold == nil || *params.QuarantineHold != api.QuarantineHoldRelease {
		t.Errorf("QuarantineHold = %v, want %q", params.QuarantineHold, api.QuarantineHoldRelease)
	}
}

// Every refusal must precede every request: `get data` resolves a table name
// with a request of its own before it ever asks for a page. This table includes
// false-valued boolean filters because false is still a real quarantine-row
// predicate, plus the review-change filter added after the original guard.
func TestGetDataRefusesBadDataOnlyFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"value outside the enum", []string{"1", "--is-bad-data", "--quarantine-hold", "yes"}},
		{"quarantine hold", []string{"1", "--quarantine-hold", "hold"}},
		{"passes validation false", []string{"1", "--passes-validation=false"}},
		{"awaiting processing false", []string{"1", "--awaiting-processing=false"}},
		{"dataset rule ids", []string{"1", "--dataset-rule-ids", "550e8400-e29b-41d4-a716-446655440000"}},
		{"include dataset rule ids", []string{"1", "--include-dataset-rule-ids"}},
		{"review status", []string{"1", "--review-status", "accepted"}},
		{"review group id", []string{"1", "--review-group-id", "550e8400-e29b-41d4-a716-446655440000"}},
		{"review change history id", []string{"1", "--review-change-history-id", "17"}},
		{"held for approval", []string{"1", "--held-for-approval"}},
		{"held for approval false", []string{"1", "--held-for-approval=false"}},
		{"approval hold reason", []string{"1", "--approval-hold-reason", "possible_duplicate"}},
		{"reason outside the enum", []string{"1", "--is-bad-data", "--approval-hold-reason", "duplicate"}},
		{"waiting for approval", []string{"1", "--waiting-for-approval"}},
		{"waiting for approval false", []string{"1", "--waiting-for-approval=false"}},
		{"request state", []string{"1", "--request-state", "to_review"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := dataTestMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, sampleGetData())
				})

			out, err := execData(t, tc.args)
			if err == nil {
				t.Fatalf("expected refusal, got success:\n%s", out)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			for _, req := range mock.Requests() {
				if strings.Contains(req, "/data") {
					t.Errorf("refusal must precede every request; saw %q", req)
				}
			}
		})
	}
}

// With --is-bad-data both approval-hold filters reach the query string as the
// wire names atlas reads, false included.
func TestGetDataSendsTheApprovalHoldFilters(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantHeld   string
		wantReason string
	}{
		{"both", []string{"--held-for-approval", "--approval-hold-reason", "possible_duplicate"},
			"true", "possible_duplicate"},
		{"held false", []string{"--held-for-approval=false"}, "false", ""},
		{"reason alone", []string{"--approval-hold-reason", "table_setting"}, "", "table_setting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saw bool
			args := append([]string{"1", "--is-bad-data"}, tc.args...)
			runData(t, args, func(w http.ResponseWriter, r *http.Request) {
				saw = true
				q := r.URL.Query()
				if got := q.Get("held_for_approval"); got != tc.wantHeld {
					t.Errorf("held_for_approval = %q, want %q", got, tc.wantHeld)
				}
				if got := q.Get("approval_hold_reason"); got != tc.wantReason {
					t.Errorf("approval_hold_reason = %q, want %q", got, tc.wantReason)
				}
				if got := q.Get("is_bad_data"); got != "true" {
					t.Errorf("is_bad_data = %q, want true", got)
				}
				testutil.RespondJSON(w, http.StatusOK, sampleGetData())
			})
			if !saw {
				t.Fatal("the data request never arrived; the query assertions above did not run")
			}
		})
	}
}

// The control for the no-request assertions above: the same mock DOES
// record a "/data" request when the command is allowed to run, so a green
// no-request check means the refusal happened rather than that the harness
// records nothing.
func TestGetDataRecordsTheDataRequestWhenAccepted(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("quarantine_hold"); got != string(api.QuarantineHoldHold) {
				t.Errorf("quarantine_hold = %q, want %q", got, api.QuarantineHoldHold)
			}
			testutil.RespondJSON(w, http.StatusOK, sampleGetData())
		})

	if out, err := execData(t, []string{"1", "--is-bad-data", "--quarantine-hold", "hold"}); err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	var saw bool
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/data") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("mock.Requests() never records \"/data\" (%v); the no-request assertions "+
			"in TestGetDataRefusesBadDataOnlyFlags cannot fail", mock.Requests())
	}
}
