package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// runDescribeFile serves body and returns the command's stdout.
func runDescribeFile(t *testing.T, root *cobra.Command, body interface{}, args ...string) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, body)
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return stdout.String()
}

// is_delta is always in status, so a plain file says false;
// delta_cursor appears only on a delta delivery.
func TestDescribeFileShowsTheSyncFields(t *testing.T) {
	plain := sampleStoredItemFull()
	out := runDescribeFile(t, setupFileDescribeTestCommand(), plain, "describe", "file", "1")
	if !strings.Contains(out, "  is_delta: false\n") {
		t.Errorf("a plain file does not say is_delta: false:\n%s", out)
	}
	if strings.Contains(out, "delta_cursor") {
		t.Errorf("a plain file shows a delta_cursor:\n%s", out)
	}

	isDelta, cursor := true, 1042
	delta := sampleStoredItemFull()
	delta.IsDelta, delta.DeltaCursor = &isDelta, &cursor
	out = runDescribeFile(t, setupFileDescribeTestCommand(), delta, "describe", "file", "1")
	for _, want := range []string{"  is_delta: true\n", "  delta_cursor: 1042\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("a delta file's status lacks %q:\n%s", want, out)
		}
	}
}
