package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestTableSeriesPeriodColumnValidatesBeforeCredentials(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		t.Run(verb, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "series.yaml")
			if err := os.WriteFile(path, []byte(seriesWriteManifest+"  period_column: report_month\n  members: []\n"), 0600); err != nil {
				t.Fatal(err)
			}
			err := runOutsideGetWithoutContext(t, verb, "-f", path)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "supplied together") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestTableSeriesPeriodColumnPatchFollowsMembershipWrites(t *testing.T) {
	for _, create := range []bool{false, true} {
		for _, refuse := range []bool{false, true} {
			t.Run(fmt.Sprintf("create=%t/refuse=%t", create, refuse), func(t *testing.T) {
				mock, path := seriesWriteSetup(t, "  period_column: report_date\n  period_column_kind: date\n  members:\n    - table: 42\n      period_key: 2026-01\n")
				var series table_series.Series
				if err := json.Unmarshal([]byte(seriesTestRecord), &series); err != nil {
					t.Fatal(err)
				}
				id := seriesTestID
				if create {
					id = emptySeriesTestID
					series = table_series.Series{ID: uuid.MustParse(id), Name: "Monthly bordereau", Members: []table_series.Member{}}
					mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
						testutil.RespondJSON(w, http.StatusOK, []table_series.Series{})
					})
					mock.RegisterHandler("POST", seriesTestPath, func(w http.ResponseWriter, r *http.Request) {
						if body := decodeSeriesBody(t, r); !reflect.DeepEqual(body, map[string]any{"name": series.Name, "description": nil}) {
							t.Errorf("create body = %#v", body)
						}
						testutil.RespondJSON(w, http.StatusCreated, series)
					})
				}
				base := seriesTestPath + "/" + id
				memberMethod, memberPath, memberStatus := "PATCH", base+"/members/42", http.StatusOK
				if create {
					memberMethod, memberPath, memberStatus = "POST", base+"/members", http.StatusCreated
				}
				mock.RegisterHandler(memberMethod, memberPath, func(w http.ResponseWriter, r *http.Request) {
					body := decodeSeriesBody(t, r)
					_, start := body["period_start"]
					_, end := body["period_end"]
					if body["period_key"] != "2026-01" || start || end {
						t.Errorf("member body = %#v, want the period key and no dates", body)
					}
					series.Members = []table_series.Member{{DatasetID: 42, PeriodKey: "2026-01"}}
					testutil.RespondJSON(w, memberStatus, series.Members[0])
				})
				mock.RegisterHandler("DELETE", base+"/members/9007199254740993", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
				mock.RegisterHandler("PATCH", base, func(w http.ResponseWriter, r *http.Request) {
					if body := decodeSeriesBody(t, r); !reflect.DeepEqual(body, map[string]any{"period_column": "report_date", "period_column_kind": "date"}) {
						t.Errorf("period patch = %#v", body)
					}
					if refuse {
						testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
							"msg": "Column missing from a period table", "error_code": "SERIES_PERIOD_COLUMN_NOT_IN_SCHEMA",
						}})
						return
					}
					column, kind := "report_date", api.Date
					series.PeriodColumn, series.PeriodColumnKind = &column, &kind
					testutil.RespondJSON(w, http.StatusOK, series)
				})
				mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, series)
				})
				preview, err := runRoot(t, "diff", "-f", path)
				if qerrors.GetExitCode(err) != 1 || len(seriesMutationRequests(mock)) != 0 {
					t.Fatalf("diff = %s, error = %v, requests = %v", preview, err, mock.Requests())
				}
				previewID := id
				if create {
					previewID = "<created-series-id>"
				}
				if !strings.Contains(preview, "PATCH /table-series/"+previewID+"\n") || strings.LastIndex(preview, `"period_column"`) < strings.LastIndex(preview, "/members") {
					t.Fatalf("period PATCH missing or before members: %s", preview)
				}
				out, err := runRoot(t, "apply", "-f", path, "--yes", "-o", "json")
				wantCode := 0
				if refuse {
					wantCode = 2
				}
				if qerrors.GetExitCode(err) != wantCode || !json.Valid([]byte(out)) {
					t.Fatalf("apply = %s, error = %v", out, err)
				}
				if refuse && !strings.Contains(err.Error(), "SERIES_PERIOD_COLUMN_NOT_IN_SCHEMA") {
					t.Fatalf("lost backend diagnostic: %v", err)
				}
				want := []string{memberMethod + " " + memberPath}
				if create {
					want = append([]string{"POST " + seriesTestPath}, want...)
				} else {
					want = append(want, "DELETE "+base+"/members/9007199254740993")
				}
				want = append(want, "PATCH "+base)
				if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, want) {
					t.Fatalf("writes = %v, want %v", got, want)
				}
				var result table_series.Manifest
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result.Spec.PeriodColumn, series.PeriodColumn) || !reflect.DeepEqual(result.Spec.PeriodColumnKind, series.PeriodColumnKind) {
					t.Fatalf("result lost period setting: %s", out)
				}
			})
		}
	}
}
