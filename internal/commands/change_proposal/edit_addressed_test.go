package change_proposal_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestEditClearsAddressedLists(t *testing.T) {
	for name, tc := range map[string]struct {
		flags    []string
		alerts   interface{}
		warnings interface{}
		submit   bool
	}{
		"clear alerts": {
			flags: []string{"--clear-also-alerts"}, alerts: []interface{}{},
		},
		"clear warnings": {
			flags: []string{"--clear-also-warnings"}, warnings: []interface{}{},
		},
		"clear both": {
			flags:  []string{"--clear-also-alerts", "--clear-also-warnings"},
			alerts: []interface{}{}, warnings: []interface{}{},
		},
		"clear alerts and replace warnings": {
			flags:  []string{"--clear-also-alerts", "--also-warning", "56,57"},
			alerts: []interface{}{}, warnings: []interface{}{float64(56), float64(57)},
		},
		"clear warnings and replace alerts": {
			flags:  []string{"--clear-also-warnings", "--also-alert", "10,11"},
			alerts: []interface{}{float64(10), float64(11)}, warnings: []interface{}{},
		},
		"clear and submit": {
			flags: []string{"--clear-also-alerts", "--submit"}, alerts: []interface{}{}, submit: true,
		},
		"false keeps both": {
			flags: []string{"--clear-also-alerts=false", "--clear-also-warnings=false", "--submit"}, submit: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			current := proposal(api.ChangeProposalStateDraft, 2)
			current.AddressedAlertItemIds = []int{10}
			current.AddressedWarningItemIds = []int{56}
			serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
			got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, current)

			if _, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, tc.flags...)...); err != nil {
				t.Fatalf("edit: %v", err)
			}
			for key, want := range map[string]interface{}{
				"addressed_alert_item_ids": tc.alerts, "addressed_warning_item_ids": tc.warnings,
				"version_id": float64(2),
			} {
				if value, present := (*got)[key]; !present || !reflect.DeepEqual(value, want) {
					t.Errorf("%s = %#v (present %v), want %#v", key, value, present, want)
				}
			}
			if payload, sent := (*got)["payload"]; sent {
				t.Errorf("payload = %v, want none: the server keeps the stored one", payload)
			}
			if value, present := (*got)["submit"]; present != tc.submit || (present && value != true) {
				t.Errorf("submit = %v (present %v), want present %v", value, present, tc.submit)
			}
			if n := countRequests(mock, "GET"); n != 1 {
				t.Errorf("%d GETs, want only the proposal read", n)
			}
			if n := countRequests(mock, "PATCH"); n != 1 {
				t.Errorf("%d PATCHes, want one", n)
			}
		})
	}
}
