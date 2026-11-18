package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const memberUserID = "11111111-2222-3333-4444-555555555555"
const memberPath = "/api/orgs/" + scopeTestOrgID + "/datasets/12/users"
const memberJSON = `{"user":{"id":"11111111-2222-3333-4444-555555555555","email":"alice@example.com","first_name":"Alice","last_name":"A"},"role":"editor","permission_level":"owner","effective_permission_level":"editor"}`

func tableMemberEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", scopeTestOrgID)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpoint, scopeTestOrgID, "member-token")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	allow := false
	cfg.Contexts[cfg.CurrentContext].Protected = &allow
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, path := range [][]string{{"get", "table-members"}, {"table-member", "set-permission"}, {"table-member", "remove"}, {"table-member", "leave"}} {
			cmd, _, err := GetRootCmd().Find(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"table", "table-id", "permission", "yes", "columns", "no-headers", "max-column-width"} {
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
	mock.RegisterHandler("GET", memberPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer member-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		testutil.RespondJSON(w, http.StatusOK, []json.RawMessage{json.RawMessage(memberJSON)})
	})
	return mock
}

func TestTableMemberListFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := tableMemberEnv(t)
			out, err := runRoot(t, "get", "table-members", "table/12", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + memberPath}) {
				t.Fatalf("requests = %v", got)
			}
			if format == "table" {
				for _, want := range []string{memberUserID, "alice@example.com", "owner", "editor"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q in %s", want, out)
					}
				}
			} else {
				want := `{"kind":"List","items":[` + memberJSON + `],"page":null,"limit":null,"total":null,"next_page":null}`
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				} else {
					assertSeriesJSON(t, out, want)
				}
			}
		})
	}
}

func TestTableMemberChangeSendsLevelAndPreservesEffectiveLevel(t *testing.T) {
	for _, level := range []string{"owner", "editor", "read_only"} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(level+"/"+format, func(t *testing.T) {
				mock := tableMemberEnv(t)
				mock.RegisterHandler("PATCH", memberPath+"/"+memberUserID, func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, map[string]any{"permission_level": level}) {
						t.Errorf("body = %v", body)
					}
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(memberJSON))
				})
				out, err := runRoot(t, "table-member", "set-permission", memberUserID, "--table-id", "12", "--permission", level, "--yes", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if got := mock.Requests(); !reflect.DeepEqual(got, []string{"PATCH " + memberPath + "/" + memberUserID}) {
					t.Fatalf("requests = %v", got)
				}
				switch format {
				case "json":
					assertSeriesJSON(t, out, memberJSON)
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, memberJSON)
				default:
					if !strings.Contains(out, "grant set to owner (effective editor)") {
						t.Error(out)
					}
				}
			})
		}
	}
}

func TestTableMemberRemoveAndLeave(t *testing.T) {
	for _, verb := range []string{"remove", "leave"} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				mock := tableMemberEnv(t)
				mock.RegisterHandler("GET", "/api/orgs/"+scopeTestOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": memberUserID, "email": "alice@example.com", "role": "member"})
				})
				mock.RegisterHandler("DELETE", memberPath+"/"+memberUserID, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
				args := []string{"table-member", verb, memberUserID, "--table-id", "12"}
				wantRequests := []string{"DELETE " + memberPath + "/" + memberUserID}
				if verb == "leave" {
					args = []string{"table-member", verb, "12"}
					wantRequests = append([]string{"GET /api/orgs/" + scopeTestOrgID + "/users/me"}, wantRequests...)
				}
				out, err := runRoot(t, append(args, "--yes", "-o", format)...)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(mock.Requests(), wantRequests) {
					t.Fatalf("requests = %v", mock.Requests())
				}
				want := `{"result":"removed","dataset_id":12,"user_id":"` + memberUserID + `"}`
				switch format {
				case "json":
					assertSeriesJSON(t, out, want)
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				default:
					if !strings.Contains(out, "grant removed") {
						t.Error(out)
					}
				}
			})
		}
	}
}

func TestTableMemberRefusalsKeepHTTPExitCodes(t *testing.T) {
	for _, verb := range []string{"set-permission", "remove"} {
		for _, tc := range []struct {
			status, exit int
			body         string
		}{
			{400, 2, `{"detail":"External users get access through their cloud sources, not through table sharing."}`},
			{403, 4, `{"detail":"You are not authorized to change or remove access to this table."}`},
			{404, 3, `{"detail":"User has no access to this table."}`},
			{409, 2, `{"detail":"A table needs at least one owner. Make someone else an owner first."}`},
			{422, 2, `{"detail":{"msg":"Invalid grant","error_code":"INVALID_GRANT","context":{}}}`},
		} {
			t.Run(verb+http.StatusText(tc.status), func(t *testing.T) {
				mock := tableMemberEnv(t)
				method := "DELETE"
				args := []string{"table-member", verb, memberUserID, "--table-id", "12", "--yes", "-o", "json"}
				if verb == "set-permission" {
					method = "PATCH"
					args = append(args, "--permission", "editor")
				}
				mock.RegisterHandler(method, memberPath+"/"+memberUserID, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
				})
				out, err := runRoot(t, args...)
				if got := qerrors.GetExitCode(err); got != tc.exit {
					t.Fatalf("exit %d: %v", got, err)
				}
				if out != "" {
					t.Errorf("false success output: %s", out)
				}
				if tc.status == 422 && !strings.Contains(err.Error(), "Invalid grant") {
					t.Error(err)
				}
			})
		}
	}
}

func TestTableMemberValidationMakesNoRequests(t *testing.T) {
	for _, args := range [][]string{
		{"get", "table-members"},
		{"get", "table-members", "12", "--table-id", "13"},
		{"table-member", "remove", "bad-id", "--table-id", "12", "--yes"},
		{"table-member", "set-permission", memberUserID, "--table-id", "12", "--permission", "bad", "--yes"},
		{"table-member", "set-permission", memberUserID, "--table-id", "12", "--yes"},
		{"table-member", "remove", memberUserID, "--table-id", "0", "--yes"},
		{"table-member", "leave", "12", "--table", "orders", "--yes"},
		{"table-member", "leave", "12", "--yes", "-o", "name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := tableMemberEnv(t)
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != 2 {
				t.Fatalf("want usage error, got %v", err)
			}
			if len(mock.Requests()) != 0 {
				t.Errorf("requests = %v", mock.Requests())
			}
		})
	}
}

func TestTableMemberDeclineMakesNoMutation(t *testing.T) {
	for _, verb := range []string{"remove", "set-permission"} {
		t.Run(verb, func(t *testing.T) {
			mock := tableMemberEnv(t)
			cmdutil.SetReader(strings.NewReader("n\n"))
			t.Cleanup(cmdutil.ResetReader)
			args := []string{"table-member", verb, memberUserID, "--table-id", "12"}
			if verb == "set-permission" {
				args = append(args, "--permission", "editor")
			}
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != 1 {
				t.Fatalf("want declined, got %v", err)
			}
			if len(mock.Requests()) != 0 {
				t.Errorf("requests = %v", mock.Requests())
			}
		})
	}
}

func TestTableMemberProtectedContextRefusesEvenWithYes(t *testing.T) {
	for _, verb := range []string{"leave", "remove", "set-permission"} {
		t.Run(verb, func(t *testing.T) {
			mock := tableMemberEnv(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Contexts[cfg.CurrentContext].Protected = nil
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			args := []string{"table-member", verb, memberUserID, "--table-id", "12", "--yes"}
			if verb == "leave" {
				args = []string{"table-member", verb, "12", "--yes"}
			}
			if verb == "set-permission" {
				args = append(args, "--permission", "editor")
			}
			_, err = runRoot(t, args...)
			if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "protected") {
				t.Fatalf("expected protected-context refusal: %v", err)
			}
			if len(mock.Requests()) != 0 {
				t.Errorf("requests = %v", mock.Requests())
			}
		})
	}
}
