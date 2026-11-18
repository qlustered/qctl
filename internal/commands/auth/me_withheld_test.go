package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

// runMeWithheld runs `auth me -o <output>` against a server whose users/me
// withholds withheld, and returns stdout.
func runMeWithheld(t *testing.T, output string, withheld *[]api.AppPermission) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_ORG", "")
	t.Setenv("QCTL_SERVER", "")
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	env.SetupConfigWithOrg(mock.URL(), "", switchOrgCurrentOrgID)
	key, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(key, switchOrgCurrentOrgID, "token")
	role := api.RoleEnum("editor")
	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.MyUserSchema{
			ID:                  uuid.MustParse(switchOrgCurrentOrgID),
			Email:               "user@example.com",
			Role:                &role,
			WithheldPermissions: withheld,
		})
	})
	cmd := NewMeCommand()
	cmd.PersistentFlags().String("org", "", "")
	cmd.PersistentFlags().Count("verbose", "")
	cmd.Flags().String("server", "", "")
	cmd.Flags().Bool("allow-insecure-http", false, "")
	cmd.Flags().String("output", "table", "")
	cmd.SetArgs([]string{"--output", output})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestAuthMeShowsWithheldPermissions(t *testing.T) {
	withheld := []api.AppPermission{"dataset:data:write", "source:data:write"}
	out := runMeWithheld(t, "table", &withheld)
	want := "Withheld Permissions:   dataset:data:write, source:data:write (your role grants them; the organization turned them off)\n"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q:\n%s", want, out)
	}

	var doc struct {
		WithheldPermissions []string `json:"withheld_permissions"`
	}
	if err := json.Unmarshal([]byte(runMeWithheld(t, "json", &withheld)), &doc); err != nil {
		t.Fatal(err)
	}
	if strings.Join(doc.WithheldPermissions, ",") != "dataset:data:write,source:data:write" {
		t.Errorf("withheld_permissions = %v", doc.WithheldPermissions)
	}
}

// An organization withholding nothing leaves the line out, and the JSON
// carries an empty list whether the server sent [] or left the key out.
func TestAuthMeOmitsAnEmptyWithheldLine(t *testing.T) {
	for name, withheld := range map[string]*[]api.AppPermission{"empty": {}, "absent": nil} {
		t.Run(name, func(t *testing.T) {
			if out := runMeWithheld(t, "table", withheld); strings.Contains(out, "Withheld") {
				t.Errorf("unexpected withheld line:\n%s", out)
			}
			if out := runMeWithheld(t, "json", withheld); !strings.Contains(out, `"withheld_permissions": []`) {
				t.Errorf("want an empty withheld_permissions list:\n%s", out)
			}
		})
	}
}
