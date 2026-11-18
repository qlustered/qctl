package alert

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// twoFileAlert is an unresolved duplicate_header alert 9 linked to files 5
// and 6, offering trash_file and try_again.
func twoFileAlert() alerts.AlertFull {
	resolved := false
	list := []api.AlertAction{api.AlertActionTrashFile, api.AlertActionTryAgain}
	a, b := "jan.csv", "feb.csv"
	links := []api.StoredItemToAlertSchema{{StoredItemID: 5, FileName: &a}, {StoredItemID: 6, FileName: &b}}
	return alerts.AlertFull{
		ID:                  9,
		DatasetID:           42,
		DatasetName:         "orders",
		IssueType:           api.AlertTypeDuplicateHeader,
		Resolved:            &resolved,
		AlertActionsList:    &list,
		StoredItemsToAlerts: &links,
	}
}

// --file-id reaches the wire as stored_item_id, and an alert the backend
// leaves open because it links another kept file is reported as that, not as
// background work still to come.
func TestResolveFileIDDiscardsOneFile(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, twoFileAlert())
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			okResolve(w, r)
		})

	stdout, _, err := runAlertCommandSplit(t, "alert", "resolve", "9", "--action", "trash_file", "--file-id", "6",
		"--comment", "duplicate upload")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sent.Action == nil || *sent.Action != api.AlertActionTrashFile {
		t.Errorf("action did not reach the wire: %+v", sent)
	}
	if sent.StoredItemID == nil || *sent.StoredItemID != 6 {
		t.Errorf("stored_item_id = %v, want 6", sent.StoredItemID)
	}
	if sent.Comment == nil || *sent.Comment != "duplicate upload" {
		t.Errorf("comment = %v, want %q", sent.Comment, "duplicate upload")
	}
	want := "alert 9 — action trash_file accepted for file 6; the alert stays open while it links another file that is not trashed"
	if !strings.Contains(stdout, want) {
		t.Errorf("output lacks %q:\n%s", want, stdout)
	}
}

// Without --file-id stored_item_id is null, so the backend trashes every file
// of the alert.
func TestResolveWithoutFileIDSendsNoStoredItemID(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, twoFileAlert())
	var raw map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&raw)
			okResolve(w, r)
		})

	if _, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "trash_file", "--comment", "x"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if v := raw["stored_item_id"]; v != nil {
		t.Errorf("stored_item_id = %v without --file-id, want null", v)
	}
}

// Each wrong combination exits 2 before any request.
func TestResolveFileIDRefusals(t *testing.T) {
	manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\nspec:\n  action: trash_file\n"
	cases := map[string][]string{
		"an action that takes no file": {"9", "--action", "try_again", "--file-id", "6"},
		"with --dry-run":               {"9", "--action", "trash_file", "--file-id", "6", "--dry-run"},
		"with -f":                      {"-f", writeManifest(t, manifest), "--file-id", "6"},
		"a non-positive id":            {"9", "--action", "trash_file", "--file-id", "0"},
		"trash without --comment":      {"9", "--action", "trash_file"},
		"a blank --comment":            {"9", "--action", "trash_file", "--comment", "  "},
		"an over-cap --comment":        {"9", "--action", "trash_file", "--comment", strings.Repeat("x", 10001)},
		"--comment on another action":  {"9", "--action", "try_again", "--comment", "why"},
		"--comment with -f":            {"-f", writeManifest(t, manifest), "--comment", "why"},
		"--comment with --dry-run":     {"9", "--action", "trash_file", "--comment", "why", "--dry-run"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			requests := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/9",
				func(w http.ResponseWriter, _ *http.Request) { requests++ })
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) { requests++ })

			_, err := runAlertCommand(t, append([]string{"alert", "resolve"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if requests != 0 {
				t.Errorf("%d request(s) sent before the refusal", requests)
			}
		})
	}
}

// A manifest's spec.stored_item_id reads the same as --file-id when the alert
// stays open.
func TestResolveFileManifestNamesTheFileItDiscarded(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, twoFileAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", okResolve)
	manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\n" +
		"spec:\n  action: trash_file\n  stored_item_id: 5\n  comment: duplicate upload\n"

	stdout, _, err := runAlertCommandSplit(t, "alert", "resolve", "-f", writeManifest(t, manifest))
	if err != nil {
		t.Fatalf("resolve -f: %v", err)
	}
	if want := "action trash_file accepted for file 5; the alert stays open"; !strings.Contains(stdout, want) {
		t.Errorf("output lacks %q:\n%s", want, stdout)
	}
}

// A manifest without spec.comment, or with a blank one, is refused before the
// alert is read.
func TestResolveFileManifestTrashNeedsAComment(t *testing.T) {
	for name, comment := range map[string]string{"missing": "", "blank": "  comment: \"  \"\n"} {
		t.Run(name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			requests := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/9",
				func(w http.ResponseWriter, _ *http.Request) { requests++ })
			manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\n" +
				"spec:\n  action: trash_alert_associated_files\n" + comment

			_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, manifest))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if requests != 0 {
				t.Errorf("%d request(s) sent before the refusal", requests)
			}
		})
	}
}
