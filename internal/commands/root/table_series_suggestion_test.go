package root

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const seriesSuggestionPath = seriesTestPath + "/suggestion"

const seriesSuggestionRecord = `{"series_name":"Orders export","frequency":"monthly","period_key":"2026-10",
  "period_source":"export_date","name":"Orders export · Oct 2026 (2)","table_name":"orders_export_2026_10"}`

// resetSeriesSuggestionFlags restores the command's local flags after the test, since the tree is shared.
func resetSeriesSuggestionFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"get", "table-series-suggestion"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range []string{"series-name", "frequency", "period-key", "columns"} {
			f := cmd.Flags().Lookup(name)
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
}

// seriesSuggestionServer answers the suggestion route with status and body, recording each request body in bodies.
func seriesSuggestionServer(t *testing.T, status int, record string, bodies *[]map[string]any) *testutil.MockAPIServer {
	t.Helper()
	mock := tableSeriesTestServer(t)
	resetSeriesSuggestionFlags(t)
	mock.RegisterHandler("POST", seriesSuggestionPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
			t.Errorf("Authorization = %q", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("body %s: %v", raw, err)
		}
		*bodies = append(*bodies, body)
		testutil.RespondJSON(w, status, json.RawMessage(record))
	})
	return mock
}

func assertOnlySeriesSuggestionRequest(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"POST " + seriesSuggestionPath}) {
		t.Errorf("requests = %v, want only the suggestion POST", got)
	}
}

func TestGetTableSeriesSuggestionFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var bodies []map[string]any
			mock := seriesSuggestionServer(t, http.StatusOK, seriesSuggestionRecord, &bodies)
			out, err := runRoot(t, "get", "table-series-suggestion", "Orders export 2026-11-03.csv", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			assertOnlySeriesSuggestionRequest(t, mock)
			want := map[string]any{"file_name": "Orders export 2026-11-03.csv", "series_name": nil, "period_key": nil}
			if !reflect.DeepEqual(bodies, []map[string]any{want}) {
				t.Errorf("bodies = %v, want %v", bodies, want)
			}
			switch format {
			case "table":
				for _, want := range []string{"SERIES-NAME", "FREQUENCY", "PERIOD-KEY", "PERIOD-SOURCE", "NAME", "TABLE-NAME", "Orders export", "monthly", "2026-10", "export_date", "Orders export · Oct 2026 (2)", "orders_export_2026_10"} {
					if !strings.Contains(out, want) {
						t.Errorf("output lacks %q: %s", want, out)
					}
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, seriesSuggestionRecord)
			default:
				assertSeriesJSON(t, out, seriesSuggestionRecord)
			}
		})
	}
}

func TestGetTableSeriesSuggestionSendsOnlyTheFileName(t *testing.T) {
	for path, want := range map[string]string{
		"/home/broker/exports/Orders 2026-10.csv": "Orders 2026-10.csv",
		`C:\Users\broker\Orders Q3.xlsx`:          "Orders Q3.xlsx",
		"exports/../Orders.csv":                   "Orders.csv",
	} {
		t.Run(want, func(t *testing.T) {
			var bodies []map[string]any
			mock := seriesSuggestionServer(t, http.StatusOK, seriesSuggestionRecord, &bodies)
			if _, err := runRoot(t, "get", "table-series-suggestion", path); err != nil {
				t.Fatal(err)
			}
			assertOnlySeriesSuggestionRequest(t, mock)
			if len(bodies) != 1 || bodies[0]["file_name"] != want {
				t.Errorf("bodies = %v, want file_name %q", bodies, want)
			}
		})
	}
}

func TestGetTableSeriesSuggestionOverrides(t *testing.T) {
	var bodies []map[string]any
	mock := seriesSuggestionServer(t, http.StatusOK, seriesSuggestionRecord, &bodies)
	_, err := runRoot(t, "get", "table-series-suggestion", "--series-name", "Quarterly orders", "--frequency", "quarterly", "--period-key", "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	assertOnlySeriesSuggestionRequest(t, mock)
	want := map[string]any{"file_name": nil, "series_name": "Quarterly orders", "frequency": "quarterly", "period_key": "2026-Q3"}
	if !reflect.DeepEqual(bodies, []map[string]any{want}) {
		t.Errorf("bodies = %v, want %v", bodies, want)
	}
}

func TestGetTableSeriesSuggestionNullsShowDash(t *testing.T) {
	var bodies []map[string]any
	record := `{"series_name":null,"frequency":"monthly","period_key":"2026-10","period_source":null,"name":null,"table_name":null}`
	seriesSuggestionServer(t, http.StatusOK, record, &bodies)
	out, err := runRoot(t, "get", "table-series-suggestion", "--period-key", "2026-10", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(out), []string{"-", "monthly", "2026-10", "-", "-", "-"}; !reflect.DeepEqual(got, want) {
		t.Errorf("row = %q, want %v", got, want)
	}
}

func TestGetTableSeriesSuggestionErrors(t *testing.T) {
	for _, tc := range []struct{ status, code int }{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var bodies []map[string]any
			record := `{"detail":{"msg":"suggestion refused","error_code":"SERIES_PERIOD_KEY_INVALID"}}`
			mock := seriesSuggestionServer(t, tc.status, record, &bodies)
			out, err := runRoot(t, "get", "table-series-suggestion", "orders.csv", "--period-key", "2026-13")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "suggestion refused") {
				t.Errorf("out = %q, error = %v", out, err)
			}
			assertOnlySeriesSuggestionRequest(t, mock)
		})
	}
}

func TestGetTableSeriesSuggestionValidationBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"a.csv", "b.csv"},
		{"exports/"},
		{strings.Repeat("x", 1025)},
		{"--series-name", ""},
		{"--series-name", strings.Repeat("x", 201)},
		{"--frequency", "weekly"},
		{"--frequency", ""},
		{"--period-key", ""},
		{"--period-key", " 2026-10"},
		{"orders.csv", "--columns", "bogus"},
		{"orders.csv", "-o", "name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetSeriesSuggestionFlags(t)
			err := runRootWithoutContext(t, append([]string{"get", "table-series-suggestion"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}
