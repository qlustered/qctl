package root

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

func resetDeliverableLifecycleFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, path := range [][]string{{"delete", "deliverable"}, {"undelete", "deliverable"}, {"delete", "deliverable-revision"}} {
			cmd, _, err := GetRootCmd().Find(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"yes", "release"} {
				if f := cmd.Flags().Lookup(name); f != nil {
					if err := f.Value.Set(f.DefValue); err != nil {
						t.Fatal(err)
					}
					f.Changed = false
				}
			}
		}
	}
	reset()
	t.Cleanup(reset)
}

func deliverableLifecycleServer(t *testing.T) *deliverableTestServer {
	t.Helper()
	resetDeliverableLifecycleFlags(t)
	s := revisionTestServer(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	allow := false
	cfg.Contexts[cfg.CurrentContext].Protected = &allow
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return s
}

func deliverableLifecycleMutation(t *testing.T, s *deliverableTestServer, method, path, body string) {
	t.Helper()
	s.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if body == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
}

func TestDeliverableTrashAndRestoreFormats(t *testing.T) {
	for _, verb := range []string{"delete", "undelete"} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				s := deliverableLifecycleServer(t)
				id, ref, action := liveDeliverableID, "monthly-bordereau", "archive"
				body := strings.Replace(liveDeliverableRecord, `"archived_at":null`, `"archived_at":"2026-10-08T12:00:00Z"`, 1)
				if verb == "undelete" {
					id, ref, action = trashedDeliverableID, "weekly-claims", "recover"
					body = strings.Replace(trashedDeliverableRecord, `"archived_at":"2026-10-02T12:00:00Z"`, `"archived_at":null`, 1)
				}
				path := deliverableTestPath + "/" + id + "/" + action
				deliverableLifecycleMutation(t, s, "POST", path, body)
				out, err := runRoot(t, verb, "deliverable", ref, "--yes", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{deliverableRequest(), deliverableRequest(id), "POST " + path}
				if got := s.Requests(); !reflect.DeepEqual(got, want) {
					t.Fatalf("requests = %v, want %v", got, want)
				}
				if got := s.listQueries[0].Get("include_archived"); (got == "true") != (verb == "undelete") {
					t.Errorf("include_archived = %q for %s", got, verb)
				}
				switch format {
				case "json":
					assertSeriesJSON(t, out, body)
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, body)
				case "name":
					if out != "deliverable/"+id+"\n" {
						t.Errorf("out = %q", out)
					}
				default:
					if verb == "delete" && (!strings.Contains(out, "moved to trash") || !strings.Contains(out, "qctl undelete deliverable "+id)) {
						t.Errorf("out = %q", out)
					}
					if verb == "undelete" && !strings.Contains(out, "restored from trash") {
						t.Errorf("out = %q", out)
					}
				}
			})
		}
	}
}

func TestDeliverableLifecycleRequiresContextAndConfirmation(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, args := range [][]string{
		{"delete", "deliverable", liveDeliverableID},
		{"undelete", "deliverable", trashedDeliverableID},
		{"delete", "deliverable-revision", liveDeliverableID, "--release", "2026.2"},
	} {
		for _, gate := range []string{"confirmation", "protected", "named context"} {
			t.Run(strings.Join(args[:2], " ")+"/"+gate, func(t *testing.T) {
				s := deliverableLifecycleServer(t)
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				cmd, _, _ := GetRootCmd().Find(args[:2])
				if !cmdutil.IsDestructive(cmd) {
					t.Fatal("lifecycle command is not protected")
				}
				argv := append([]string{}, args...)
				if gate != "confirmation" {
					cfg.Contexts[cfg.CurrentContext].Protected = nil
					if err := cfg.Save(); err != nil {
						t.Fatal(err)
					}
					argv = append(argv, "--yes")
				}
				path := deliverableTestPath + "/" + liveDeliverableID + "/archive"
				method, body := "POST", liveDeliverableRecord
				if args[0] == "undelete" {
					path = deliverableTestPath + "/" + trashedDeliverableID + "/recover"
				} else if args[1] == "deliverable-revision" {
					method, path, body = "DELETE", revisionTestPath+draftRevisionID, ""
				}
				deliverableLifecycleMutation(t, s, method, path, body)
				code, writes := qerrors.ExitBadUsage, 0
				if gate == "named context" {
					argv = append(argv, "--context", cfg.CurrentContext)
					code, writes = 0, 1
				}
				out, err := runRootWithContextFlag(t, argv...)
				if qerrors.GetExitCode(err) != code {
					t.Fatalf("out = %q, error = %v", out, err)
				}
				if got := len(seriesMutationRequests(s.MockAPIServer)); got != writes {
					t.Fatalf("writes = %v", s.Requests())
				}
				if gate == "protected" && len(s.Requests()) != 0 {
					t.Fatalf("protected context reached API: %v", s.Requests())
				}
			})
		}
	}
}

func TestDeliverableLifecycleRejectsWrongTrashState(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "deliverable", trashedDeliverableID},
		{"delete", "deliverable", "deliverable/" + trashedDeliverableID},
		{"undelete", "deliverable", liveDeliverableID},
		{"undelete", "deliverable", "monthly-bordereau"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := deliverableLifecycleServer(t)
			out, err := runRoot(t, append(args, "--yes")...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" {
				t.Fatalf("out = %q, error = %v", out, err)
			}
			if len(seriesMutationRequests(s.MockAPIServer)) != 0 {
				t.Fatalf("unexpected writes: %v", s.Requests())
			}
		})
	}
}

func TestDeleteDeliverableRevisionSelectsADraftWithinItsDeliverable(t *testing.T) {
	for _, tc := range []struct {
		release, state, parent string
		code                   int
	}{
		{"2026.2", "draft", liveDeliverableID, 0},
		{draftRevisionID, "draft", liveDeliverableID, 0},
		{"2026.1", "enabled", liveDeliverableID, 2},
		{"2026.2", "disabled", liveDeliverableID, 2},
		{"2026.2", "enabled", liveDeliverableID, 2},
		{"2026.2", "draft", trashedDeliverableID, 3},
		{seriesTestID, "draft", liveDeliverableID, 3},
		{"missing", "draft", liveDeliverableID, 3},
	} {
		t.Run(tc.release+"/"+tc.state+"/"+tc.parent, func(t *testing.T) {
			s := deliverableLifecycleServer(t)
			body := strings.ReplaceAll(revisionTreeRecord, enabledRevisionID, draftRevisionID)
			body = strings.ReplaceAll(body, `"state":"enabled"`, `"state":"`+tc.state+`"`)
			body = strings.ReplaceAll(body, `"release":"2026.1"`, `"release":"2026.2"`)
			body = strings.ReplaceAll(body, liveDeliverableID, tc.parent)
			s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
			})
			deliverableLifecycleMutation(t, s, "DELETE", revisionTestPath+draftRevisionID, "")
			out, err := runRoot(t, "delete", "deliverable-revision", liveDeliverableID, "--release", tc.release, "--yes")
			if qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("out = %q, error = %v", out, err)
			}
			writes := seriesMutationRequests(s.MockAPIServer)
			if tc.code == 0 {
				if !reflect.DeepEqual(writes, []string{"DELETE " + revisionTestPath + draftRevisionID}) || !strings.Contains(out, "deleted (release \"2026.2\")") {
					t.Fatalf("out = %q, writes = %v", out, writes)
				}
			} else if len(writes) != 0 || out != "" {
				t.Fatalf("out = %q, unexpected writes = %v", out, writes)
			}
		})
	}
}

func TestDeliverableLifecycleHTTPFailuresKeepExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status, code int
	}{
		{http.StatusConflict, 2}, {http.StatusUnprocessableEntity, 2},
		{http.StatusForbidden, 4}, {http.StatusNotFound, 3}, {http.StatusServiceUnavailable, 1},
	} {
		for _, command := range []struct {
			args         []string
			method, path string
		}{
			{[]string{"delete", "deliverable", liveDeliverableID}, "POST", deliverableTestPath + "/" + liveDeliverableID + "/archive"},
			{[]string{"undelete", "deliverable", trashedDeliverableID}, "POST", deliverableTestPath + "/" + trashedDeliverableID + "/recover"},
			{[]string{"delete", "deliverable-revision", liveDeliverableID, "--release", "2026.2"}, "DELETE", revisionTestPath + draftRevisionID},
		} {
			t.Run(strings.Join(command.args[:2], " ")+"/"+http.StatusText(tc.status), func(t *testing.T) {
				s := deliverableLifecycleServer(t)
				s.RegisterHandler(command.method, command.path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{
						"msg": "lifecycle refused", "error_code": "REVISION_NOT_DRAFT", "context": map[string]any{},
					}})
				})
				out, err := runRoot(t, append(command.args, "--yes")...)
				if qerrors.GetExitCode(err) != tc.code || out != "" || !strings.Contains(err.Error(), "lifecycle refused") {
					t.Fatalf("out = %q, error = %v", out, err)
				}
				if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{command.method + " " + command.path}) {
					t.Fatalf("writes = %v", got)
				}
			})
		}
	}
}

func TestDeleteDeliverableRevisionWorksInTheTrash(t *testing.T) {
	s := deliverableLifecycleServer(t)
	s.RegisterHandler("GET", deliverableTestPath+"/"+trashedDeliverableID+"/revisions", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(liveDeliverableRevisions))
	})
	body := strings.ReplaceAll(revisionTreeRecord, enabledRevisionID, draftRevisionID)
	body = strings.ReplaceAll(body, `"state":"enabled"`, `"state":"draft"`)
	body = strings.ReplaceAll(body, liveDeliverableID, trashedDeliverableID)
	s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	deliverableLifecycleMutation(t, s, "DELETE", revisionTestPath+draftRevisionID, "")
	_, err := runRoot(t, "delete", "deliverable-revision", "weekly-claims", "--release", "2026.2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if s.listQueries[0].Get("include_archived") != "true" {
		t.Errorf("listing excluded the trash: %v", s.listQueries)
	}
	if got := seriesMutationRequests(s.MockAPIServer); !reflect.DeepEqual(got, []string{"DELETE " + revisionTestPath + draftRevisionID}) {
		t.Fatalf("writes = %v", got)
	}
}

func TestDeliverableLifecycleStopsAtFailedReads(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"delete", "deliverable", "monthly-bordereau"}, deliverableTestPath},
		{[]string{"delete", "deliverable", liveDeliverableID}, deliverableTestPath + "/" + liveDeliverableID},
		{[]string{"undelete", "deliverable", trashedDeliverableID}, deliverableTestPath + "/" + trashedDeliverableID},
		{[]string{"delete", "deliverable-revision", liveDeliverableID, "--release", "2026.2"}, deliverableTestPath + "/" + liveDeliverableID + "/revisions"},
		{[]string{"delete", "deliverable-revision", liveDeliverableID, "--release", "2026.2"}, revisionTestPath + draftRevisionID},
	} {
		t.Run(strings.Join(tc.args[:2], " ")+"/"+tc.path, func(t *testing.T) {
			s := deliverableLifecycleServer(t)
			s.RegisterHandler("GET", tc.path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "permission denied"}})
			})
			out, err := runRoot(t, append(tc.args, "--yes")...)
			if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized || out != "" {
				t.Fatalf("out = %q, error = %v", out, err)
			}
			requests := s.Requests()
			if len(seriesMutationRequests(s.MockAPIServer)) != 0 || requests[len(requests)-1] != "GET "+tc.path {
				t.Fatalf("continued after failed read: %v", requests)
			}
		})
	}
}

func TestDeliverableTrashConflictWithoutErrorCode(t *testing.T) {
	s := deliverableLifecycleServer(t)
	s.RegisterHandler("POST", deliverableTestPath+"/"+liveDeliverableID+"/archive", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
			"msg": "Deliverable has 1 run(s) not in a terminal state (delivered/cancelled). Cancel or deliver them before archiving.",
		}})
	})
	out, err := runRoot(t, "delete", "deliverable", liveDeliverableID, "--yes")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" || !strings.Contains(err.Error(), "Cancel or deliver them") {
		t.Fatalf("out = %q, error = %v", out, err)
	}
}

func TestDeliverableLifecycleArgumentsFailBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "deliverable"}, {"delete", "deliverable", " "},
		{"undelete", "deliverable", ""}, {"undelete", "deliverable", "table/42"},
		{"delete", "deliverable-revision", liveDeliverableID},
		{"delete", "deliverable-revision", liveDeliverableID, "--release", ""},
		{"delete", "deliverable-revision", liveDeliverableID, "--release", " "},
		{"delete", "deliverable-revision", "", "--release", "2026.2"},
		{"delete", "deliverable-revision", liveDeliverableID, "--release", "2026.2", "-o", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetDeliverableLifecycleFlags(t)
			err := runOutsideGetWithoutContext(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
