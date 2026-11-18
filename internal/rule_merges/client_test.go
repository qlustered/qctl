package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID       = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testTargetID    = 12
	testSourceID    = 7
	testMergeID     = 5
	mergesPath      = "/api/orgs/" + testOrgID + "/datasets/12/rule-merges"
	mergePath       = mergesPath + "/5"
	applyPath       = mergePath + "/apply"
	testCandidateID = "9a3e1c52-7b4d-4f1e-8c2a-1d5e6f7a8b90"
)

// previewedMerge is a previewed merge with a created item and a conflict item
// whose snapshot params carry a fraction and an integer past 2^53.
const previewedMerge = `{
  "id": 5,
  "source_dataset_id": 7,
  "target_dataset_id": 12,
  "state": "previewed",
  "summary": null,
  "created_at": "2026-09-25T10:00:00Z",
  "items": [
    {
      "id": 11,
      "source_object_id": "3f1d2c4b-5a6e-4f70-8a9b-0c1d2e3f4a5b",
      "target_object_id": null,
      "action": "created",
      "conflict_choice": null,
      "note": null,
      "rule_snapshot": {"rule_revision_id": "r1", "params": {}, "severity": "warning",
        "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
      "base_snapshot": null,
      "target_snapshot": null,
      "source_changes": null,
      "target_changes": null,
      "candidate_object_ids": [],
      "added_field_names": []
    },
    {
      "id": 12,
      "source_object_id": "4a2e3d5c-6b7f-4081-9bac-1d2e3f4a5b6c",
      "target_object_id": "` + testCandidateID + `",
      "action": "conflict",
      "conflict_choice": null,
      "note": "Both tables changed params since the last copy.",
      "rule_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.12345, "max": 9007199254740993},
        "severity": "blocker", "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
      "base_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.1, "max": 10}, "severity": "blocker",
        "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
      "target_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.2, "max": 10}, "severity": "blocker",
        "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
      "source_changes": ["params"],
      "target_changes": ["params"],
      "candidate_object_ids": [],
      "added_field_names": ["amount_ok"]
    }
  ]
}`

// applyingMerge is previewedMerge after the server accepted an apply.
var applyingMerge = strings.Replace(previewedMerge, `"state": "previewed"`, `"state": "applying"`, 1)

func newTestClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, mock
}

func respondRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func TestNewClient_RejectsAnInvalidOrganizationID(t *testing.T) {
	if _, err := NewClient("http://mock.local", "not-a-uuid", 0); err == nil {
		t.Fatal("NewClient accepted an organization ID that is not a UUID")
	}
}

// TestPreview_PostsTheSourceAndDecodesItems: the preview posts exactly
// {"source_dataset_id": N} to the target's route, and snapshot params keep
// their value -- including an integer json.Unmarshal into interface{} would
// round.
func TestPreview_PostsTheSourceAndDecodesItems(t *testing.T) {
	c, mock := newTestClient(t)
	var sent map[string]interface{}
	mock.RegisterHandler("POST", mergesPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Errorf("request body is not JSON: %v: %s", err, raw)
		}
		respondRaw(w, http.StatusOK, previewedMerge)
	})

	merge, body, err := c.Preview("token", testTargetID, testSourceID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if string(body) != previewedMerge {
		t.Errorf("body = %s, want the response body as received", body)
	}

	if want := map[string]interface{}{"source_dataset_id": float64(testSourceID)}; !reflect.DeepEqual(sent, want) {
		t.Errorf("request body = %#v, want %#v", sent, want)
	}
	if got, want := mock.Requests(), []string{"POST " + mergesPath}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}

	if merge.ID != testMergeID || merge.State != api.DatasetCopyStatePreviewed || len(merge.Items) != 2 {
		t.Fatalf("merge = id %d state %q with %d items, want id 5 previewed with 2", merge.ID, merge.State, len(merge.Items))
	}
	conflict := merge.Items[1]
	if conflict.Action != api.DatasetCopyItemActionConflict || conflict.TargetObjectID == nil ||
		*conflict.TargetObjectID != testCandidateID {
		t.Errorf("conflict item = %+v", conflict)
	}
	params := conflict.RuleSnapshot.Params
	if got := params["min"]; got != 0.12345 {
		t.Errorf("params.min = %#v, want 0.12345", got)
	}
	if got := params["max"]; got != int64(9007199254740993) {
		t.Errorf("params.max = %#v (%T), want int64(9007199254740993); the body was not decoded with jsonnum", got, got)
	}
	if got := conflict.AddedFieldNames; !reflect.DeepEqual(got, []string{"amount_ok"}) {
		t.Errorf("added_field_names = %v", got)
	}
}

func TestGet_DecodesTheMerge(t *testing.T) {
	c, mock := newTestClient(t)
	mock.RegisterHandler("GET", mergePath, func(w http.ResponseWriter, _ *http.Request) {
		respondRaw(w, http.StatusOK, applyingMerge)
	})

	merge, _, err := c.Get("token", testTargetID, testMergeID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if merge.State != api.DatasetCopyStateApplying || merge.SourceDatasetID != testSourceID ||
		merge.TargetDatasetID != testTargetID || len(merge.Items) != 2 {
		t.Errorf("merge = %+v", merge)
	}
	if got := merge.Items[1].RuleSnapshot.Params["max"]; got != int64(9007199254740993) {
		t.Errorf("params.max = %#v (%T), want int64(9007199254740993)", got, got)
	}
}

// captureApply registers an apply handler answering status with body, and
// returns where the decoded request body lands.
func captureApply(t *testing.T, mock *testutil.MockAPIServer, status int, body string) *map[string]json.RawMessage {
	t.Helper()
	sent := new(map[string]json.RawMessage)
	mock.RegisterHandler("POST", applyPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, sent); err != nil {
			t.Errorf("request body is not a JSON object: %v: %s", err, raw)
		}
		respondRaw(w, status, body)
	})
	return sent
}

// TestApply_SendsResolutionsInOrder: each resolution keeps its position and
// its set fields, and an unset optional field goes out absent or null.
func TestApply_SendsResolutionsInOrder(t *testing.T) {
	c, mock := newTestClient(t)
	sent := captureApply(t, mock, http.StatusAccepted, applyingMerge)

	target := uuid.MustParse(testCandidateID)
	params := map[string]interface{}{"min": 12.5}
	mapping := map[string]string{"value": "amount"}
	severity := api.RuleSeverityBlocker
	resolutions := []Resolution{
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{
			ItemID: 12, Choice: api.DatasetCopyConflictChoiceEditSource, Params: &params, ColumnMapping: &mapping, Severity: &severity,
		}},
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 11, Choice: api.DatasetCopyConflictChoiceKeepTarget}},
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 13, Choice: api.DatasetCopyConflictChoiceTakeSource, TargetRuleID: &target}},
	}

	merge, _, err := c.Apply("token", testTargetID, testMergeID, resolutions)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if merge.State != api.DatasetCopyStateApplying {
		t.Errorf("state = %q, want applying", merge.State)
	}

	var got []map[string]interface{}
	if err := json.Unmarshal((*sent)["resolutions"], &got); err != nil {
		t.Fatalf("resolutions: %v: %s", err, (*sent)["resolutions"])
	}
	want := []map[string]interface{}{
		{"item_id": float64(12), "choice": "edit_source", "params": map[string]interface{}{"min": 12.5},
			"column_mapping": map[string]interface{}{"value": "amount"}, "severity": "blocker"},
		{"item_id": float64(11), "choice": "keep_target"},
		{"item_id": float64(13), "choice": "take_source", "target_rule_id": testCandidateID},
	}
	if len(got) != len(want) {
		t.Fatalf("sent %d resolutions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		for _, key := range []string{"item_id", "choice", "target_rule_id", "params", "column_mapping", "severity"} {
			w, wantSet := want[i][key]
			g, present := got[i][key]
			switch {
			case wantSet && !reflect.DeepEqual(g, w):
				t.Errorf("resolution %d: %s = %#v, want %#v", i, key, g, w)
			case !wantSet && present && g != nil:
				t.Errorf("resolution %d: unset %s went out as %#v, want absent or null", i, key, g)
			}
		}
	}
}

// TestApply_SendsRowFilterOnlyWhenSet: a filter goes out as its object, a
// RowFilterSet resolution without one as null, and any other with no
// row_filter key at all, which the server would refuse on every choice but
// edit_source.
func TestApply_SendsRowFilterOnlyWhenSet(t *testing.T) {
	c, mock := newTestClient(t)
	sent := captureApply(t, mock, http.StatusAccepted, applyingMerge)

	conditions := []api.RowFilterCondition{{ColumnName: "status", Operator: api.RowFilterOperator("eq"), Value: resolutionTestStr("done")}}
	filter := &api.DescribedRowFilter{Description: "Completed orders", Conditions: &conditions}
	resolutions := []Resolution{
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 11, Choice: api.DatasetCopyConflictChoiceEditSource, RowFilter: filter}, RowFilterSet: true},
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 12, Choice: api.DatasetCopyConflictChoiceEditSource}, RowFilterSet: true},
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 13, Choice: api.DatasetCopyConflictChoiceTakeSource}},
	}
	if _, _, err := c.Apply("token", testTargetID, testMergeID, resolutions); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var got []map[string]json.RawMessage
	if err := json.Unmarshal((*sent)["resolutions"], &got); err != nil {
		t.Fatalf("resolutions: %v: %s", err, (*sent)["resolutions"])
	}
	if len(got) != 3 {
		t.Fatalf("sent %d resolutions, want 3: %s", len(got), (*sent)["resolutions"])
	}
	testutil.RequireSameJSONTree(t, got[0]["row_filter"],
		`{"description":"Completed orders","conditions":[{"column_name":"status","operator":"eq","value":"done","value_high":null,"value_list":null}]}`)
	if raw, ok := got[1]["row_filter"]; !ok || string(raw) != "null" {
		t.Errorf("resolution 1: row_filter = %s (present %t), want null", raw, ok)
	}
	if raw, ok := got[2]["row_filter"]; ok {
		t.Errorf("resolution 2: row_filter = %s, want no key", raw)
	}
	if string(got[1]["choice"]) != `"edit_source"` || string(got[1]["item_id"]) != "12" {
		t.Errorf("resolution 1 lost its generated fields: %v", got[1])
	}
}

// TestApplyRequestMirrorsTheGeneratedSchema fails when a regenerated
// RuleMergeApplyRequestSchema gains or renames a key the hand-written
// applyRequest does not send.
func TestApplyRequestMirrorsTheGeneratedSchema(t *testing.T) {
	keys := func(v interface{}) []string {
		var out []string
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			out = append(out, typ.Field(i).Tag.Get("json"))
		}
		return out
	}
	if got, want := keys(applyRequest{}), keys(api.RuleMergeApplyRequestSchema{}); !reflect.DeepEqual(got, want) {
		t.Errorf("applyRequest keys = %v, want the generated %v", got, want)
	}
}

// TestApply_NoResolutionsSendsNoKey: a nil and an empty slice both send a
// body without a resolutions key.
func TestApply_NoResolutionsSendsNoKey(t *testing.T) {
	for name, resolutions := range map[string][]Resolution{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			c, mock := newTestClient(t)
			sent := captureApply(t, mock, http.StatusAccepted, applyingMerge)
			if _, _, err := c.Apply("token", testTargetID, testMergeID, resolutions); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if *sent == nil {
				t.Fatal("the apply handler never saw a body")
			}
			if raw, ok := (*sent)["resolutions"]; ok {
				t.Errorf("body carries resolutions = %s, want no key", raw)
			}
		})
	}
}

// TestApply_A200IsNotSuccess: apply answers 202; any other 2xx is an error.
func TestApply_A200IsNotSuccess(t *testing.T) {
	c, mock := newTestClient(t)
	captureApply(t, mock, http.StatusOK, applyingMerge)

	merge, _, err := c.Apply("token", testTargetID, testMergeID, nil)
	if err == nil {
		t.Fatalf("Apply returned %+v for a 200, want an error", merge)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitGenericError, err)
	}
}

// TestPreview_A202IsNotSuccess: preview answers 200; any other 2xx is an error.
func TestPreview_A202IsNotSuccess(t *testing.T) {
	c, mock := newTestClient(t)
	mock.RegisterHandler("POST", mergesPath, func(w http.ResponseWriter, _ *http.Request) {
		respondRaw(w, http.StatusAccepted, previewedMerge)
	})
	if merge, _, err := c.Preview("token", testTargetID, testSourceID); err == nil {
		t.Fatalf("Preview returned %+v for a 202, want an error", merge)
	}
}

// TestApply_NotPreviewedExitsTwo: the 409 for a merge that is no longer
// previewed carries the server's words and exits 2.
func TestApply_NotPreviewedExitsTwo(t *testing.T) {
	const msg = "Rule merge 5 is applied; only a previewed merge can be applied."
	c, mock := newTestClient(t)
	captureApply(t, mock, http.StatusConflict, `{"detail":{"msg":"`+msg+`"}}`)

	_, _, err := c.Apply("token", testTargetID, testMergeID, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error %q does not carry the server's message", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Errorf("error is not an *APIError with status 409: %#v", err)
	}
}

// TestErrorStatusesKeepTheirExitCode: every method maps an object-shaped
// error body to the status's exit code and keeps the server's msg. 422 is
// the status the generated parser fails on before the body is inspectable.
func TestErrorStatusesKeepTheirExitCode(t *testing.T) {
	calls := map[string]struct {
		method, path string
		call         func(*Client) error
	}{
		"Preview": {"POST", mergesPath, func(c *Client) error {
			_, _, err := c.Preview("token", testTargetID, testSourceID)
			return err
		}},
		"Get": {"GET", mergePath, func(c *Client) error {
			_, _, err := c.Get("token", testTargetID, testMergeID)
			return err
		}},
		"Apply": {"POST", applyPath, func(c *Client) error {
			_, _, err := c.Apply("token", testTargetID, testMergeID, nil)
			return err
		}},
	}
	statuses := map[int]int{
		http.StatusBadRequest:          qerrors.ExitBadUsage,
		http.StatusUnauthorized:        qerrors.ExitUnauthorized,
		http.StatusForbidden:           qerrors.ExitUnauthorized,
		http.StatusNotFound:            qerrors.ExitNotFound,
		http.StatusConflict:            qerrors.ExitBadUsage,
		http.StatusUnprocessableEntity: qerrors.ExitBadUsage,
		http.StatusInternalServerError: qerrors.ExitGenericError,
	}
	for name, call := range calls {
		for status, wantExit := range statuses {
			t.Run(fmt.Sprintf("%s/%d", name, status), func(t *testing.T) {
				msg := fmt.Sprintf("server says %d", status)
				c, mock := newTestClient(t)
				mock.RegisterHandler(call.method, call.path, func(w http.ResponseWriter, _ *http.Request) {
					respondRaw(w, status, `{"detail":{"msg":"`+msg+`","loc":["body","source_dataset_id"]}}`)
				})
				err := call.call(c)
				if err == nil {
					t.Fatal("expected an error")
				}
				if got := qerrors.GetExitCode(err); got != wantExit {
					t.Errorf("exit code = %d, want %d; err = %v", got, wantExit, err)
				}
				if !strings.Contains(err.Error(), msg) {
					t.Errorf("error %q does not carry the server's message %q", err, msg)
				}
			})
		}
	}
}
