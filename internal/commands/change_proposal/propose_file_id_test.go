package change_proposal_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestAlertProposeFileIDPayload(t *testing.T) {
	manifest := writeFile(t, `apiVersion: qluster.ai/v1
kind: AlertResolution
metadata:
  id: "9"
spec:
  action: trash_file
  stored_item_id: 6
  comment: duplicate upload
`)
	for _, tc := range []struct {
		name   string
		args   []string
		fileID *int
		draft  bool
	}{
		{name: "all files", args: []string{"9", "--action", "trash_file", "--comment", "duplicate upload"}},
		{name: "one file", args: []string{"9", "--action", "trash_file", "--file-id", "6", "--comment", "duplicate upload"}, fileID: intPtr(6)},
		{name: "one file draft", args: []string{"9", "--action", "trash_file", "--file-id", "6", "--comment", "duplicate upload", "--draft"}, fileID: intPtr(6), draft: true},
		{name: "manifest", args: []string{"-f", manifest}, fileID: intPtr(6)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionTrashFile))
			p := proposal(api.ChangeProposalStatePendingReview, 1)
			if tc.draft {
				p.State = api.ChangeProposalStateDraft
			}
			action := api.AlertActionTrashFile
			comment := "duplicate upload"
			want := asJSON(t, api.AlertResolveSchema{Action: &action, StoredItemID: tc.fileID, Comment: &comment})
			p.Payload = want
			got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, p)

			_, _, err := run(t, append([]string{"alert", "propose"}, tc.args...)...)
			if err != nil {
				t.Fatalf("propose: %v", err)
			}
			if !reflect.DeepEqual((*got)["payload"], interface{}(want)) {
				t.Errorf("payload = %v, want %v", (*got)["payload"], want)
			}
			if (*got)["operation"] != "alert_resolve" || (*got)["alert_item_id"] != float64(9) || (*got)["submit"] != !tc.draft {
				t.Errorf("create body = %v", *got)
			}
			if n := countRequests(mock, "POST"); n != 1 {
				t.Errorf("%d POSTs, want only the proposal create", n)
			}
		})
	}
}

func TestAlertProposeFileIDRefusalsCostNoRequest(t *testing.T) {
	manifest := writeFile(t, mapValueDoc)
	for name, args := range map[string][]string{
		"an action that takes no file": {"9", "--action", "not_anomaly", "--file-id", "6"},
		"with --dry-run":               {"9", "--action", "trash_file", "--file-id", "6", "--dry-run"},
		"with -f":                      {"-f", manifest, "--file-id", "6"},
		"zero id":                      {"9", "--action", "trash_file", "--file-id", "0"},
		"negative id":                  {"9", "--action", "trash_file", "--file-id", "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			_, _, err := run(t, append([]string{"alert", "propose"}, args...)...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if err == nil || !strings.Contains(err.Error(), "--file-id") {
				t.Errorf("error = %v, want a --file-id explanation", err)
			}
			if requests := mock.Requests(); len(requests) != 0 {
				t.Errorf("requests = %v, want none", requests)
			}
		})
	}
}
