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

const uploadableSeriesPath = seriesTestPath + "/uploadable"

const uploadableSeriesRecord = `[
  {"id":"11111111-2222-3333-4444-555555555555","name":"Monthly bordereau","frequency":"monthly",
   "data_source_model_id":9007199254740993,"routing_mode":"uploader_chooses","periods":[
     {"period_key":"2026-10","label":"Oct 2026","dataset_id":null},
     {"period_key":"2026-09","label":"Sep 2026","dataset_id":9007199254740995}]},
  {"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","name":"Quarterly reports","frequency":"quarterly",
   "data_source_model_id":42,"routing_mode":"file_name","periods":[]}
]`

func uploadableSeriesServer(t *testing.T, record string) *testutil.MockAPIServer {
	t.Helper()
	mock := tableSeriesTestServer(t)
	cmd, _, err := GetRootCmd().Find([]string{"get", "table-series"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f := cmd.Flags().Lookup("uploadable")
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	mock.RegisterHandler("GET", uploadableSeriesPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected uploadable query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(record))
	})
	return mock
}

func assertOnlyUploadableSeriesRequest(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + uploadableSeriesPath}) {
		t.Errorf("requests = %v, want only uploadable series", got)
	}
}

func TestGetTableSeriesUploadableFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := uploadableSeriesServer(t, uploadableSeriesRecord)
			out, err := runRoot(t, "get", "table-series", "--uploadable", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			assertOnlyUploadableSeriesRequest(t, mock)
			switch format {
			case "table":
				for _, want := range []string{"CLOUD-SOURCE-ID", "ROUTING-MODE", "PERIOD-KEY", "LABEL", "TABLE-ID", "9007199254740993", "9007199254740995", "uploader_chooses", "Oct 2026", "Sep 2026", "Quarterly reports", "quarterly", "file_name"} {
					if !strings.Contains(out, want) {
						t.Errorf("output lacks %q: %s", want, out)
					}
				}
				if strings.Index(out, "2026-10") > strings.Index(out, "2026-09") || strings.Index(out, "2026-09") > strings.Index(out, "Quarterly reports") {
					t.Errorf("server order changed: %s", out)
				}
			case "name":
				if want := "table-series/" + seriesTestID + "\ntable-series/" + emptySeriesTestID + "\n"; out != want {
					t.Errorf("names = %q, want %q", out, want)
				}
			default:
				want := fmt.Sprintf(`{"kind":"List","items":%s,"page":null,"limit":null,"total":null,"next_page":null}`, uploadableSeriesRecord)
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				} else {
					assertSeriesJSON(t, out, want)
				}
			}
		})
	}
}

func TestGetTableSeriesUploadableColumns(t *testing.T) {
	mock := uploadableSeriesServer(t, uploadableSeriesRecord)
	out, err := runRoot(t, "get", "table-series", "--uploadable", "--columns", "id,cloud_source_id,period_key,table_id", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		seriesTestID, "9007199254740993", "2026-10", "-",
		seriesTestID, "9007199254740993", "2026-09", "9007199254740995",
		emptySeriesTestID, "42", "-", "-",
	}
	if !reflect.DeepEqual(strings.Fields(out), want) {
		t.Errorf("columns = %q, want %v", out, want)
	}
	assertOnlyUploadableSeriesRequest(t, mock)
}

func TestGetTableSeriesUploadableEmpty(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := uploadableSeriesServer(t, "[]")
			out, err := runRoot(t, "get", "table-series", "--uploadable", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			assertOnlyUploadableSeriesRequest(t, mock)
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

func TestGetTableSeriesUploadableErrors(t *testing.T) {
	for _, tc := range []struct{ status, code int }{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock := uploadableSeriesServer(t, uploadableSeriesRecord)
			mock.RegisterHandler("GET", uploadableSeriesPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "upload targets refused", "error_code": "UPLOAD_ERROR"}})
			})
			out, err := runRoot(t, "get", "table-series", "--uploadable")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "upload targets refused") {
				t.Errorf("out = %q, error = %v", out, err)
			}
			assertOnlyUploadableSeriesRequest(t, mock)
		})
	}
}

func TestGetTableSeriesUploadableValidationBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"--uploadable", seriesTestID},
		{"--uploadable", "Monthly bordereau"},
		{"--uploadable", "--periods"},
		{"--uploadable", "--rule-approval"},
		{"--uploadable", "--periods=false"},
		{"--uploadable=false", "--rule-approval", seriesTestID},
		{"--uploadable", "--columns", "bogus"},
		{"--uploadable", "--columns", "is_locked"},
		{"--uploadable", "--columns", "is_locked", "-o", "json"},
		{"--columns", "cloud_source_id"},
		{seriesTestID, "--periods", "--columns", "cloud_source_id"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, append([]string{"get", "table-series"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}
