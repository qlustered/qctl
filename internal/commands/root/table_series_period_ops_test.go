package root

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// resetAllCommandFlags restores every local flag of the command at path to its
// default now and when t ends.
func resetAllCommandFlags(t *testing.T, path ...string) {
	t.Helper()
	reset := func() {
		cmd, _, err := GetRootCmd().Find(path)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if sv, ok := flag.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		})
	}
	reset()
	t.Cleanup(reset)
}

const periodOpsLegacyPath = seriesTestPath + "/" + seriesTestID + "/periods/FY/Z"

// periodOpsServer is tableSeriesTestServer on an unprotected context, with
// every period-op flag reset.
func periodOpsServer(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	mock := tableSeriesTestServer(t)
	setCurrentContextProtected(t, testutil.BoolPtr(false))
	resetAllCommandFlags(t, "table-series", "delete-period")
	resetAllCommandFlags(t, "table-series", "recreate-period")
	resetAllCommandFlags(t, "get", "table-series")
	return mock
}

func setCurrentContextProtected(t *testing.T, protected *bool) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts[cfg.CurrentContext].Protected = protected
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestTableSeriesPeriodOpsRequireANamedProtectedContext(t *testing.T) {
	for _, verb := range []string{"delete-period", "recreate-period"} {
		t.Run(verb, func(t *testing.T) {
			cmd, _, err := GetRootCmd().Find([]string{"table-series", verb})
			if err != nil || !cmdutil.IsDestructive(cmd) {
				t.Fatalf("%s is not destructive: %v", verb, err)
			}
			mock := periodOpsServer(t)
			setCurrentContextProtected(t, nil)
			_, err = runRoot(t, "table-series", verb, seriesTestID, "--period", "2026-09", "--yes")
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--context") {
				t.Fatalf("err = %v, want the protected-context refusal", err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

// requireEscapedPeriod fails unless the request sent the period key FY/Z as
// one escaped path segment.
func requireEscapedPeriod(t *testing.T, r *http.Request) {
	t.Helper()
	if !strings.Contains(r.URL.EscapedPath(), "/periods/FY%2FZ") {
		t.Errorf("escaped path = %q, want the period as one segment", r.URL.EscapedPath())
	}
}

const periodDeleteResponse = `{"dataset_id":9007199254740993,"table_name":"legacy_table_9007199254740993_deleted",
 "runs_to_reissue":[{"run_id":31,"deliverable_id":"dddddddd-2222-3333-4444-555555555555","deliverable_name":"Q3 pack"}],
 "moved_source_templates":[{"data_source_model_id":7,"name":"Carrier S3","template_dataset_id":42}]}`

func TestTableSeriesDeletePeriodFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := periodOpsServer(t)
			mock.RegisterHandler(http.MethodDelete, periodOpsLegacyPath, func(w http.ResponseWriter, r *http.Request) {
				requireEscapedPeriod(t, r)
				if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
					t.Errorf("Authorization = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(periodDeleteResponse))
			})
			out, err := runRoot(t, "table-series", "delete-period", seriesTestID, "--period", "FY/Z", "--yes", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"GET " + seriesTestPath + "/" + seriesTestID, "DELETE " + periodOpsLegacyPath}
			if got := mock.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			switch format {
			case "table":
				for _, s := range []string{"table/9007199254740993", "deleted", "legacy_table_9007199254740993_deleted",
					"cloud-source/7", "table/42", "deliverable-run/31"} {
					if !strings.Contains(out, s) {
						t.Errorf("output lacks %q:\n%s", s, out)
					}
				}
			case "name":
				if out != "table/9007199254740993\n" {
					t.Errorf("output = %q", out)
				}
			case "json":
				assertSeriesJSON(t, out, periodDeleteResponse)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, periodDeleteResponse)
			}
		})
	}
}

func TestTableSeriesDeletePeriodUnknownPeriodIsNotFoundBeforeAnyWrite(t *testing.T) {
	mock := periodOpsServer(t)
	_, err := runRoot(t, "table-series", "delete-period", seriesTestID, "--period", "2031-01", "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit = %d (%v), want not found", got, err)
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %v, want the series read only", got)
	}
}

func TestTableSeriesDeletePeriodBlockedNamesEachBlocker(t *testing.T) {
	mock := periodOpsServer(t)
	mock.RegisterHandler(http.MethodDelete, periodOpsLegacyPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
			"msg": "Can not delete this period. This period is locked.", "error_code": "SERIES_PERIOD_CHANGE_BLOCKED",
			"error_id": "e-1",
			"context": map[string]any{"blockers": []any{
				map[string]any{"kind": "period_locked", "msg": "This period is locked.", "refs": []any{}},
				map[string]any{"kind": "active_jobs", "msg": "Jobs are running.", "refs": []any{}},
				map[string]any{"kind": "deliverable_revision", "msg": "Bound by a revision.", "refs": []any{
					map[string]any{"deliverable_id": "eeeeeeee-2222-3333-4444-555555555555", "name": "Q4 pack", "release": 3}}},
				map[string]any{"kind": "composite_spec", "msg": "Read by a linked table.", "refs": []any{
					map[string]any{"composite_spec_group_id": "g", "dataset_id": 77, "name": "Enriched", "reads_as": "spine"}}},
				map[string]any{"kind": "deliverable_run", "msg": "Read by runs.", "refs": []any{
					map[string]any{"deliverable_id": "dddddddd-2222-3333-4444-555555555555", "name": "Q3 pack", "run_ids": []int{31, 32}}}},
			}},
		}})
	})
	_, err := runRoot(t, "table-series", "delete-period", seriesTestID, "--period", "FY/Z", "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	for _, s := range []string{"qctl table-series unlock", "qctl get ingestion-jobs --table-id",
		"qctl describe deliverable eeeeeeee-2222-3333-4444-555555555555", "qctl get linked-table 77",
		"qctl describe deliverable-run 31", "qctl describe deliverable-run 32"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error lacks %q:\n%v", s, err)
		}
	}
}

const periodRecreateResponse = `{"dataset_id":501,"dataset_copy_id":12,"replaced_dataset_id":9007199254740993,
 "replaced_table_name":"legacy_table_9007199254740993_deleted","runs_to_reissue":[],
 "stayed_with_replaced_table":{"unresolved_alerts":2,"comment_threads":0,"data_reviews":0,"data_action_reviews":0,
   "row_ignores":1,"duplicate_groups":0,"duplicate_group_responses":0,"steward_fixes":3},
 "moved_data_sources":[{"id":7,"name":"Carrier S3"}],"moved_stored_item_ids":[90,91],
 "file_reloads":[{"stored_item_id":90,"outcome":"reloaded","ingestion_job_id":700,"reason":null},
   {"stored_item_id":91,"outcome":"held_until_first_migration","ingestion_job_id":701,"reason":null}],
 "skipped_rules":[{"dataset_rule_id":"r-1","rule_revision_id":null,"name":"Premium check","missing_columns":["premium"],"note":null}]}`

func TestTableSeriesRecreatePeriodSendsTheFlagsAndReloadsEveryListedFile(t *testing.T) {
	mock := periodOpsServer(t)
	mock.RegisterHandler(http.MethodGet, periodOpsLegacyPath+"/reloadable-files", func(w http.ResponseWriter, r *http.Request) {
		requireEscapedPeriod(t, r)
		testutil.RespondJSON(w, http.StatusOK, []map[string]any{
			{"id": 90, "key": "a.csv", "data_source_model_id": 7, "created_at": "2026-09-02T10:00:00Z",
				"is_ever_loaded": true, "clean_rows_count": 10, "bad_rows_count": 0},
			{"id": 91, "key": nil, "data_source_model_id": 7, "created_at": "2026-09-03T10:00:00Z",
				"is_ever_loaded": false, "clean_rows_count": nil, "bad_rows_count": nil},
		})
	})
	mock.RegisterHandler(http.MethodPost, periodOpsLegacyPath+"/recreate", func(w http.ResponseWriter, r *http.Request) {
		requireEscapedPeriod(t, r)
		want := map[string]any{
			"schema": "learn_from_file", "copy_from_period_key": "2026-09", "columns_to_leave_out": nil,
			"key_columns": nil, "include": []any{"rules", "value_maps"}, "data_loading_process": "mirror",
			"reload_stored_item_ids": []any{float64(90), float64(91)},
		}
		if got := decodeSeriesBody(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %#v\nwant %#v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(periodRecreateResponse))
	})
	out, err := runRoot(t, "table-series", "recreate-period", seriesTestID, "--period", "FY/Z", "--copy-from", "2026-09",
		"--schema", "learn-from-file", "--loading-process", "mirror", "--include", "rules,value-maps",
		"--reload-all-files", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"table/501 recreated", "replacing table/9007199254740993", "cloud-source/7",
		"2 unresolved alerts (now resolved), 1 row ignores, 3 cells with steward fixes", "Premium check (missing premium)",
		"file/90 reloaded ingestion-job/700", "file/91 held_until_first_migration ingestion-job/701",
		"Next: qctl get alerts --table-id 501"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

func TestTableSeriesRecreatePeriodNameIsTheNewTable(t *testing.T) {
	mock := periodOpsServer(t)
	mock.RegisterHandler(http.MethodPost, seriesTestPath+"/"+seriesTestID+"/periods/2026-09/recreate",
		func(w http.ResponseWriter, r *http.Request) {
			want := map[string]any{"schema": "copy", "copy_from_period_key": nil, "columns_to_leave_out": nil,
				"key_columns": nil, "include": nil, "reload_stored_item_ids": []any{float64(90)},
				"no_key_columns": true}
			if got := decodeSeriesBody(t, r); !reflect.DeepEqual(got, want) {
				t.Errorf("body = %#v\nwant %#v", got, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(periodRecreateResponse))
		})
	out, err := runRoot(t, "table-series", "recreate-period", seriesTestID, "--period", "2026-09",
		"--no-key-columns", "--reload-file", "file/90", "--yes", "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	if out != "table/501\n" {
		t.Errorf("output = %q", out)
	}
}

func TestTableSeriesRecreatePeriodRefusesBadFlagsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"leave-out without copy-without-columns": {"--leave-out-column", "a"},
		"copy-without-columns without columns":   {"--schema", "copy-without-columns"},
		"keys with learn-from-file":              {"--schema", "learn_from_file", "--key-column", "id"},
		"no-keys with learn-from-file":           {"--schema", "learn-from-file", "--no-key-columns"},
		"keys and no-keys":                       {"--key-column", "id", "--no-key-columns"},
		"unknown schema":                         {"--schema", "infer"},
		"composite loading process":              {"--loading-process", "composite"},
		"settings in include":                    {"--include", "settings"},
		"unknown include":                        {"--include", "everything"},
		"reload file and all":                    {"--reload-file", "1", "--reload-all-files"},
		"non-positive reload file":               {"--reload-file", "0"},
		"table reference as reload file":         {"--reload-file", "table/5"},
		"blank copy-from":                        {"--copy-from", " 2026-08"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := periodOpsServer(t)
			args := append([]string{"table-series", "recreate-period", seriesTestID, "--period", "2026-09", "--yes"}, args...)
			_, err := runRoot(t, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (%v), want 2", got, err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestGetTableSeriesReloadableFiles(t *testing.T) {
	for _, format := range []string{"table", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := periodOpsServer(t)
			mock.RegisterHandler(http.MethodGet, periodOpsLegacyPath+"/reloadable-files", func(w http.ResponseWriter, r *http.Request) {
				requireEscapedPeriod(t, r)
				testutil.RespondJSON(w, http.StatusOK, []map[string]any{
					{"id": 90, "key": "a.csv", "data_source_model_id": 7, "created_at": "2026-09-02T10:00:00Z",
						"is_ever_loaded": true, "clean_rows_count": 10, "bad_rows_count": 0},
				})
			})
			out, err := runRoot(t, "get", "table-series", seriesTestID, "--reloadable-files", "--period", "FY/Z", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "name" {
				if out != "file/90\n" {
					t.Errorf("output = %q", out)
				}
				return
			}
			for _, s := range []string{"90", "a.csv", "2026-09-02T10:00:00Z"} {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
		})
	}
}

func TestGetTableSeriesPeriodNeedsReloadableFiles(t *testing.T) {
	for name, args := range map[string][]string{
		"period alone":              {"get", "table-series", seriesTestID, "--period", "2026-09"},
		"reloadable without period": {"get", "table-series", seriesTestID, "--reloadable-files"},
		"reloadable without series": {"get", "table-series", "--reloadable-files", "--period", "2026-09"},
	} {
		t.Run(name, func(t *testing.T) {
			periodOpsServer(t)
			_, err := runRoot(t, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (%v), want 2", got, err)
			}
		})
	}
}
