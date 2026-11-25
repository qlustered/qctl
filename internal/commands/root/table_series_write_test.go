package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const seriesWriteManifest = "apiVersion: qluster.ai/v1\nkind: TableSeries\nmetadata:\n  name: Monthly bordereau\nspec:\n"

func resetSeriesWriteFlags(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{{"apply"}, {"delete", "table-series"}} {
		cmd, _, err := GetRootCmd().Find(args)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"yes", "filename"} {
			if f := cmd.Flags().Lookup(name); f != nil {
				if err := cmd.Flags().Set(name, f.DefValue); err != nil {
					t.Fatal(err)
				}
				f.Changed = false
			}
		}
	}
}

func seriesWriteSetup(t *testing.T, spec string) (*testutil.MockAPIServer, string) {
	t.Helper()
	mock := tableSeriesTestServer(t)
	registerSeriesList(t, mock, unlockedSeriesTestRecord)
	resetSeriesWriteFlags(t)
	t.Cleanup(func() { resetSeriesWriteFlags(t) })
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	allow := false
	cfg.Contexts[cfg.CurrentContext].Protected = &allow
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "series.yaml")
	if err := os.WriteFile(path, []byte(seriesWriteManifest+spec), 0600); err != nil {
		t.Fatal(err)
	}
	return mock, path
}

func decodeSeriesBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func seriesMutationRequests(mock *testutil.MockAPIServer) []string {
	var writes []string
	for _, r := range mock.Requests() {
		if !strings.HasPrefix(r, "GET ") {
			writes = append(writes, r)
		}
	}
	return writes
}

func TestTableSeriesApplyCreateAndMoveIntoNewSeries(t *testing.T) {
	for _, moving := range []bool{false, true} {
		t.Run(map[bool]string{false: "add", true: "move"}[moving], func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  description: Reports\n  members:\n    - table: 42\n      period_key: Q1\n")
			var series table_series.Series
			if err := json.Unmarshal([]byte(unlockedSeriesTestRecord), &series); err != nil {
				t.Fatal(err)
			}
			mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
				items := []table_series.Series{}
				if moving {
					series.Name = "Old series"
					items = append(items, series)
				}
				testutil.RespondJSON(w, http.StatusOK, items)
			})
			created := table_series.Series{ID: uuid.MustParse(emptySeriesTestID), Name: "Monthly bordereau", Members: []table_series.Member{}}
			mock.RegisterHandler("POST", seriesTestPath, func(w http.ResponseWriter, r *http.Request) {
				if got := decodeSeriesBody(t, r); !reflect.DeepEqual(got, map[string]any{"name": "Monthly bordereau", "description": "Reports"}) {
					t.Errorf("create body = %#v", got)
				}
				testutil.RespondJSON(w, http.StatusCreated, created)
			})
			method, route, status := "POST", seriesTestPath+"/"+emptySeriesTestID+"/members", http.StatusCreated
			if moving {
				method, route, status = "PATCH", seriesTestPath+"/"+seriesTestID+"/members/42", http.StatusOK
			}
			mock.RegisterHandler(method, route, func(w http.ResponseWriter, r *http.Request) {
				body := decodeSeriesBody(t, r)
				want := map[string]any{"period_key": "Q1", "dataset_id": float64(42)}
				if moving {
					delete(want, "dataset_id")
					want["series_id"] = emptySeriesTestID
				}
				if !reflect.DeepEqual(body, want) {
					t.Errorf("membership body = %#v, want %#v", body, want)
				}
				testutil.RespondJSON(w, status, table_series.Member{SeriesID: created.ID, DatasetID: 42, PeriodKey: "Q1"})
			})
			mock.RegisterHandler("GET", seriesTestPath+"/"+emptySeriesTestID, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, created) })
			out, err := runRoot(t, "apply", "-f", path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "table-series/Monthly bordereau created") {
				t.Fatalf("output = %s", out)
			}
			if want := []string{"POST " + seriesTestPath, method + " " + route}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
				t.Errorf("writes = %v", seriesMutationRequests(mock))
			}
		})
	}
}

func TestApplyTableSeriesStoppedAfterWritesPrintsWhatItWrote(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
		added    int
	}{
		{name: "created series"},
		{name: "created series and member", added: 1},
		{name: "existing series and member", existing: true, added: 1},
	} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				mock, path := seriesWriteSetup(t, "  members:\n    - table: 42\n      period_key: Q1\n    - table: 43\n      period_key: Q2\n")
				series := table_series.Series{ID: uuid.MustParse(emptySeriesTestID), Name: "Monthly bordereau", Members: []table_series.Member{}}
				mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
					items := []table_series.Series{}
					if tc.existing {
						items = append(items, series)
					}
					testutil.RespondJSON(w, http.StatusOK, items)
				})
				mock.RegisterHandler("POST", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusCreated, series)
				})
				memberPath := seriesTestPath + "/" + emptySeriesTestID + "/members"
				mock.RegisterHandler("POST", memberPath, func(w http.ResponseWriter, r *http.Request) {
					body := decodeSeriesBody(t, r)
					if body["dataset_id"] == float64(42) && tc.added == 1 {
						member := table_series.Member{SeriesID: series.ID, DatasetID: 42, PeriodKey: "Q1"}
						series.Members = append(series.Members, member)
						testutil.RespondJSON(w, http.StatusCreated, member)
						return
					}
					testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"msg": "period is occupied"}})
				})
				mock.RegisterHandler("GET", seriesTestPath+"/"+emptySeriesTestID, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, series)
				})

				out, err := runRoot(t, "apply", "-f", path, "-o", format)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
				}
				completed, total := tc.added, 2
				writes := []string{}
				if !tc.existing {
					completed++
					total++
					writes = append(writes, "POST "+seriesTestPath)
				}
				for i := 0; i <= tc.added; i++ {
					writes = append(writes, "POST "+memberPath)
				}
				if !strings.Contains(err.Error(), fmt.Sprintf("request %d of %d (%d completed)", completed+1, total, completed)) {
					t.Errorf("missing partial progress: %v", err)
				}
				if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, writes) {
					t.Fatalf("writes = %v, want %v", got, writes)
				}
				want, marshalErr := json.Marshal(table_series.FromAPI(&series, nil))
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, string(want))
				} else {
					assertSeriesJSON(t, out, string(want))
				}
			})
		}
	}
}

func TestTableSeriesApplyReplacePeriodHolder(t *testing.T) {
	mock, path := seriesWriteSetup(t, "  description: null\n  members:\n    - table: 43\n      period_key: 2026-09\n")
	var patch map[string]any
	mock.RegisterHandler("PATCH", seriesTestPath+"/"+seriesTestID, func(w http.ResponseWriter, r *http.Request) {
		patch = decodeSeriesBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesTestRecord))
	})
	for _, id := range []string{"42", "9007199254740993"} {
		mock.RegisterHandler("DELETE", seriesTestPath+"/"+seriesTestID+"/members/"+id, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	mock.RegisterHandler("POST", seriesTestPath+"/"+seriesTestID+"/members", func(w http.ResponseWriter, r *http.Request) {
		want := map[string]any{"dataset_id": float64(43), "period_key": "2026-09"}
		if body := decodeSeriesBody(t, r); !reflect.DeepEqual(body, want) {
			t.Errorf("body = %#v", body)
		}
		testutil.RespondJSON(w, http.StatusCreated, table_series.Member{DatasetID: 43})
	})
	out, err := runRoot(t, "apply", "-f", path, "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(out)) || strings.Contains(out, "updated") {
		t.Errorf("stdout = %s", out)
	}
	if !reflect.DeepEqual(patch, map[string]any{"name": "Monthly bordereau", "description": nil}) {
		t.Errorf("series patch = %#v", patch)
	}
	want := []string{"PATCH " + seriesTestPath + "/" + seriesTestID, "DELETE " + seriesTestPath + "/" + seriesTestID + "/members/42", "POST " + seriesTestPath + "/" + seriesTestID + "/members", "DELETE " + seriesTestPath + "/" + seriesTestID + "/members/9007199254740993"}
	if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
}

func TestTableSeriesRemovalRequiresConfirmationBeforeAnyWrite(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmation", true: "protected context"}[protected], func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  description: Changed\n  members: []\n")
			args := []string{"apply", "-f", path}
			if protected {
				cfg, _ := config.Load()
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--yes")
			}
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v", err)
			}
			if len(seriesMutationRequests(mock)) != 0 {
				t.Fatalf("unexpected writes: %v", mock.Requests())
			}
		})
	}
}

func TestTableSeriesExportAppliesUnchangedAndDiffIsReadOnly(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		t.Run(verb, func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  members: []\n")
			out, err := runRoot(t, "get", "table-series", seriesTestID, "-o", "yaml")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(out), 0600); err != nil {
				t.Fatal(err)
			}
			out, err = runRoot(t, verb, "-f", path, "-o", "table")
			if err != nil {
				t.Fatal(err)
			}
			if verb == "apply" && !strings.Contains(out, "unchanged") || verb == "diff" && out != "" {
				t.Errorf("output = %s", out)
			}
			if len(seriesMutationRequests(mock)) != 0 {
				t.Fatalf("export caused writes: %v", mock.Requests())
			}
			if reads := mock.Requests(); reads[len(reads)-1] != "GET "+seriesSchedulePath {
				t.Errorf("the plan did not read the exported schedule: %v", reads)
			}
		})
	}
}

func TestTableSeriesDiffShowsRequestsAndErrorsUseTypedExitCodes(t *testing.T) {
	for _, verb := range []string{"diff", "apply"} {
		t.Run(verb, func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  members: []\n")
			mock.RegisterHandler("DELETE", seriesTestPath+"/"+seriesTestID+"/members/9007199254740993", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"msg": "non-cancelled run reads this table", "error_code": "DATASET_BOUND_TO_DELIVERABLE"}})
			})
			args := []string{verb, "-f", path}
			if verb == "apply" {
				args = append(args, "--yes")
			}
			out, err := runRoot(t, args...)
			if verb == "diff" {
				if qerrors.GetExitCode(err) != 1 || !strings.Contains(out, "DELETE "+"/table-series"+"/"+seriesTestID+"/members/42") {
					t.Fatalf("out = %s, error = %v", out, err)
				}
				if len(seriesMutationRequests(mock)) != 0 {
					t.Fatal("diff wrote resources")
				}
			} else {
				if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "non-cancelled run") {
					t.Fatalf("error = %v", err)
				}
				if len(seriesMutationRequests(mock)) != 1 {
					t.Fatalf("apply continued after failure: %v", mock.Requests())
				}
			}
		})
	}
}

func TestTableSeriesPlanTouchingALockedPeriodRefusesBeforeAnyWrite(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		t.Run(verb, func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  members:\n    - table: 42\n      period_key: 2026-10\n")
			registerSeriesList(t, mock, seriesTestRecord)
			args := []string{verb, "-f", path}
			if verb == "apply" {
				args = append(args, "--yes")
			}
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, error = %v", code, err)
			}
			if !strings.Contains(err.Error(), "qctl table-series unlock "+seriesTestID+" --period 2026-09") {
				t.Errorf("error = %v", err)
			}
			if out != "" || len(seriesMutationRequests(mock)) != 0 {
				t.Fatalf("out = %q, requests = %v", out, mock.Requests())
			}
		})
	}
}

func TestDeleteTableSeriesRequiresContextAndConfirmation(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, tc := range []struct {
		name                   string
		yes, protected         bool
		status, code, requests int
	}{
		{"success", true, false, http.StatusNoContent, 0, 1},
		{"members or bindings", true, false, http.StatusConflict, 2, 1},
		{"confirmation", false, false, http.StatusNoContent, 2, 0},
		{"protected", true, true, http.StatusNoContent, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, _ := seriesWriteSetup(t, "  members: []\n")
			if tc.protected {
				cfg, _ := config.Load()
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
			}
			mock.RegisterHandler("DELETE", seriesTestPath+"/"+seriesTestID, func(w http.ResponseWriter, _ *http.Request) {
				if tc.status == http.StatusNoContent {
					w.WriteHeader(tc.status)
				} else {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "Series still has members or bindings"}})
				}
			})
			args := []string{"delete", "table-series", "Monthly bordereau"}
			if tc.yes {
				args = append(args, "--yes")
			}
			out, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("out = %q, error = %v", out, err)
			}
			if len(seriesMutationRequests(mock)) != tc.requests {
				t.Fatalf("writes = %v", seriesMutationRequests(mock))
			}
			cmd, _, _ := GetRootCmd().Find([]string{"delete", "table-series"})
			if !cmdutil.IsDestructive(cmd) {
				t.Fatal("series delete is not destructive")
			}
		})
	}
}

func TestTableSeriesDeleteRefusalsNameBlockingSources(t *testing.T) {
	for _, member := range []bool{false, true} {
		t.Run(fmt.Sprintf("member=%t", member), func(t *testing.T) {
			mock, file := seriesWriteSetup(t, "  members:\n    - table: 9007199254740993\n      period_key: FY/Z\n")
			path, code, verb := seriesTestPath+"/"+seriesTestID, "SERIES_REFERENCED_BY_SOURCE", "delete"
			args := []string{"delete", "table-series", seriesTestID, "--yes"}
			if member {
				path += "/members/42"
				code, verb = "SERIES_SOURCE_FILES_IN_TABLE", "describe"
				args = []string{"apply", "-f", file, "--yes"}
			}
			mock.RegisterHandler("DELETE", path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
					"msg": "Server refused this change", "error_code": code,
					"context": map[string]any{"data_sources": []map[string]any{
						{"id": 12, "name": "Feed A"}, {"id": 9007199254740993, "name": "Feed B"},
					}},
				}})
			})
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != 2 {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
			for _, want := range []string{code, "Feed A", "Feed B", "qctl " + verb + " cloud-source 12", "qctl " + verb + " cloud-source 9007199254740993"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, []string{"DELETE " + path}) {
				t.Errorf("writes = %v", got)
			}
		})
	}
}

func TestTableSeriesApplyNotesMembersItRenamesThatAreNamedByName(t *testing.T) {
	// "September reports" moves to a new period, so the server renames it; "Legacy table" keeps its period and its name.
	mock, path := seriesWriteSetup(t, "  members:\n    - table: Legacy table\n      period_key: FY/Z\n"+
		"    - table: September reports\n      period_key: 2026-10\n")
	mock.RegisterHandler("GET", "/api/orgs/"+scopeTestOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		ids := map[string]json.Number{"Legacy table": "9007199254740993", "September reports": "42"}
		name := r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{{"id": ids[name], "name": name}}})
	})
	mock.RegisterHandler("PATCH", seriesTestPath+"/"+seriesTestID+"/members/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, table_series.Member{DatasetID: 42, PeriodKey: "2026-10"})
	})
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs([]string{"apply", "-f", path})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
	})
	if _, err := executeC(); err != nil {
		t.Fatal(err)
	}
	if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, []string{"PATCH " + seriesTestPath + "/" + seriesTestID + "/members/42"}) {
		t.Fatalf("writes = %v", got)
	}
	want := "Note: spec.members[1] names table \"September reports\" by name; this apply renames it after the series, " +
		"so the next apply of this file will not find it. Name it by table ID 42 instead.\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
	if !strings.Contains(out.String(), "table-series/Monthly bordereau updated") || strings.Contains(out.String(), "Note:") {
		t.Errorf("stdout = %q", out.String())
	}
}
