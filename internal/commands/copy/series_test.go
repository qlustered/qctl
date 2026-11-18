package copycmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const copySeriesID = "11111111-2222-3333-4444-555555555555"

var copySeriesPath = "/api/orgs/" + copyTestOrgID + "/table-series"

func serveCopySeries(e *copyEnv) {
	e.mock.RegisterHandler("GET", copySeriesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+copySeriesID+`","name":"Monthly orders","members":[]}]`))
	})
}

// copySeriesName and copySeriesTableName are the names the copy-options GET
// derives for period 2026-Q4 of the series Monthly orders.
const (
	copySeriesName      = "Monthly orders · 2026-Q4"
	copySeriesTableName = "monthly_orders_2026_q4"
)

// serveCopySeriesPreview answers the copy-options GET with the series-derived
// names and returns the query string of each GET it answers.
func serveCopySeriesPreview(e *copyEnv) *[]string {
	var queries []string
	body := strings.Replace(strings.Replace(copyTestOptions,
		`"name": "Orders (2)"`, `"name": "`+copySeriesName+`"`, 1),
		`"table_name": "orders_2"`, `"table_name": "`+copySeriesTableName+`"`, 1)
	e.mock.RegisterHandler("GET", copyTestPath, func(w http.ResponseWriter, req *http.Request) {
		queries = append(queries, req.URL.RawQuery)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	return &queries
}

// assertSeriesQuery fails unless query names the series and periodKey.
func assertSeriesQuery(t *testing.T, query, periodKey string) {
	t.Helper()
	got, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"series_id": {copySeriesID}, "period_key": {periodKey}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("copy-options query = %v, want %v", got, want)
	}
}

func TestCopyTable_SeriesDryRunMatchesThePostedRequest(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newCopyEnv(t)
			serveCopySeries(e)
			queries := serveCopySeriesPreview(e)
			args := []string{"--table-id", "42", "--series", "Monthly orders", "--period-key", "2026-Q4", "--include", "rules", "-o", format}
			stdout, stderr, err := runCopy(t, append(args, "--dry-run")...)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			e.assertRequests(t, "GET "+copySeriesPath, copyTestGET)
			assertSeriesQuery(t, (*queries)[0], "2026-Q4")
			var doc struct {
				Request map[string]any `json:"request" yaml:"request"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &doc)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &doc)
			}
			if err != nil {
				t.Fatalf("decode dry run: %v\n%s", err, stdout)
			}
			request, err := json.Marshal(doc.Request)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, string(request), `{
				"name":null,"table_name":null,"database_name":null,"schema_name":null,
				"destination_id":null,"include":["settings","rules"],
				"series":{"series_id":"`+copySeriesID+`","period_key":"2026-Q4"}
			}`)
			for _, want := range []string{
				"Series membership:", copySeriesID, "2026-Q4",
				`"` + copySeriesName + `"  (set by the series)`, copySeriesTableName,
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("plan lacks %q:\n%s", want, stderr)
				}
			}
			if n := strings.Count(stderr, "(set by the series)"); n != 2 {
				t.Errorf("plan marks %d names as set by the series, want 2:\n%s", n, stderr)
			}
			if strings.Contains(stderr, "Period start:") || strings.Contains(stderr, "Period end:") {
				t.Errorf("plan shows period dates the request does not carry:\n%s", stderr)
			}
			stdout, _, err = runCopy(t, append(args, "--yes")...)
			if err != nil {
				t.Fatalf("copy: %v", err)
			}
			e.assertRequests(t, "GET "+copySeriesPath, copyTestGET, "GET "+copySeriesPath, copyTestGET, copyTestPOST)
			assertSeriesQuery(t, (*queries)[1], "2026-Q4")
			assertJSONEqual(t, e.postBodies[0], string(request))
			if strings.Contains(stdout, "Series membership:") {
				t.Errorf("plan leaked into structured stdout:\n%s", stdout)
			}
		})
	}
}

func TestCopyTable_SeriesUUIDAndKindReference(t *testing.T) {
	for _, tc := range []struct {
		name, key, ref string
	}{
		{name: "UUID", key: "next period"},
		{name: "kind reference", key: "2026-10", ref: "table-series/" + copySeriesID},
		{name: "unicode key", key: strings.Repeat("月", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCopyEnv(t)
			queries := serveCopySeriesPreview(e)
			ref := copySeriesID
			if tc.ref != "" {
				ref = tc.ref
			}
			args := []string{"--table-id", "42", "--series", ref, "--period-key", tc.key, "--yes"}
			stdout, _, err := runCopy(t, args...)
			if err != nil {
				t.Fatalf("copy: %v", err)
			}
			e.assertRequests(t, copyTestGET, copyTestPOST)
			assertSeriesQuery(t, (*queries)[0], tc.key)
			var body struct {
				Series map[string]any `json:"series"`
			}
			if err := json.Unmarshal([]byte(e.postBodies[0]), &body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"series_id": copySeriesID, "period_key": tc.key}
			gotJSON, _ := json.Marshal(body.Series)
			wantJSON, _ := json.Marshal(want)
			assertJSONEqual(t, string(gotJSON), string(wantJSON))
			if !strings.Contains(stdout, "Series membership:") || !strings.Contains(stdout, tc.key) {
				t.Errorf("text plan lacks membership:\n%s", stdout)
			}
		})
	}
}

func TestCopyTable_InvalidSeriesFlagsExitTwoBeforeRequests(t *testing.T) {
	cases := map[string][]string{
		"series alone":    {"--series", "Monthly orders"},
		"key alone":       {"--period-key", "2026-10"},
		"empty series":    {"--series", "", "--period-key", "2026-10"},
		"blank series":    {"--series", " \t ", "--period-key", "2026-10"},
		"empty key":       {"--series", copySeriesID, "--period-key", ""},
		"padded key":      {"--series", copySeriesID, "--period-key", " 2026-10"},
		"trailing space":  {"--series", copySeriesID, "--period-key", "2026-10 "},
		"multiline key":   {"--series", copySeriesID, "--period-key", "2026\n10"},
		"carriage return": {"--series", copySeriesID, "--period-key", "2026\r10"},
		"long key":        {"--series", copySeriesID, "--period-key", strings.Repeat("月", 201)},
		"wrong kind":      {"--series", "table/42", "--period-key", "K"},
		"name":            {"--series", copySeriesID, "--period-key", "2026-10", "--name", "Orders EU"},
		"table name":      {"--series", copySeriesID, "--period-key", "2026-10", "--table-name", "orders_eu"},
		"empty name":      {"--series", copySeriesID, "--period-key", "2026-10", "--name", ""},
		"both names":      {"--name", "Orders EU", "--table-name", "orders_eu", "--series", copySeriesID, "--period-key", "2026-10"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCopyEnv(t)
			_, _, err := runCopy(t, append([]string{"--table-id", "42", "--yes"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			e.assertRequests(t)
		})
	}
}

func TestCopyTable_PeriodDateFlagsAreGone(t *testing.T) {
	for _, flag := range []string{"--period-start", "--period-end"} {
		t.Run(flag, func(t *testing.T) {
			e := newCopyEnv(t)
			_, _, err := runCopy(t, "--table-id", "42", "--series", copySeriesID, "--period-key", "2026-10", flag, "2026-10-01", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(fmt.Sprint(err), "unknown flag") {
				t.Fatalf("exit = %d, want 2 for an unknown flag; error = %v", code, err)
			}
			e.assertRequests(t)
		})
	}
}

func TestCopyTable_SeriesLookupRefusalsPostNothing(t *testing.T) {
	for _, name := range []string{"missing", "monthly orders", "Monthly"} {
		t.Run(name, func(t *testing.T) {
			e := newCopyEnv(t)
			serveCopySeries(e)
			_, _, err := runCopy(t, "--table-id", "42", "--series", name, "--period-key", "K", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit = %d, want 3; error = %v", code, err)
			}
			e.assertRequests(t, "GET "+copySeriesPath)
		})
	}
}

// The copy-options GET previews the series-derived names, so --dry-run is
// refused for what would refuse the copy: a missing permission, a series
// outside the organization, a bad period key, an occupied period.
func TestCopyTable_SeriesPreviewRefusalsStopTheDryRun(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		field, msg   string
		errorCode    string
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized, "series_id", "deliverable:admin required", ""},
		{http.StatusNotFound, qerrors.ExitNotFound, "series_id", "series not in this organization", ""},
		{http.StatusConflict, qerrors.ExitBadUsage, "period_key", "Table 7 already holds period", ""},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage, "period_key", "Series is monthly.", "SERIES_PERIOD_KEY_INVALID"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			e := newCopyEnv(t)
			detail := map[string]any{"msg": tc.msg, "loc": []string{"query", tc.field}}
			if tc.errorCode != "" {
				detail["error_code"] = tc.errorCode
			}
			body, _ := json.Marshal(map[string]any{"detail": detail})
			e.mock.RegisterHandler("GET", copyTestPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(body))
			})
			stdout, _, err := runCopy(t, "--table-id", "42", "--series", copySeriesID, "--period-key", "2026-Q4", "--dry-run")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error lost the server message: %v", err)
			}
			if strings.Contains(stdout, "Copy table") {
				t.Errorf("plan printed despite the refusal:\n%s", stdout)
			}
			e.assertRequests(t, copyTestGET)
		})
	}
}

// With a series the new table's upload source is named after the derived
// name, so a data source copy may take the suggested name's but not that one.
func TestCopyTable_SeriesDataSourceNameClashUsesTheDerivedName(t *testing.T) {
	for _, tc := range []struct {
		name, sourceName string
		clash            bool
	}{
		{name: "derived", sourceName: copySeriesName + " Source", clash: true},
		{name: "suggested", sourceName: "Orders (2) Source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCopyEnv(t)
			serveCopySeriesPreview(e)
			file := writeDataSourcesFile(t, "- source_id: 13\n  name: \""+tc.sourceName+"\"\n")
			_, _, err := runCopy(t, "--table-id", "42", "--series", copySeriesID, "--period-key", "2026-Q4",
				"--data-sources-file", file, "--yes")
			if tc.clash {
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "already used") {
					t.Fatalf("exit = %d, want 2 for a reused name; error = %v", code, err)
				}
				e.assertRequests(t, copyTestGET)
				return
			}
			if err != nil {
				t.Fatalf("copy: %v", err)
			}
			e.assertRequests(t, copyTestGET, copyTestPOST)
		})
	}
}

// The preview checks the table name at the source table's location only, so
// the plan says so when the copy goes elsewhere.
func TestCopyTable_SeriesPlanNotesAPreviewAtAnotherLocation(t *testing.T) {
	const note = "previewed at the source table's location"
	for _, tc := range []struct {
		name  string
		extra []string
		want  bool
	}{
		{name: "source location"},
		{name: "schema", extra: []string{"--schema-name", "eu"}, want: true},
		{name: "database", extra: []string{"--database-name", "warehouse"}, want: true},
		{name: "destination", extra: []string{"--destination-id", "9"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCopyEnv(t)
			serveCopySeriesPreview(e)
			args := append([]string{"--table-id", "42", "--series", copySeriesID, "--period-key", "2026-Q4", "--dry-run"}, tc.extra...)
			stdout, _, err := runCopy(t, args...)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := strings.Contains(stdout, note); got != tc.want {
				t.Errorf("note shown = %v, want %v:\n%s", got, tc.want, stdout)
			}
		})
	}
}

func TestCopyTable_SeriesErrorsPreserveExitCodesAndFieldPaths(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		field, msg   string
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized, "series", "deliverable:admin required"},
		{http.StatusNotFound, qerrors.ExitNotFound, "series.series_id", "series not in this organization"},
		{http.StatusConflict, qerrors.ExitBadUsage, "series.period_key", "period already held by another table"},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage, "series.period_key", "invalid period"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			e := newCopyEnv(t)
			e.postStatus = tc.status
			body, _ := json.Marshal(map[string]any{"detail": map[string]any{
				"msg": tc.msg, "loc": append([]string{"body"}, strings.Split(tc.field, ".")...), "error_code": "SERIES_ERROR",
			}})
			e.postBody = string(body)
			_, _, err := runCopy(t, "--table-id", "42", "--series", copySeriesID, "--period-key", "K", "--yes")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error lost field/message: %v", err)
			}
			e.assertRequests(t, copyTestGET, copyTestPOST)
		})
	}
}
