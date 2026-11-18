package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// The server ANDs is_row_level IS TRUE into its file filter while its own
// is_row_level defaults to false, so --file-id must send is_row_level=true
// whether or not the user passed --is-row-level.
func TestGetAlertsFileIDQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"alone", []string{"--file-id", "55"}},
		{"with --is-row-level", []string{"--file-id", "55", "--is-row-level"}},
		{"with --is-muted", []string{"--file-id", "55", "--is-muted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			hits := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
				hits++
				query := r.URL.Query()
				if got := query.Get("stored_item_id"); got != "55" {
					t.Errorf("stored_item_id = %q, want 55 (query %v)", got, query)
				}
				if got := query.Get("is_row_level"); got != "true" {
					t.Errorf("is_row_level = %q, want true (query %v)", got, query)
				}
				testutil.RespondJSON(w, http.StatusOK, alerts.AlertsPage{})
			})
			if err := runGetAlerts(t, tc.args...); err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Fatalf("alerts hits = %d, want 1", hits)
			}
		})
	}
}

func TestGetAlertsFileIDValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"zero", []string{"--file-id", "0"}},
		{"negative", []string{"--file-id", "-3"}},
		{"not row-level", []string{"--file-id", "55", "--is-row-level=false"}},
		{"not muted", []string{"--file-id", "55", "--is-muted=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			if err := runGetAlerts(t, tc.args...); qerrors.GetExitCode(err) != 2 {
				t.Fatalf("error = %v, want exit 2 before credentials or requests", err)
			}
		})
	}
}

// The table view says what COUNT means for the file; the structured views
// carry count_capped: false per alert and gain no line.
func TestGetAlertsFileIDCountNote(t *testing.T) {
	const note = "Counts are exact for file 55"
	for _, tc := range []struct {
		format   string
		empty    bool
		wantNote bool
	}{
		{format: "table", wantNote: true},
		{format: "table", empty: true},
		{format: "json"},
		{format: "yaml"},
		{format: "name"},
	} {
		name := tc.format
		if tc.empty {
			name += "/empty"
		}
		t.Run(name, func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			results := []alerts.AlertTiny{sampleAlertTiny()}
			if tc.empty {
				results = nil
			}
			notCapped := false
			for i := range results {
				results[i].Count, results[i].CountCapped = 1200, &notCapped
			}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
				total := len(results)
				testutil.RespondJSON(w, http.StatusOK, alerts.AlertsPage{Results: results, TotalRows: &total})
			})
			cmd := setupAlertsTestCommand()
			cmd.SetArgs([]string{"get", "alerts", "--file-id", "55", "-o", tc.format})
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(errOut.String(), note); got != tc.wantNote {
				t.Errorf("stderr carries the count note = %v, want %v: %q", got, tc.wantNote, errOut.String())
			}
			if strings.Contains(out.String(), note) {
				t.Errorf("the count note leaked onto stdout: %q", out.String())
			}
			if tc.wantNote && (!strings.Contains(out.String(), "1200") || strings.Contains(out.String(), "1200+")) {
				t.Errorf("table count = %q, want the exact 1200 with no cap marker", out.String())
			}
		})
	}
}
