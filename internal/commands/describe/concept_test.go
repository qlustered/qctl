package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeStandardFieldShowsDetail(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	description := "Policy identifier"
	aliases := []string{"policy no", "policy #"}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Full{ID: id, OrganizationID: uuid.MustParse(testOrgID), Slug: "policy-number", Name: "Policy Number", Description: &description, Aliases: &aliases, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	cmd := setupTableKindTestCommand()
	parent, _, _ := cmd.Find([]string{"describe"})
	parent.AddCommand(NewStandardFieldCommand())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "standard-field", id.String()})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Policy Number", "Policy identifier", "policy no, policy #"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q lacks %q", out.String(), want)
		}
	}
}
