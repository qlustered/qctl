package table_rule //nolint:revive // Matches the command directory.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestRecheckPreviewAndAction(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		for _, dry := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", format, dry), func(t *testing.T) {
				e := newEnv(t, 42)
				path := "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules/" + ruleID("email_check").String() + "/recheck-clean-rows"
				posts := 0
				e.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, 200, map[string]any{"count": 7, "capped": true, "predicate_errors": 2})
				})
				e.mock.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
					posts++
					testutil.RespondJSON(w, 200, map[string]any{"result": "ok", "msg": "done", "moved": 9, "already_in_quarantine": 0, "not_found": 0, "comment_thread_id": nil, "notified_user_count": nil})
				})
				cmd := setupTestCommand()
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				args := []string{"table-rule", "recheck-clean-rows", "email_check", "--table-id", "42", "-o", format}
				if dry {
					args = append(args, "--dry-run")
				} else {
					args = append(args, "--yes")
				}
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				want := 1
				if dry {
					want = 0
				}
				if posts != want {
					t.Fatalf("POSTs=%d", posts)
				}
				human := out.String()
				if format != "table" {
					human = stderr.String()
				}
				if !strings.Contains(human, "at least 7") || !strings.Contains(human, "Predicate failures") {
					t.Fatalf("preview=%s", human)
				}
				if format == "json" {
					var doc map[string]any
					if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
						t.Fatalf("stdout: %s: %v", out.String(), err)
					}
					key := "moved"
					if dry {
						key = "count"
					}
					if _, ok := doc[key]; !ok {
						t.Fatalf("result=%v", doc)
					}
				}
				if format == "yaml" {
					expected := `{"result":"ok","msg":"done","moved":9,"already_in_quarantine":0,"not_found":0,"comment_thread_id":null,"notified_user_count":null}`
					if dry {
						expected = `{"count":7,"capped":true,"predicate_errors":2}`
					}
					testutil.AssertYAMLReadsAsJSON(t, out.String(), expected)
				}
			})
		}
	}
}

func TestRecheckFailurePreservesExitCode(t *testing.T) {
	for _, status := range []int{403, 409, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t, 42)
			path := "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules/" + ruleID("email_check").String() + "/recheck-clean-rows"
			e.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": "cannot recheck"}})
			})
			cmd := setupTestCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"table-rule", "recheck-clean-rows", "email_check", "--table-id", "42", "--yes"})
			want := qerrors.ExitBadUsage
			if status == 403 {
				want = qerrors.ExitUnauthorized
			}
			if code := qerrors.GetExitCode(cmd.Execute()); code != want {
				t.Fatalf("exit=%d want %d", code, want)
			}
		})
	}
}

func TestRecheckNeedsConfirmationOffTTY(t *testing.T) {
	e := newEnv(t, 42)
	cmd := setupTestCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"table-rule", "recheck-clean-rows", "email_check", "--table-id", "42"})
	path := "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules/" + ruleID("email_check").String() + "/recheck-clean-rows"
	e.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, 200, map[string]any{"count": 1, "capped": false, "predicate_errors": 0})
	})
	// An unregistered POST would fail; the usage error proves no mutation ran.
	if code := qerrors.GetExitCode(cmd.Execute()); code != qerrors.ExitBadUsage {
		t.Fatalf("exit=%d", code)
	}
}
