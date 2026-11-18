package change_proposal_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestAlertProposeCarriesMigrationChoicesThroughApproval(t *testing.T) {
	const acceptID = "11111111-1111-1111-1111-111111111111"
	const dismissID = "22222222-2222-2222-2222-222222222222"
	for _, tc := range []struct {
		name      string
		issueType api.AlertType
		spec      string
		payload   map[string]interface{}
	}{
		{"suggestion decisions", api.AlertTypeNeedsReviewMigration,
			"  action: apply_migration\n  accept_suggestion_ids: [" + acceptID + "]\n  dismiss_suggestion_ids: [" + dismissID + "]\n",
			map[string]interface{}{"action": "apply_migration", "accept_suggestion_ids": []interface{}{acceptID}, "dismiss_suggestion_ids": []interface{}{dismissID}}},
		{"key columns", api.AlertTypeNeedsReviewMigration,
			"  action: apply_migration\n  key_columns: [id]\n",
			map[string]interface{}{"action": "apply_migration", "key_columns": []interface{}{"id"}}},
		{"keyless", api.AlertTypeNeedsReviewMigration,
			"  action: apply_migration\n  no_key_columns: true\n",
			map[string]interface{}{"action": "apply_migration", "no_key_columns": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newEnv(t)
			target := anomalyAlert(api.AlertActionApplyMigration)
			target.IssueType = tc.issueType
			target.ActionModes = &map[string]api.ActionMode{"apply_migration": api.ActionModePropose}
			serveJSON(mock, "GET", alertURL, http.StatusOK, target)
			p := pending("apply_migration")
			p.Payload = tc.payload
			created := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, p)
			manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n" + tc.spec
			_, _, err := run(t, "alert", "propose", "-f", writeFile(t, manifest))
			wantExit(t, err, qerrors.ExitSuccess)
			payload, _ := (*created)["payload"].(map[string]interface{})
			supplied := map[string]interface{}{}
			for key, value := range payload {
				if value != nil {
					supplied[key] = value
				}
			}
			if !reflect.DeepEqual(supplied, tc.payload) || (*created)["submit"] != true {
				t.Fatalf("proposal request = %v, want submitted payload %v", *created, tc.payload)
			}
			p.Payload = payload
			serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, p))
			approved := captureJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())
			_, preview, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
			wantExit(t, err, qerrors.ExitSuccess)
			if (*approved)["impact_digest"] != "d1g35t" || (*approved)["version_id"] != float64(p.VersionID) {
				t.Errorf("approval lost version or digest: %v", *approved)
			}
			_, block, _ := strings.Cut(preview, "```yaml\n")
			raw, _, _ := strings.Cut(block, "```")
			doc, parseErr := alerts.ParseResolution([]byte(raw))
			if parseErr != nil {
				t.Fatalf("approval preview is not a resolution manifest: %v\n%s", parseErr, preview)
			}
			if !reflect.DeepEqual(asJSON(t, doc.Spec.ToRequest()), payload) {
				t.Errorf("approval preview changed the proposed request: %s", raw)
			}
			want := []string{"GET " + alertURL, "GET " + migrationDetailsURL, "POST " + proposalsURL, "GET " + proposalURL, "POST " + proposalURL + approveURL}
			if !reflect.DeepEqual(mock.Requests(), want) {
				t.Errorf("requests = %v, want %v", mock.Requests(), want)
			}
		})
	}
}

func TestAlertProposeRefusesKeyColumnsOnAColumnMappingLocally(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeInitialFields, api.AlertTypeUnexpectedFieldName} {
		t.Run(string(issueType), func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, alerts.AlertFull{ID: 9, IssueType: issueType})
			doc := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n" +
				"  key_columns: [id]\n  field_names_resolution:\n    - incoming_field_name: ID\n      field_name: id\n      operation: new\n      scope: dataset\n"
			_, _, err := run(t, "alert", "propose", "-f", writeFile(t, doc))
			wantExit(t, err, qerrors.ExitBadUsage)
			if n := countRequests(mock, "POST"); n != 0 {
				t.Errorf("%d POSTs, want local refusal", n)
			}
		})
	}
}

func TestApproveExplainsNotPendingMigrationSuggestions(t *testing.T) {
	mock := newEnv(t)
	p := pending("apply_migration")
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, p))
	target := anomalyAlert(api.AlertActionApplyMigration)
	target.IssueType = api.AlertTypeNeedsReviewMigration
	serveJSON(mock, "GET", alertURL, http.StatusOK, target)
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusConflict,
		map[string]interface{}{"detail": map[string]interface{}{
			"msg": "These rule suggestions are not pending on the draft migration: 22222222-2222-2222-2222-222222222222.",
		}})
	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	for _, want := range []string{"Remove or replace", "qctl change-proposal edit <proposal-id> -f <file> --submit", "still pending_review"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
