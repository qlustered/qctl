package auth

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestAuthMeUsesSelectedOrganization(t *testing.T) {
	for _, selector := range []string{"flag", "env", "flag without default"} {
		t.Run(selector, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Setenv("QCTL_ORG", "")
			t.Setenv("QCTL_SERVER", "")
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			cfg := env.SetupConfigWithOrg(mock.URL(), "", switchOrgCurrentOrgID)
			if selector == "flag without default" {
				cfg.Contexts["default"].Organization = ""
				cfg.Contexts["default"].Organizations = []config.OrganizationRef{{ID: switchOrgCurrentOrgID, Name: "Other Org"}}
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
			}
			key, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(key, switchOrgTargetOrgID, "target-token")
			requests := 0
			handler := mockMyUser(t, "agent")
			mock.RegisterHandler("GET", "/api/orgs/"+switchOrgTargetOrgID+"/users/me", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer target-token" {
					t.Error("wrong credential")
				}
				handler(w, r)
			})
			cmd := NewMeCommand()
			cmd.PersistentFlags().String("org", "", "")
			cmd.PersistentFlags().Count("verbose", "")
			cmd.Flags().String("server", "", "")
			cmd.Flags().Bool("allow-insecure-http", false, "")
			cmd.Flags().String("output", "table", "")
			if selector == "env" {
				t.Setenv("QCTL_ORG", switchOrgTargetOrgID)
			} else {
				cmd.SetArgs([]string{"--org", switchOrgTargetOrgID})
			}
			cmd.SetOut(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want 1 to selected org", requests)
			}
		})
	}
}

// `auth me`'s table view prints the profile the server answered with -- the
// email, the role, the org names -- plus the context fields `auth login` wrote
// from that same answer. It goes through a writer, so the rendered text can be checked directly.
//
// The json/yaml path (printMeStructured) is deliberately NOT covered here: a
// structured document must carry the server's exact bytes and rely on the
// encoder's own escaping.
func TestPrintMeTableSanitizesServerSuppliedProfileFields(t *testing.T) {
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "dev\x1b[2K",
		Contexts: map[string]*config.Context{
			"dev\x1b[2K": {
				Server:           "http://localhost:8000",
				Organization:     "550e8400-e29b-41d4-a716-446655440000",
				OrganizationName: "Acme\x1b[31mCorp",
			},
		},
	}
	userInfo := &auth.UserMeResponse{
		ID:                      "user-1\x00",
		Email:                   "alice\x1b]0;pwned\x07@example.com",
		Role:                    "admin\x1b[31m",
		ActiveOrganizationIDs:   []string{"550e8400-e29b-41d4-a716-446655440000"},
		ActiveOrganizationNames: []string{"Acme\x1b[7mCorp"},
		IsActive:                true,
	}

	var buf strings.Builder
	printMeTable(&buf, cfg, userInfo, 0)
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07\x00") {
		t.Fatalf("auth me reached the terminal with a live control byte: %q", out)
	}
	for _, want := range []string{
		`dev\x1b[2K`,
		`user-1\x00`,
		`alice\x1b]0;pwned\x07@example.com`,
		`admin\x1b[31m`,
		`Acme\x1b[31mCorp`,
		`Acme\x1b[7mCorp`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the visible escape %q:\n%s", want, out)
		}
	}
}
