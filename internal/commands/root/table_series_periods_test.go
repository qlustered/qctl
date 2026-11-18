package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const submissionPeriodsPath = seriesTestPath + "/" + seriesTestID + "/periods"

const submissionPeriodsRecord = `[
  {"dataset_id":9007199254740993,"period_key":"FY/Z","period_start":"2026-08-01","period_end":"2026-08-31","is_locked":false,"locked_at":null,"creatable":false},
  {"dataset_id":42,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30","is_locked":true,"locked_at":"2026-10-02T09:00:00Z","creatable":false}
]`

func submissionPeriodsServer(t *testing.T, record string) *testutil.MockAPIServer {
	t.Helper()
	mock := tableSeriesTestServer(t)
	cmd, _, err := GetRootCmd().Find([]string{"get", "table-series"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f := cmd.Flags().Lookup("periods")
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, path := range []string{seriesTestPath, seriesTestPath + "/" + seriesTestID} {
		mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("periods listing attempted an admin-only series read")
			testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "series reads forbidden"})
		})
	}
	mock.RegisterHandler("GET", submissionPeriodsPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected periods query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(record))
	})
	return mock
}

func assertOnlyPeriodsRequest(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + submissionPeriodsPath}) {
		t.Errorf("requests = %v, want only submission periods", got)
	}
}

func TestGetTableSeriesSubmissionPeriodsFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := submissionPeriodsServer(t, submissionPeriodsRecord)
			ref := seriesTestID
			if format == "yaml" {
				ref = "table-series/" + ref
			}
			out, err := runRoot(t, "get", "table-series", ref, "--periods", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			assertOnlyPeriodsRequest(t, mock)
			switch format {
			case "table":
				for _, want := range []string{"PERIOD-KEY", "DATASET-ID", "IS-LOCKED", "FY/Z", "9007199254740993", "false", "2026-09", "42", "2026-09-01", "2026-09-30", "true"} {
					if !strings.Contains(out, want) {
						t.Errorf("output lacks %q: %s", want, out)
					}
				}
				if strings.Index(out, "FY/Z") > strings.Index(out, "2026-09") || strings.Contains(out, "0001-01-01") {
					t.Errorf("period ordering or nullable dates changed: %s", out)
				}
			case "name":
				if want := "table/9007199254740993\ntable/42\n"; out != want {
					t.Errorf("name output = %q, want %q", out, want)
				}
			default:
				want := fmt.Sprintf(`{"kind":"List","items":%s,"page":null,"limit":null,"total":null,"next_page":null}`, submissionPeriodsRecord)
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				} else {
					assertSeriesJSON(t, out, want)
				}
			}
		})
	}
}

func TestGetTableSeriesSubmissionPeriodsColumns(t *testing.T) {
	mock := submissionPeriodsServer(t, submissionPeriodsRecord)
	out, err := runRoot(t, "get", "table-series", seriesTestID, "--periods", "--columns", "period_key,period_start,period_end,is_locked", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"FY/Z", "2026-08-01", "2026-08-31", "false", "2026-09", "2026-09-01", "2026-09-30", "true"}; !reflect.DeepEqual(strings.Fields(out), want) {
		t.Errorf("columns = %q, want %v", out, want)
	}
	assertOnlyPeriodsRequest(t, mock)
}

// A period with no table yet shows "-" for its table and is left out of -o
// name; the creatable column says which it is.
func TestGetTableSeriesSubmissionPeriodsWithNoTable(t *testing.T) {
	record := `[
  {"dataset_id":42,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30","is_locked":false,"creatable":false},
  {"dataset_id":null,"period_key":"2026-10","period_start":"2026-10-01","period_end":"2026-10-31","is_locked":false,"creatable":true}
]`
	submissionPeriodsServer(t, record)
	out, err := runRoot(t, "get", "table-series", seriesTestID, "--periods", "--columns", "period_key,dataset_id,creatable", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-09", "42", "false", "2026-10", "-", "true"}; !reflect.DeepEqual(strings.Fields(out), want) {
		t.Errorf("columns = %q, want %v", out, want)
	}

	submissionPeriodsServer(t, record)
	out, err = runRoot(t, "get", "table-series", seriesTestID, "--periods", "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	if out != "table/42\n" {
		t.Errorf("name output = %q, want only the period with a table", out)
	}
}

func TestGetTableSeriesSubmissionPeriodsEmpty(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := submissionPeriodsServer(t, "[]")
			out, err := runRoot(t, "get", "table-series", seriesTestID, "--periods", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			assertOnlyPeriodsRequest(t, mock)
			if format == "table" || format == "name" {
				if out != "" {
					t.Errorf("empty output = %q", out)
				}
				return
			}
			want := `{"kind":"List","items":[],"page":null,"limit":null,"total":null,"next_page":null}`
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, out, want)
			} else {
				assertSeriesJSON(t, out, want)
			}
		})
	}
}

func TestGetTableSeriesSubmissionPeriodsErrors(t *testing.T) {
	for _, tc := range []struct{ status, code int }{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock := submissionPeriodsServer(t, submissionPeriodsRecord)
			mock.RegisterHandler("GET", submissionPeriodsPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "period listing refused", "error_code": "PERIOD_ERROR"}})
			})
			out, err := runRoot(t, "get", "table-series", seriesTestID, "--periods")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "period listing refused") {
				t.Errorf("out = %q, error = %v", out, err)
			}
			assertOnlyPeriodsRequest(t, mock)
		})
	}
}

func TestGetTableSeriesSubmissionPeriodsValidationBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"--periods"},
		{"Monthly bordereau", "--periods"},
		{seriesTestID[:8], "--periods"},
		{"x" + seriesTestID + "y", "--periods"},
		{"table/42", "--periods"},
		{seriesTestID, "--periods", "--columns", "name"},
		{seriesTestID, "--periods", "--columns", "name", "-o", "json"},
		{seriesTestID, "--periods", "--columns", "bogus"},
		{seriesTestID, "--columns", "dataset_id"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, append([]string{"get", "table-series"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}
