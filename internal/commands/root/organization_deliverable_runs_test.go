package root

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

var organizationLiveRun = strings.TrimSuffix(strings.TrimSpace(liveDeliverableRun), "}") +
	`,"deliverable_name":"Monthly bordereau renamed"}`

var organizationArchivedRun = strings.NewReplacer(
	liveDeliverableID, trashedDeliverableID,
	`"id":40`, `"id":39`,
).Replace(strings.TrimSuffix(strings.TrimSpace(olderRunRecord), "}") + `,"deliverable_name":"Weekly claims"}`)

func (s *runTestServer) answerOrganizationRuns(t *testing.T, status int, body string) {
	t.Helper()
	s.RegisterHandler("GET", strings.TrimSuffix(runTestPath, "/"), func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.runQueries = append(s.runQueries, r.URL.RawQuery)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
}

func runDeliverableRuns(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"get", "deliverable-runs"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestGetOrganizationDeliverableRunsQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		query string
	}{
		{"defaults", nil, "limit=20&page=1"},
		{"every flag", []string{"--state", "signed_off", "--reverse", "--page", "2", "--limit", "5"}, "limit=5&page=2&reverse=false&state=signed_off"},
		{"not reversed", []string{"--state", "stale", "--reverse=false"}, "limit=20&page=1&state=stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			s.answerOrganizationRuns(t, http.StatusOK, `{"results":[],"total_rows":0,"page":1,"next":null,"previous":null}`)
			if _, err := runRoot(t, append([]string{"get", "deliverable-runs", "-o", "json"}, tc.flags...)...); err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{"GET " + strings.TrimSuffix(runTestPath, "/")}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if got := strings.Join(s.runQueries, " "); got != tc.query {
				t.Errorf("query = %q, want %q", got, tc.query)
			}
		})
	}
}

func TestGetOrganizationDeliverableRunsFormats(t *testing.T) {
	items := `[` + organizationLiveRun + `,` + organizationArchivedRun + `]`
	wantList := `{"kind":"List","items":` + items + `,"page":1,"limit":20,"total":2,"next_page":null}`
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			s.answerOrganizationRuns(t, http.StatusOK, `{"results":`+items+`,"total_rows":2,"page":1,"next":null,"previous":null}`)
			out, err := runRoot(t, "get", "deliverable-runs", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, wantList)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, wantList)
			case "name":
				if want := "deliverable-run/41\ndeliverable-run/39\n"; out != want {
					t.Errorf("out = %q, want %q", out, want)
				}
			default:
				for _, want := range []string{"DELIVERABLE", "Monthly bordereau renamed", "Weekly claims", "rendered", "delivered", liveDeliverableRunParams} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				if strings.Index(out, "Monthly bordereau renamed") > strings.Index(out, "Weekly claims") {
					t.Errorf("runs out of server order: %s", out)
				}
			}
		})
	}
}

func TestGetOrganizationDeliverableRunsColumns(t *testing.T) {
	s := newRunTestServer(t, describedRunRecord)
	s.answerOrganizationRuns(t, http.StatusOK, `{"results":[`+organizationArchivedRun+`],"total_rows":1,"page":1}`)
	out, err := runRoot(t, "get", "deliverable-runs", "--columns", "deliverable,deliverable_id,id", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(out), []string{"Weekly", "claims", trashedDeliverableID, "39"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

func TestGetDeliverableRunsPagingBothScopes(t *testing.T) {
	for _, ref := range []string{"", liveDeliverableID} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			for _, page := range []string{"1", "2", "99"} {
				t.Run(ref+"/"+format+"/"+page, func(t *testing.T) {
					s := newRunTestServer(t, describedRunRecord)
					items, path := `[`+organizationLiveRun+`]`, strings.TrimSuffix(runTestPath, "/")
					args := []string{"-o", format, "--page", page, "--limit", "1"}
					if ref != "" {
						items, path = `[`+liveDeliverableRun+`]`, deliverableTestPath+"/"+ref+"/runs"
						args = append(args, ref)
					}
					if page == "99" {
						items = `[]`
					}
					// The total drives paging even without a server next link.
					s.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
						checkDeliverableToken(t, r)
						testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":`+items+`,"total_rows":2,"page":`+page+`}`))
					})
					out, stderr, err := runDeliverableRuns(t, args...)
					if err != nil {
						t.Fatal(err)
					}
					if format == "json" || format == "yaml" {
						next := "null"
						if page == "1" {
							next = "2"
						}
						want := `{"kind":"List","items":` + items + `,"page":` + page + `,"limit":1,"total":2,"next_page":` + next + `}`
						if format == "json" {
							assertSeriesJSON(t, out, want)
						} else {
							testutil.AssertYAMLReadsAsJSON(t, out, want)
						}
						if strings.Contains(stderr, "More available") {
							t.Errorf("machine output has paging hint: %s", stderr)
						}
					} else if got := strings.Contains(stderr, "More available: use --page 2"); got != (page == "1") {
						t.Errorf("page %s paging hint = %t: %s", page, got, stderr)
					}
				})
			}
		}
	}
}

func TestGetOrganizationDeliverableRunsEmpty(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			s.answerOrganizationRuns(t, http.StatusOK, `{"results":[],"total_rows":0,"page":1,"next":null,"previous":null}`)
			out, stderr, err := runDeliverableRuns(t, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			empty := `{"kind":"List","items":[],"page":1,"limit":20,"total":0,"next_page":null}`
			switch format {
			case "json":
				assertSeriesJSON(t, out, empty)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, empty)
			default:
				if out != "" || (format == "table" && !strings.Contains(stderr, "No deliverable runs")) {
					t.Errorf("out = %q, stderr = %q", out, stderr)
				}
			}
		})
	}
}

func TestGetOrganizationDeliverableRunsPageLinks(t *testing.T) {
	s := newRunTestServer(t, describedRunRecord)
	s.answerOrganizationRuns(t, http.StatusOK, `{"results":[`+organizationLiveRun+`],"total_rows":null,"page":2,"next":{"page":3},"previous":{"page":1}}`)
	out, err := runRoot(t, "get", "deliverable-runs", "--page", "2", "--limit", "1", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	assertSeriesJSON(t, out, `{"kind":"List","items":[`+organizationLiveRun+`],"page":2,"limit":1,"total":null,"next_page":3}`)
}

func TestGetOrganizationDeliverableRunsErrors(t *testing.T) {
	for _, tc := range []struct{ status, code int }{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			s.answerOrganizationRuns(t, tc.status, `{"detail":{"msg":"Cannot list runs."}}`)
			out, err := runRoot(t, "get", "deliverable-runs")
			if code := qerrors.GetExitCode(err); code != tc.code || out != "" {
				t.Errorf("out = %q, exit = %d, want %d; %v", out, code, tc.code, err)
			}
		})
	}
}

func TestGetOrganizationDeliverableRunsUsageBeforeBootstrap(t *testing.T) {
	for _, flags := range [][]string{
		{"--state", "bogus"}, {"--state", ""}, {"--page", "0"}, {"--limit", "0"}, {"--limit", "-1"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, append([]string{"get", "deliverable-runs"}, flags...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2; %v", code, err)
			}
		})
	}
}
