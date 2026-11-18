package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

func resetSeriesLockFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, verb := range []string{"lock", "unlock"} {
			cmd, _, err := GetRootCmd().Find([]string{"table-series", verb})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"period", "yes"} {
				flag := cmd.Flags().Lookup(name)
				if err := flag.Value.Set(flag.DefValue); err != nil {
					t.Fatal(err)
				}
				flag.Changed = false
			}
		}
	}
	reset()
	t.Cleanup(reset)
}

func seriesLockMember(t *testing.T, verb string) table_series.Member {
	t.Helper()
	var series table_series.Series
	if err := json.Unmarshal([]byte(seriesTestRecord), &series); err != nil {
		t.Fatal(err)
	}
	if verb == "unlock" {
		return series.Members[1]
	}
	return series.Members[0]
}

func TestTableSeriesLockFormatsAndExactTarget(t *testing.T) {
	for _, verb := range []string{"lock", "unlock"} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				mock := tableSeriesTestServer(t)
				resetSeriesLockFlags(t)
				member := seriesLockMember(t, verb)
				member.IsLocked = verb == "lock"
				if member.IsLocked {
					member.LockedAt = seriesLockMember(t, "unlock").LockedAt
				} else {
					member.LockedAt, member.LockedBy = nil, nil
				}
				path := fmt.Sprintf("%s/%s/members/%d", seriesTestPath, seriesTestID, member.DatasetID)
				mock.RegisterHandler("PATCH", path, func(w http.ResponseWriter, r *http.Request) {
					want := map[string]any{"is_locked": verb == "lock"}
					if got := decodeSeriesBody(t, r); !reflect.DeepEqual(got, want) {
						t.Errorf("body = %#v, want %#v", got, want)
					}
					if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
						t.Errorf("Authorization = %q", got)
					}
					testutil.RespondJSON(w, http.StatusOK, member)
				})
				ref := "Monthly bordereau"
				wantRequests := []string{"GET " + seriesTestPath, "GET " + seriesTestPath + "/" + seriesTestID}
				if format != "table" {
					ref, wantRequests = seriesTestID, wantRequests[1:]
					if format == "name" {
						ref = "table-series/" + seriesTestID
					}
				}
				out, err := runRoot(t, "table-series", verb, ref, "--period", member.PeriodKey, "--yes", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				wantRequests = append(wantRequests, "PATCH "+path)
				if got := mock.Requests(); !reflect.DeepEqual(got, wantRequests) {
					t.Errorf("requests = %v, want %v", got, wantRequests)
				}
				name := fmt.Sprintf("table/%d", member.DatasetID)
				switch format {
				case "table":
					if !strings.Contains(out, name) || !strings.Contains(out, verb+"ed") || !strings.Contains(out, member.PeriodKey) {
						t.Errorf("output = %q", out)
					}
				case "name":
					if out != name+"\n" {
						t.Errorf("output = %q", out)
					}
				default:
					want, err := json.Marshal(member)
					if err != nil {
						t.Fatal(err)
					}
					if format == "yaml" {
						testutil.AssertYAMLReadsAsJSON(t, out, string(want))
					} else {
						assertSeriesJSON(t, out, string(want))
					}
				}
			})
		}
	}
}

func TestTableSeriesLockAlreadyRequestedState(t *testing.T) {
	for _, verb := range []string{"lock", "unlock"} {
		t.Run(verb, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			resetSeriesLockFlags(t)
			period := "FY/Z"
			if verb == "lock" {
				period = "2026-09"
			}
			out, err := runRoot(t, "table-series", verb, seriesTestID, "--period", period)
			if err != nil || !strings.Contains(out, "already "+verb+"ed") {
				t.Fatalf("output = %q, error = %v", out, err)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + seriesTestPath + "/" + seriesTestID}) {
				t.Errorf("requests = %v", got)
			}
		})
	}
}

func TestTableSeriesLockNeedsConfirmation(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-interactive stdin")
	}
	for _, verb := range []string{"lock", "unlock"} {
		t.Run(verb, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			resetSeriesLockFlags(t)
			member := seriesLockMember(t, verb)
			out, err := runRoot(t, "table-series", verb, seriesTestID, "--period", member.PeriodKey)
			if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "--yes") || out != "" {
				t.Fatalf("output = %q, error = %v", out, err)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Errorf("writes = %v", writes)
			}
		})
	}
}

func TestTableSeriesLockMissingExactPeriod(t *testing.T) {
	mock := tableSeriesTestServer(t)
	resetSeriesLockFlags(t)
	out, err := runRoot(t, "table-series", "lock", seriesTestID, "--period", "fy/z", "--yes")
	if qerrors.GetExitCode(err) != 3 || !strings.Contains(err.Error(), "describe table-series") || !strings.Contains(err.Error(), "Scoped to organization") || out != "" {
		t.Fatalf("output = %q, error = %v", out, err)
	}
	if len(seriesMutationRequests(mock)) != 0 {
		t.Errorf("requests = %v", mock.Requests())
	}
}

func TestTableSeriesLockArgumentsBeforeBootstrap(t *testing.T) {
	for _, verb := range []string{"lock", "unlock"} {
		for _, args := range [][]string{
			{}, {seriesTestID}, {" ", "--period", "Q1"}, {seriesTestID, "--period", ""},
			{seriesTestID, "--period", " Q1"}, {seriesTestID, "--period", "Q1\n"},
			{seriesTestID, "--period", strings.Repeat("x", 201)},
			{"table/42", "--period", "Q1"}, {seriesTestID, "extra", "--period", "Q1"},
		} {
			t.Run(verb+"/"+strings.Join(args, " "), func(t *testing.T) {
				resetSeriesLockFlags(t)
				err := runOutsideGetWithoutContext(t, append([]string{"table-series", verb}, args...)...)
				if qerrors.GetExitCode(err) != 2 {
					t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
				}
			})
		}
	}
}

func TestTableSeriesLockRefusalsKeepExitCode(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
		exit   int
		hint   string
	}{
		{409, "PERIOD_HAS_ACTIVE_JOBS", 2, "qctl get ingestion-jobs --table-id"},
		{409, "ERR_DB_CONCURRENCY_CONFLICT", 2, "retry after the other change finishes"},
		{403, "", 4, ""}, {404, "", 3, ""}, {422, "", 2, ""}, {500, "", 1, ""},
	} {
		for _, verb := range []string{"lock", "unlock"} {
			t.Run(fmt.Sprintf("%s/%d/%s", verb, tt.status, tt.code), func(t *testing.T) {
				mock := tableSeriesTestServer(t)
				resetSeriesLockFlags(t)
				member := seriesLockMember(t, verb)
				path := fmt.Sprintf("%s/%s/members/%d", seriesTestPath, seriesTestID, member.DatasetID)
				mock.RegisterHandler("PATCH", path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tt.status, map[string]any{"detail": map[string]any{
						"msg": "server refusal", "error_code": tt.code,
					}})
				})
				out, err := runRoot(t, "table-series", verb, seriesTestID, "--period", member.PeriodKey, "--yes")
				if qerrors.GetExitCode(err) != tt.exit || !strings.Contains(err.Error(), "server refusal") || !strings.Contains(err.Error(), tt.hint) || out != "" {
					t.Fatalf("exit = %d, output = %q, error = %v", qerrors.GetExitCode(err), out, err)
				}
				if tt.code == "PERIOD_HAS_ACTIVE_JOBS" {
					if strings.Count(err.Error(), "Hint:") != 1 || !strings.Contains(err.Error(), "Check: `qctl get table ") {
						t.Errorf("active-job advice is missing or repeated: %v", err)
					}
				}
				if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + seriesTestPath + "/" + seriesTestID, "PATCH " + path}) {
					t.Errorf("requests = %v", got)
				}
			})
		}
	}
}
