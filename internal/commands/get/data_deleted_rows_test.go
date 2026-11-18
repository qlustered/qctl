package get

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// --deleted-rows replaced the boolean --is-deleted in the dd342a4ae spec. It
// is derived, so it arrived with the generic passthrough help.
func TestDeletedRowsFlagIsCurated(t *testing.T) {
	cmd := &cobra.Command{Use: "data"}
	RegisterDataFlags(cmd)

	if cmd.Flags().Lookup("is-deleted") != nil {
		t.Errorf("--is-deleted is registered; the parameter it derived from is gone")
	}
	f := cmd.Flags().Lookup("deleted-rows")
	if f == nil {
		t.Fatal("--deleted-rows was not registered")
	}
	if f.Usage != paramflags.DeletedRowsHelp {
		t.Errorf("--deleted-rows help = %q, want the shared curated line", f.Usage)
	}
	for _, v := range api.StringEnumValues["DeletedRows"] {
		if !strings.Contains(f.Usage, v) {
			t.Errorf("--deleted-rows help does not name %q: %q", v, f.Usage)
		}
	}
}

func TestGetDataRefusesAnUnknownDeletedRowsValue(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleGetData())
		})

	out, err := execData(t, []string{"1", "--deleted-rows", "deleted"})
	if err == nil {
		t.Fatalf("expected refusal, got success:\n%s", out)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/data") {
			t.Errorf("refusal must precede every request; saw %q", req)
		}
	}
}

// deleted_rows is not quarantine-only: atlas applies it to clean reads too.
func TestGetDataSendsDeletedRowsAndSplitsTheCount(t *testing.T) {
	var saw bool
	out := runData(t, []string{"1", "--deleted-rows", "all"}, func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if got := r.URL.Query().Get("deleted_rows"); got != string(api.DeletedRowsAll) {
			t.Errorf("deleted_rows = %q, want all", got)
		}
		if r.URL.Query().Has("is_bad_data") {
			t.Errorf("is_bad_data was sent: %q", r.URL.RawQuery)
		}
		resp := sampleGetData()
		live, deleted := 1, 0
		resp.LiveRowsCount, resp.DeletedRowsCount = &live, &deleted
		testutil.RespondJSON(w, http.StatusOK, resp)
	})
	if !saw {
		t.Fatal("the data request never arrived")
	}
	if !strings.Contains(out, "Matching Rows: 1 (1 live, 0 deleted)") {
		t.Errorf("summary lacks the live/deleted split:\n%s", out)
	}
}
