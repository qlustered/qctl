package download

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/spf13/cobra"
)

// These four parameters cannot honestly shape an export and must not be
// offered. The worker overwrites --page/--limit, --include-history-flags is
// unused, and --include-dataset-rule-ids is rejected on clean rows but shapes
// nothing on quarantine rows. A user who passed --limit 20 and got four
// million rows would be right to call that a bug.
func TestInertParamsAreNotRegistered(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)

	for _, name := range []string{"page", "limit", "include-history-flags", "include-dataset-rule-ids"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s is registered, but the backend ignores it on this route", name)
		}
	}
}

func TestUsefulParamsAreRegistered(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)

	for _, name := range []string{
		"is-bad-data", "show-metadata", "show-row-logs", "fields", "order-by",
		"search-query", "search-by-field-name", "search-regex", "search-case-sensitive",
		"filter-by-field-name", "filter-by-field-value", "filter-operator",
		"comment-thread-id", "dataset-rule-ids", "data-source-model-id",
		"stored-item-id", "warning-item-id", "has-any-warning", "history-ids",
		"deleted-rows", "awaiting-processing", "quarantine-hold", "passes-validation",
		"held-for-approval", "approval-hold-reason", "waiting-for-approval",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s should be registered", name)
		}
	}
}

func TestGetOnlyReviewParamsAreNotRegistered(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)
	for _, name := range []string{
		"review-status", "review-group-id", "review-change-history-id", "request-state", "request-view",
	} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s is registered, but the export route does not accept it", name)
		}
	}
}

// --quarantine-hold replaced the boolean --keep-in-quarantine in the
// 2026-09-10 spec. It is derived, so the rename needed no code -- which is
// exactly why it needs a test: the flag arrived with the generic passthrough
// help and no client-side check, and the old name simply stopped existing.
func TestQuarantineHoldFlagIsCuratedOnThisCommandToo(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)

	f := cmd.Flags().Lookup(paramflags.QuarantineHoldFlag)
	if f == nil {
		t.Fatalf("--%s was not registered", paramflags.QuarantineHoldFlag)
	}
	if f.Usage != paramflags.QuarantineHoldHelp {
		t.Errorf("--%s help = %q, want the shared curated line %q",
			paramflags.QuarantineHoldFlag, f.Usage, paramflags.QuarantineHoldHelp)
	}
}

func TestEveryExportBadDataOnlyFlagHasActionableHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)
	for _, name := range []string{
		"quarantine-hold", "passes-validation", "awaiting-processing", "dataset-rule-ids",
		"held-for-approval", "approval-hold-reason", "waiting-for-approval",
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

// Every refusal costs nothing: runExport validates before it builds params,
// before Bootstrap and before the export is enqueued.
func TestExportRefusesBadDataOnlyFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"value outside the enum", []string{"--is-bad-data", "--quarantine-hold", "yes"}},
		{"quarantine hold", []string{"--quarantine-hold", "hold"}},
		{"passes validation false", []string{"--passes-validation=false"}},
		{"awaiting processing false", []string{"--awaiting-processing=false"}},
		{"dataset rule ids", []string{"--dataset-rule-ids", "550e8400-e29b-41d4-a716-446655440000"}},
		{"held for approval", []string{"--held-for-approval"}},
		{"held for approval false", []string{"--held-for-approval=false"}},
		{"approval hold reason", []string{"--approval-hold-reason", "review_policy"}},
		{"reason outside the enum", []string{"--is-bad-data", "--approval-hold-reason", "duplicate"}},
		{"waiting for approval", []string{"--waiting-for-approval"}},
		{"waiting for approval false", []string{"--waiting-for-approval=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDataTestServer(t)
			command := append([]string{"download", "data", exportDatasetID}, tc.args...)
			_, _, err := runDownloadData(t, command...)
			if err == nil {
				t.Fatal("bad data-only flag was accepted")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if requests, _, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused export still made %d request(s)", requests)
			}
		})
	}
}

// With --is-bad-data both approval-hold filters reach the export POST's query
// string, false included.
func TestExportSendsTheApprovalHoldFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"both", []string{"--held-for-approval", "--approval-hold-reason", "possible_duplicate"},
			[]string{"held_for_approval=true", "approval_hold_reason=possible_duplicate"}},
		{"held false", []string{"--held-for-approval=false"}, []string{"held_for_approval=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDataTestServer(t)
			command := append([]string{"download", "data", exportDatasetID, "--is-bad-data"}, tc.args...)
			if stdout, stderr, err := runDownloadData(t, command...); err != nil {
				t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			_, posts, _, _, query := f.counters()
			if posts != 1 {
				t.Fatalf("export POST issued %d times, want once", posts)
			}
			for _, want := range append(tc.want, "is_bad_data=true") {
				if !strings.Contains(query, want) {
					t.Errorf("the POST query lacks %q: %q", want, query)
				}
			}
		})
	}
}

// --fields takes a comma-separated list as well as repeats, and each name
// reaches the export POST as its own fields parameter.
func TestExportSplitsFieldsCommaLists(t *testing.T) {
	f := newDataTestServer(t)
	command := []string{"download", "data", exportDatasetID, "--fields", "email, amount", "--fields", "id"}
	if stdout, stderr, err := runDownloadData(t, command...); err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	_, posts, _, _, query := f.counters()
	if posts != 1 {
		t.Fatalf("export POST issued %d times, want once", posts)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse the POST query %q: %v", query, err)
	}
	if got, want := values["fields"], []string{"email", "amount", "id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fields query = %q, want %q (raw %q)", got, want, query)
	}
}

func TestBuildExportParamsOnlySetsChangedFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)
	if err := cmd.Flags().Set("is-bad-data", "true"); err != nil {
		t.Fatal(err)
	}

	params, err := buildExportParams(cmd)
	if err != nil {
		t.Fatalf("buildExportParams: %v", err)
	}
	if params.IsBadData == nil || !*params.IsBadData {
		t.Error("--is-bad-data was set but IsBadData is not true")
	}
	if params.ShowMetadata != nil {
		t.Error("an untouched flag must stay nil so the backend default applies")
	}
}

func TestBuildExportParamsRejectsBadUUID(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)
	if err := cmd.Flags().Set("comment-thread-id", "not-a-uuid"); err != nil {
		t.Fatal(err)
	}
	_, err := buildExportParams(cmd)
	if err != nil {
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			// The message stays right even when the Unwrap chain breaks, so
			// assert the code: a client-side flag validation failure is exit 2.
			t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
		}
	} else {
		t.Fatal("expected a rejection for a malformed UUID")
	}
}

// TRIPWIRE 1. The backend comment on put_data_for_download reads "NOTE: if you
// update this, update the params in get_data too!!". This is our side of it: a
// parameter that appears on the export route but not on get_data means the two
// have drifted and `get data`'s flags no longer preview what an export will
// return.
func TestDownloadParamsAreASubsetOfGetDataParams(t *testing.T) {
	getData := map[string]reflect.Type{}
	gt := reflect.TypeOf(api.GetDataParams{})
	for i := 0; i < gt.NumField(); i++ {
		f := gt.Field(i)
		if wire := paramflags.WireNameFromTag(f); wire != "" {
			getData[wire] = f.Type
		}
	}

	dt := reflect.TypeOf(api.PutDataForDownloadParams{})
	for i := 0; i < dt.NumField(); i++ {
		f := dt.Field(i)
		wire := paramflags.WireNameFromTag(f)
		if wire == "" {
			continue
		}
		want, ok := getData[wire]
		if !ok {
			t.Errorf("PutDataForDownloadParams.%s (%s) has no counterpart on GetDataParams; "+
				"the two routes have drifted apart", f.Name, wire)
			continue
		}
		if want != f.Type {
			t.Errorf("%s: export type %s, get_data type %s", wire, f.Type, want)
		}
	}
}

// TRIPWIRE 2. A new backend parameter must not vanish. Modelled on
// TestNoParamIsSilentlyUnsupported in internal/commands/get.
func TestNoDownloadParamIsSilentlyUnsupported(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)

	tp := reflect.TypeOf(api.PutDataForDownloadParams{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		wire := paramflags.WireNameFromTag(f)
		if wire == "" || exportFlagDenylist[wire] {
			continue
		}
		if paramflags.KindForField(f.Type) == paramflags.KindUnsupported {
			t.Errorf("PutDataForDownloadParams.%s (%s, %s) classifies as KindUnsupported and is "+
				"dropped with no flag and no error.\nEither add support to KindForField or record "+
				"it in exportFlagDenylist with a reason.", f.Name, wire, f.Type)
			continue
		}
		if cmd.Flags().Lookup(paramflags.FlagNameForWire(wire)) == nil {
			t.Errorf("PutDataForDownloadParams.%s (%s) is neither registered nor denylisted", f.Name, wire)
		}
	}
}

func TestDeletedRowsFlagIsCuratedOnThisCommandToo(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	registerExportFlags(cmd)

	f := cmd.Flags().Lookup("deleted-rows")
	if f == nil {
		t.Fatal("--deleted-rows was not registered")
	}
	if f.Usage != paramflags.DeletedRowsHelp {
		t.Errorf("--deleted-rows help = %q, want the shared curated line %q", f.Usage, paramflags.DeletedRowsHelp)
	}
}

func TestExportRefusesAnUnknownDeletedRowsValue(t *testing.T) {
	f := newDataTestServer(t)
	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--deleted-rows", "true")
	if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
		t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
	}
	if requests, _, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("a refused export still made %d request(s)", requests)
	}
}

func TestExportSendsDeletedRows(t *testing.T) {
	f := newDataTestServer(t)
	command := []string{"download", "data", exportDatasetID, "--is-bad-data", "--deleted-rows", "live_and_person_deleted"}
	if stdout, stderr, err := runDownloadData(t, command...); err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	_, posts, _, _, query := f.counters()
	if posts != 1 {
		t.Fatalf("export POST issued %d times, want once", posts)
	}
	if !strings.Contains(query, "deleted_rows=live_and_person_deleted") {
		t.Errorf("the POST query lacks deleted_rows: %q", query)
	}
}
