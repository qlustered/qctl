package alert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestResolveFileExplainsNotPendingSuggestionIDs(t *testing.T) {
	for _, field := range []string{"accept_suggestion_ids", "dismiss_suggestion_ids"} {
		t.Run(field, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, migrationAlert())
			registerEmptySuggestions(mock)
			const id = "22222222-2222-2222-2222-222222222222"
			posts := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
				posts++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				ids, _ := body[field].([]any)
				if len(ids) != 1 || ids[0] != id {
					t.Errorf("%s = %v", field, body[field])
				}
				testutil.RespondJSON(w, http.StatusConflict, map[string]any{
					"detail": map[string]any{"msg": "These rule suggestions are not pending on the draft migration: " + id + "."},
				})
			})
			doc := fmt.Sprintf("apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n  %s: [%s]\n", field, id)
			_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, doc), "--yes")
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || posts != 1 {
				t.Fatalf("err = %v, posts = %d; want exit 2 after one POST", err, posts)
			}
			for _, want := range []string{id, "nothing was applied", "spec.accept_suggestion_ids", "spec.dismiss_suggestion_ids",
				"qctl get rule-suggestions --table-id 42 --show-kind", "qctl describe rule-suggestion <id> --table-id 42 -o json",
				"Remove or replace", "qctl change-proposal edit <proposal-id> -f <file> --submit"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestResolveFileLeavesPeriodicSuggestionDecisionsToTheServer(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	const id = "22222222-2222-2222-2222-222222222222"
	periodic := suggestionDetail(id, api.SuggestionActionUpdate, "Periodic rule")
	periodic.Source = api.Periodic
	registerPendingSuggestions(mock, periodic)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
		posts++
		var body api.AlertResolveSchema
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.DismissSuggestionIds == nil || len(*body.DismissSuggestionIds) != 1 || (*body.DismissSuggestionIds)[0].String() != id {
			t.Errorf("dismiss_suggestion_ids = %v", body.DismissSuggestionIds)
		}
		okResolve(w, r)
	})
	doc := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n  dismiss_suggestion_ids: [" + id + "]\n"
	_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, doc), "--yes")
	if err != nil || posts != 1 {
		t.Fatalf("periodic decision: %v; posts = %d, want 1", err, posts)
	}
}
