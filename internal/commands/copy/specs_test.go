package copycmd

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const generatedClient = "../../api/client.gen.go"

func strPtr(s string) *string { return &s }

func statePtr(s api.DatasetCopyDataSourceRequestState) *api.DatasetCopyDataSourceRequestState {
	return &s
}

// assertBadUsage fails unless err exits 2 and its message contains every one
// of wants.
func assertBadUsage(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestParseCopyOptions(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []api.DatasetCopyOption
	}{
		{name: "no values", values: nil, want: nil},
		{name: "wire spelling", values: []string{"value_maps"}, want: []api.DatasetCopyOption{api.DatasetCopyOptionValueMaps}},
		{
			name:   "hyphen spelling",
			values: []string{"value-maps,learned-values,anomaly-model"},
			want:   []api.DatasetCopyOption{api.DatasetCopyOptionValueMaps, api.DatasetCopyOptionLearnedValues, api.DatasetCopyOptionAnomalyModel},
		},
		{
			name:   "repeated flag and comma lists are deduplicated in first-seen order",
			values: []string{"rules", "access,rules", "value-maps", "value_maps"},
			want:   []api.DatasetCopyOption{api.DatasetCopyOptionRules, api.DatasetCopyOptionAccess, api.DatasetCopyOptionValueMaps},
		},
		{name: "spaces around an element", values: []string{" rules , access"}, want: []api.DatasetCopyOption{api.DatasetCopyOptionRules, api.DatasetCopyOptionAccess}},
		{
			name:   "every generated option",
			values: []string{"settings,value_maps,rules,learned_values,anomaly_model,access"},
			want: []api.DatasetCopyOption{
				api.DatasetCopyOptionSettings, api.DatasetCopyOptionValueMaps, api.DatasetCopyOptionRules, api.DatasetCopyOptionLearnedValues, api.DatasetCopyOptionAnomalyModel, api.DatasetCopyOptionAccess,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCopyOptions(includeFlag, tt.values)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCopyOptionsRefusals(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "trailing empty elements", values: []string{"rules,,"}, want: `"rules,,"`},
		{name: "leading empty element", values: []string{",rules"}, want: `",rules"`},
		{name: "empty value", values: []string{""}, want: `""`},
		{name: "blank value", values: []string{"  "}, want: `"  "`},
		{name: "unknown option", values: []string{"rules", "bogus"}, want: `"bogus"`},
		{name: "wrong case", values: []string{"Rules"}, want: `"Rules"`},
		{name: "item type that is not an option", values: []string{"data_source"}, want: `"data_source"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCopyOptions(excludeFlag, tt.values)
			assertBadUsage(t, err, "--exclude", tt.want)
			if got != nil {
				t.Errorf("got %v alongside the error, want nil", got)
			}
		})
	}
}

func TestParseCopyOptionsListsTheValidOptions(t *testing.T) {
	_, err := parseCopyOptions(includeFlag, []string{"bogus"})
	assertBadUsage(t, err, "settings, value_maps, rules, learned_values, anomaly_model, access")
}

// backendOffered is what GET .../copy offers today: every option is a
// default, and settings is always copied.
func backendOffered() []api.DatasetCopyOptionInfo {
	return []api.DatasetCopyOptionInfo{
		{Option: api.DatasetCopyOptionSettings, Default: true, Always: true, Count: 1},
		{Option: api.DatasetCopyOptionValueMaps, Default: true, Count: 2},
		{Option: api.DatasetCopyOptionRules, Default: true, Count: 5},
		{Option: api.DatasetCopyOptionLearnedValues, Default: true},
		{Option: api.DatasetCopyOptionAnomalyModel, Default: true, Count: 1},
		{Option: api.DatasetCopyOptionAccess, Default: true, Count: 3},
	}
}

// rulesAlwaysOffered is a hypothetical offer in which rules, not settings, is
// always copied, and two options are not defaults.
func rulesAlwaysOffered() []api.DatasetCopyOptionInfo {
	return []api.DatasetCopyOptionInfo{
		{Option: api.DatasetCopyOptionSettings, Default: true},
		{Option: api.DatasetCopyOptionRules, Default: true, Always: true},
		{Option: api.DatasetCopyOptionLearnedValues},
		{Option: api.DatasetCopyOptionAccess},
	}
}

func TestResolveInclude(t *testing.T) {
	tests := []struct {
		name    string
		offered []api.DatasetCopyOptionInfo
		include []api.DatasetCopyOption
		exclude []api.DatasetCopyOption
		want    []api.DatasetCopyOption
	}{
		{
			name:    "neither flag carries every default and always option",
			offered: backendOffered(),
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionValueMaps, api.DatasetCopyOptionRules, api.DatasetCopyOptionLearnedValues, api.DatasetCopyOptionAnomalyModel, api.DatasetCopyOptionAccess},
		},
		{
			name:    "include adds the always option",
			offered: backendOffered(),
			include: []api.DatasetCopyOption{api.DatasetCopyOptionRules},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionRules},
		},
		{
			name:    "include follows the offered order",
			offered: backendOffered(),
			include: []api.DatasetCopyOption{api.DatasetCopyOptionAccess, api.DatasetCopyOptionValueMaps},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionValueMaps, api.DatasetCopyOptionAccess},
		},
		{
			name:    "including the always option is harmless",
			offered: backendOffered(),
			include: []api.DatasetCopyOption{api.DatasetCopyOptionSettings},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings},
		},
		{
			name:    "exclude removes from the defaults",
			offered: backendOffered(),
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionAccess, api.DatasetCopyOptionRules},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionValueMaps, api.DatasetCopyOptionLearnedValues, api.DatasetCopyOptionAnomalyModel},
		},
		{
			name:    "neither flag skips options that are not defaults",
			offered: rulesAlwaysOffered(),
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionRules},
		},
		{
			name:    "include takes an option that is not a default",
			offered: rulesAlwaysOffered(),
			include: []api.DatasetCopyOption{api.DatasetCopyOptionAccess},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionRules, api.DatasetCopyOptionAccess},
		},
		{
			name:    "settings can be excluded when the offer does not mark it always",
			offered: rulesAlwaysOffered(),
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionSettings},
			want:    []api.DatasetCopyOption{api.DatasetCopyOptionRules},
		},
		{
			name:    "no option offered",
			offered: nil,
			want:    []api.DatasetCopyOption{},
		},
		{
			name:    "nothing default or always",
			offered: []api.DatasetCopyOptionInfo{{Option: api.DatasetCopyOptionAccess}},
			want:    []api.DatasetCopyOption{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInclude(tt.offered, tt.include, tt.exclude)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("got nil, want an explicit list")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveIncludeRefusals(t *testing.T) {
	notAccess := backendOffered()[:5]
	tests := []struct {
		name    string
		offered []api.DatasetCopyOptionInfo
		include []api.DatasetCopyOption
		exclude []api.DatasetCopyOption
		wants   []string
	}{
		{
			name:    "excluding the always option",
			offered: backendOffered(),
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionRules, api.DatasetCopyOptionSettings},
			wants:   []string{"--exclude", `"settings"`, "settings is always copied"},
		},
		{
			name:    "excluding an option the offer marks always, whatever its name",
			offered: rulesAlwaysOffered(),
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionRules},
			wants:   []string{"--exclude", "rules is always copied"},
		},
		{
			name:    "including an option not offered",
			offered: notAccess,
			include: []api.DatasetCopyOption{api.DatasetCopyOptionAccess},
			wants:   []string{"--include", `"access"`, "settings, value_maps, rules, learned_values, anomaly_model"},
		},
		{
			name:    "excluding an option not offered",
			offered: notAccess,
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionAccess},
			wants:   []string{"--exclude", `"access"`},
		},
		{
			name:    "including when nothing is offered",
			offered: nil,
			include: []api.DatasetCopyOption{api.DatasetCopyOptionRules},
			wants:   []string{"--include", "offers no copy options"},
		},
		{
			name:    "both flags",
			offered: backendOffered(),
			include: []api.DatasetCopyOption{api.DatasetCopyOptionRules},
			exclude: []api.DatasetCopyOption{api.DatasetCopyOptionAccess},
			wants:   []string{"--include", "--exclude"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveInclude(tt.offered, tt.include, tt.exclude)
			assertBadUsage(t, err, tt.wants...)
		})
	}
}

func s3Active() api.DatasetCopyDataSourceInfo {
	return api.DatasetCopyDataSourceInfo{
		ID:             12,
		Name:           "orders-s3",
		DataSourceType: "s3",
		State:          api.DataSourceStateActive,
		LocationField:  strPtr("s3_bucket"),
		Location:       strPtr("acme-drop"),
		DistinguishingFields: map[string]*string{
			"pattern":          strPtr("*.csv"),
			"s3_prefix":        strPtr("2026-09/"),
			"archive_pattern":  strPtr(""),
			"excel_sheet_name": strPtr(""),
		},
	}
}

func sftpDraft() api.DatasetCopyDataSourceInfo {
	return api.DatasetCopyDataSourceInfo{
		ID:             13,
		Name:           "orders-sftp",
		DataSourceType: "sftp",
		State:          api.DataSourceStateDraft,
		LocationField:  strPtr("sftp_host"),
		Location:       strPtr("sftp.example.com"),
		DistinguishingFields: map[string]*string{
			"pattern":          strPtr("*.csv"),
			"sftp_folder":      strPtr("/in"),
			"archive_pattern":  strPtr(""),
			"excel_sheet_name": strPtr(""),
		},
	}
}

// locationless is a hypothetical active source with no location, which the
// server's same-files guard never compares.
func locationless() api.DatasetCopyDataSourceInfo {
	return api.DatasetCopyDataSourceInfo{
		ID:                   14,
		Name:                 "uploads",
		DataSourceType:       "qluster_ui",
		State:                api.DataSourceStateActive,
		DistinguishingFields: map[string]*string{},
	}
}

// simpleURLActive is an active simple_url source, whose URL the offer
// withholds.
func simpleURLActive() api.DatasetCopyDataSourceInfo {
	return api.DatasetCopyDataSourceInfo{
		ID:                   17,
		Name:                 "orders-url",
		DataSourceType:       "simple_url",
		State:                api.DataSourceStateActive,
		LocationField:        strPtr("simple_url"),
		DistinguishingFields: map[string]*string{"archive_pattern": nil, "excel_sheet_name": nil},
		SecretsToResupply:    []string{"simple_url"},
	}
}

// testURL is a URL a test supplies for a simple_url source. It carries a
// credential, so no output may hold it.
const testURL = "https://files.example.com/orders.csv?token=url-credential"

// mustParseFile returns the specs of a --data-sources-file holding file, nil
// for "".
func mustParseFile(t *testing.T, file string) []dataSourceSpec {
	t.Helper()
	if file == "" {
		return nil
	}
	specs, err := parseDataSourcesFile([]byte(file))
	if err != nil {
		t.Fatalf("parseDataSourcesFile(%q): %v", file, err)
	}
	return specs
}

func TestBuildDataSourceRequests(t *testing.T) {
	offered := []api.DatasetCopyDataSourceInfo{s3Active(), sftpDraft(), locationless(), simpleURLActive()}
	otherURL := testURL + "&page=2"
	tests := []struct {
		name string
		file string
		want []api.DatasetCopyDataSourceRequest
	}{
		{name: "no specs", file: "", want: []api.DatasetCopyDataSourceRequest{}},
		{
			name: "draft source unchanged",
			file: "- source_id: 13",
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 13}},
		},
		{
			name: "draft source keeping every selector at its own value",
			file: `- {source_id: 13, sftp_folder: /in, pattern: "*.csv"}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 13, SftpFolder: strPtr("/in"), Pattern: strPtr("*.csv")}},
		},
		{
			name: "active source with s3_prefix changed",
			file: "- {source_id: 12, s3_prefix: 2026-10/, state: draft}",
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 12, S3Prefix: strPtr("2026-10/"), State: statePtr(api.DatasetCopyDataSourceRequestStateDraft)},
			},
		},
		{
			name: "active source whose field is unset gets one",
			file: `- {source_id: 12, archive_pattern: "*.zip"}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 12, ArchivePattern: strPtr("*.zip")}},
		},
		{
			name: "active source with s3_prefix cleared",
			file: `- {source_id: 12, s3_prefix: ""}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 12, S3Prefix: strPtr("")}},
		},
		{
			name: "active source where one of two given selectors changes",
			file: `- {source_id: 12, pattern: "*.csv", s3_prefix: 2026-10/}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 12, Pattern: strPtr("*.csv"), S3Prefix: strPtr("2026-10/")}},
		},
		{
			name: "a value holding a comma reaches the request",
			file: `- {source_id: 12, pattern: "a,b*.csv"}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 12, Pattern: strPtr("a,b*.csv")}},
		},
		{
			name: "active source without a location",
			file: "- {source_id: 14, name: uploads-copy}",
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 14, Name: strPtr("uploads-copy")}},
		},
		{
			name: "one source copied twice, in order",
			file: `
- {source_id: 12, s3_prefix: 2026-10/, name: october}
- {source_id: 13}
- {source_id: 12, s3_prefix: 2026-11/, name: november, schedule: "0 * * * *", excel_sheet_name: Q4}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 12, S3Prefix: strPtr("2026-10/"), Name: strPtr("october")},
				{SourceID: 13},
				{
					SourceID:       12,
					S3Prefix:       strPtr("2026-11/"),
					Name:           strPtr("november"),
					Schedule:       strPtr("0 * * * *"),
					ExcelSheetName: strPtr("Q4"),
				},
			},
		},
		{
			name: "an inactive first copy may share files with a later copy",
			file: `
- {source_id: 12, s3_prefix: 2026-10/, state: draft, name: first}
- {source_id: 12, s3_prefix: 2026-10/, state: active, name: second}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 12, S3Prefix: strPtr("2026-10/"), State: statePtr(api.DatasetCopyDataSourceRequestStateDraft), Name: strPtr("first")},
				{SourceID: 12, S3Prefix: strPtr("2026-10/"), State: statePtr(api.DatasetCopyDataSourceRequestStateActive), Name: strPtr("second")},
			},
		},
		{
			name: "active simple_url source unchanged",
			file: `- {source_id: 17, simple_url: "` + testURL + `"}`,
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 17, SimpleURL: strPtr(testURL)}},
		},
		{
			name: "active simple_url source unchanged and without a URL",
			file: "- source_id: 17",
			want: []api.DatasetCopyDataSourceRequest{{SourceID: 17}},
		},
		{
			name: "two active copies of a simple_url source with different URLs",
			file: `
- {source_id: 17, name: first, simple_url: "` + testURL + `"}
- {source_id: 17, name: second, simple_url: "` + otherURL + `"}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first"), SimpleURL: strPtr(testURL)},
				{SourceID: 17, Name: strPtr("second"), SimpleURL: strPtr(otherURL)},
			},
		},
		{
			name: "two active copies of a simple_url source, only the later with a URL",
			file: `
- {source_id: 17, name: first}
- {source_id: 17, name: second, simple_url: "` + testURL + `"}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first")},
				{SourceID: 17, Name: strPtr("second"), SimpleURL: strPtr(testURL)},
			},
		},
		{
			name: "two active copies of a simple_url source, only the earlier with a URL",
			file: `
- {source_id: 17, name: first, simple_url: "` + testURL + `"}
- {source_id: 17, name: second}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first"), SimpleURL: strPtr(testURL)},
				{SourceID: 17, Name: strPtr("second")},
			},
		},
		{
			name: "two active copies of a simple_url source, neither with a URL",
			file: "- {source_id: 17, name: first}\n- {source_id: 17, name: second}",
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first")},
				{SourceID: 17, Name: strPtr("second")},
			},
		},
		{
			name: "the same URL after a draft copy of a simple_url source",
			file: `
- {source_id: 17, name: first, state: draft, simple_url: "` + testURL + `"}
- {source_id: 17, name: second, simple_url: "` + testURL + `"}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first"), State: statePtr(api.DatasetCopyDataSourceRequestStateDraft), SimpleURL: strPtr(testURL)},
				{SourceID: 17, Name: strPtr("second"), SimpleURL: strPtr(testURL)},
			},
		},
		{
			name: "the same URL with another excel_sheet_name",
			file: `
- {source_id: 17, name: first, simple_url: "` + testURL + `"}
- {source_id: 17, name: second, simple_url: "` + testURL + `", excel_sheet_name: Q4}
`,
			want: []api.DatasetCopyDataSourceRequest{
				{SourceID: 17, Name: strPtr("first"), SimpleURL: strPtr(testURL)},
				{SourceID: 17, Name: strPtr("second"), SimpleURL: strPtr(testURL), ExcelSheetName: strPtr("Q4")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDataSourceRequests(offered, mustParseFile(t, tt.file), "Orders (2)")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(tt.want)
				t.Errorf("got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestBuildDataSourceRequestsRefusals(t *testing.T) {
	offered := []api.DatasetCopyDataSourceInfo{s3Active(), sftpDraft()}
	sameFiles := "data source 12 (orders-s3) is active, so a copy reads the same files " +
		"unless it changes at least one of archive_pattern, excel_sheet_name, pattern, s3_prefix"
	tests := []struct {
		name    string
		offered []api.DatasetCopyDataSourceInfo
		file    string
		wants   []string
	}{
		{
			name:    "id not offered",
			offered: offered,
			file:    "- source_id: 99",
			wants:   []string{"entry 1 (source_id 99): ", "data source 99", "copyable: 12 (orders-s3), 13 (orders-sftp)"},
		},
		{
			name:    "no source offered",
			offered: nil,
			file:    "- {source_id: 12, s3_prefix: x/}",
			wants:   []string{"the table has no data source a copy can take"},
		},
		{
			name:    "a later spec is refused",
			offered: offered,
			file:    "- source_id: 13\n- source_id: 98",
			wants:   []string{"entry 2 (source_id 98): "},
		},
		{
			name:    "selector the source's type does not distinguish by",
			offered: offered,
			file:    "- {source_id: 13, s3_prefix: x/}",
			wants: []string{
				"s3_prefix is not a distinguishing field of data source 13 (orders-sftp), a sftp source",
				"its distinguishing fields are archive_pattern, excel_sheet_name, pattern, sftp_folder",
			},
		},
		{
			name:    "selector on a source with none",
			offered: []api.DatasetCopyDataSourceInfo{locationless()},
			file:    `- {source_id: 14, pattern: "*.csv"}`,
			wants:   []string{"it has no distinguishing fields"},
		},
		{name: "active source unchanged", offered: offered, file: "- source_id: 12", wants: []string{sameFiles}},
		{
			name:    "active source with only name and schedule",
			offered: offered,
			file:    `- {source_id: 12, name: other, schedule: "0 * * * *"}`,
			wants:   []string{sameFiles},
		},
		{
			name:    "active source copied as a draft",
			offered: offered,
			file:    "- {source_id: 12, state: draft}",
			wants:   []string{sameFiles},
		},
		{
			name:    "active source with a selector set to its own value",
			offered: offered,
			file:    "- {source_id: 12, s3_prefix: 2026-09/}",
			wants:   []string{sameFiles},
		},
		{
			name:    "active source with every set selector set to its own value",
			offered: offered,
			file:    `- {source_id: 12, pattern: "*.csv", s3_prefix: 2026-09/}`,
			wants:   []string{sameFiles},
		},
		{
			name:    "active source with an unset selector cleared",
			offered: offered,
			file:    `- {source_id: 12, archive_pattern: ""}`,
			wants:   []string{sameFiles},
		},
		{
			name:    "two active copies of one source read the same files",
			offered: offered,
			file: `
- {source_id: 12, s3_prefix: us/, name: first}
- {source_id: 12, s3_prefix: us/, name: second, state: disabled}
`,
			wants: []string{"entry 2 (source_id 12): ", "earlier active copy"},
		},
		{
			name:    "active source unchanged though the entry supplies simple_url",
			offered: offered,
			file:    `- {source_id: 12, simple_url: "` + testURL + `"}`,
			wants:   []string{sameFiles},
		},
		{
			name:    "two active copies of a source that is not simple_url, whatever URLs they supply",
			offered: offered,
			file: `
- {source_id: 12, s3_prefix: us/, name: first, simple_url: "` + testURL + `"}
- {source_id: 12, s3_prefix: us/, name: second, simple_url: "` + testURL + `&page=2"}
`,
			wants: []string{"entry 2 (source_id 12): ", "earlier active copy; change at least one of"},
		},
		{
			name:    "two copies with equal final selectors given in another order",
			offered: offered,
			file: `
- {source_id: 12, s3_prefix: us/, pattern: x, name: first}
- {source_id: 12, pattern: x, s3_prefix: us/, name: second}
`,
			wants: []string{"earlier active copy"},
		},
		{
			name:    "two copies default to the same name",
			offered: offered,
			file:    "- {source_id: 13, state: draft}\n- {source_id: 13, state: disabled}",
			wants:   []string{"entry 2 (source_id 13): ", `name "orders-sftp" is already used`},
		},
		{
			name:    "an explicit name collides with an earlier default name",
			offered: offered,
			file:    "- source_id: 13\n- {source_id: 12, s3_prefix: us/, name: orders-sftp}",
			wants:   []string{`name "orders-sftp" is already used`},
		},
		{
			name:    "name collides with new table upload source",
			offered: offered,
			file:    "- {source_id: 13, name: Orders (2) Source}",
			wants:   []string{`name "Orders (2) Source" is already used`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDataSourceRequests(tt.offered, mustParseFile(t, tt.file), "Orders (2)")
			assertBadUsage(t, err, append([]string{"--data-sources-file entry "}, tt.wants...)...)
			if got != nil {
				t.Errorf("got %v alongside the error, want nil", got)
			}
		})
	}
}

// Two copies of a simple_url source collide when both supply one URL, keep the
// same distinguishing fields, and the earlier is active, and the refusal never
// shows the URL.
func TestBuildDataSourceRequestsRefusesASimpleURLSuppliedTwice(t *testing.T) {
	t.Setenv("QCTL_COPY_TEST_URL_A", testURL)
	t.Setenv("QCTL_COPY_TEST_URL_B", testURL)
	tests := []struct {
		name string
		file string
	}{
		{
			name: "the later copy a draft",
			file: `
- {source_id: 17, name: first, simple_url: "` + testURL + `"}
- {source_id: 17, name: second, state: draft, simple_url: "` + testURL + `"}
`,
		},
		{
			name: "the same distinguishing field given on both",
			file: `
- {source_id: 17, name: first, excel_sheet_name: Q4, simple_url: "` + testURL + `"}
- {source_id: 17, name: second, excel_sheet_name: Q4, simple_url: "` + testURL + `"}
`,
		},
		{
			name: "one URL through two variables",
			file: `
- {source_id: 17, name: first, simple_url: "${QCTL_COPY_TEST_URL_A}"}
- {source_id: 17, name: second, simple_url: "${QCTL_COPY_TEST_URL_B}"}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildDataSourceRequests([]api.DatasetCopyDataSourceInfo{simpleURLActive()},
				mustParseFile(t, tt.file), "Orders (2)")
			assertBadUsage(t, err, "--data-sources-file entry 2 (source_id 17): ",
				"earlier active copy that supplies the same simple_url; change simple_url or at least one of archive_pattern, excel_sheet_name")
			if err != nil && strings.Contains(err.Error(), "files.example.com") {
				t.Error("the error shows the URL")
			}
		})
	}
}

// TestEveryDataSourceKeyReachesItsRequestField sets each key alone and checks
// that it is the one non-null optional field of the request's wire form. Each
// request field must be source_id, a non-secret key or a secret key, so a
// field a regeneration adds fails here until it is given a class.
func TestEveryDataSourceKeyReachesItsRequestField(t *testing.T) {
	requestKeys := jsonKeys(t, reflect.TypeFor[api.DatasetCopyDataSourceRequest]())
	want := slices.Concat([]string{dsKeySourceID}, dataSourceKeys, dataSourceSecretKeys)
	sort.Strings(want)
	if !slices.Equal(requestKeys, want) {
		t.Fatalf("DatasetCopyDataSourceRequest has keys %v, want source_id, the non-secret keys and the secret keys: %v",
			requestKeys, want)
	}
	for _, key := range slices.Concat(dataSourceKeys, dataSourceSecretKeys) {
		t.Run(key, func(t *testing.T) {
			value := "v"
			if key == dsKeyState {
				value = string(api.DatasetCopyDataSourceRequestStateDisabled)
			}
			spec := dataSourceSpec{ID: 5, Values: map[string]string{}, Secrets: map[string]string{}}
			if slices.Contains(dataSourceSecretKeys, key) {
				spec.Secrets[key] = value
			} else {
				spec.Values[key] = value
			}
			body, err := json.Marshal(newDataSourceRequest(spec))
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatal(err)
			}
			for wireKey, got := range wire {
				switch wireKey {
				case "source_id":
					if got != float64(5) {
						t.Errorf("source_id = %v, want 5", got)
					}
				case key:
					if got != value {
						t.Errorf("%s = %v, want %q", key, got, value)
					}
				default:
					if got != nil {
						t.Errorf("%s = %v, want null", wireKey, got)
					}
				}
			}
		})
	}
}

func TestDataSourceStatesMatchTheGeneratedConstants(t *testing.T) {
	generated := generatedEnumConstants(t, "DatasetCopyDataSourceRequestState")
	var ours []string
	for _, state := range dataSourceStates {
		ours = append(ours, string(state))
	}
	sort.Strings(ours)
	if !slices.Equal(ours, generated) {
		t.Errorf("dataSourceStates = %v, %s declares %v", ours, generatedClient, generated)
	}
}

func TestCopyOptionEnumValuesMatchTheGeneratedConstants(t *testing.T) {
	generated := generatedEnumConstants(t, "DatasetCopyOption")
	values := api.EnumValues[api.DatasetCopyOption]()
	sort.Strings(values)
	if !slices.Equal(values, generated) {
		t.Errorf("api.EnumValues[DatasetCopyOption] = %v, %s declares %v", values, generatedClient, generated)
	}
}

// jsonKeys returns the sorted json names of typ's fields.
func jsonKeys(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	keys := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// generatedEnumConstants returns, sorted, the values of every constant of the
// named type in the generated client.
func generatedEnumConstants(t *testing.T, typeName string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), generatedClient, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", generatedClient, err)
	}
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			lit, isLit := vs.Values[0].(*ast.BasicLit)
			if !ok || ident.Name != typeName || !isLit || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquoting %s: %v", lit.Value, err)
			}
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		t.Fatalf("no %s constants in %s; this check cannot fail as written", typeName, generatedClient)
	}
	sort.Strings(values)
	return values
}
