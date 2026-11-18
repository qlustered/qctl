package rule_suggestions

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	sugA      = "3f2a0000-1111-2222-3333-444444444444"
	sugB      = "3f2a0000-1111-2222-3333-555555555555"

	bulkAcceptPath  = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/bulk-accept"
	bulkDismissPath = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/bulk-dismiss"
)

func newTestClient(t *testing.T) (*testutil.MockAPIServer, *Client) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return mock, c
}

func TestBulkAcceptSendsIDs(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("POST", bulkAcceptPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{
			Processed: 2, Skipped: 0, Errors: []string{},
		})
	})

	got, err := c.BulkAccept("test-token", 8, BulkSelector{IDs: []string{sugA, sugB}})
	if err != nil {
		t.Fatalf("BulkAccept: %v", err)
	}
	if got.Processed != 2 {
		t.Errorf("Processed = %d, want 2", got.Processed)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	ids, ok := body["suggestion_ids"].([]interface{})
	if !ok || len(ids) != 2 || ids[0] != sugA || ids[1] != sugB {
		t.Errorf("suggestion_ids wrong, body: %s", raw)
	}
	// A filter the caller did not set must not appear at all: the server ANDs
	// every key present, so a stray null would narrow the write.
	if _, present := body["source"]; present {
		t.Errorf("source must be omitted when unset, body: %s", raw)
	}
	if _, present := body["action"]; present {
		t.Errorf("action must be omitted when unset, body: %s", raw)
	}
}

func TestBulkAcceptSendsFilterWithoutIDs(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("POST", bulkAcceptPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Processed: 3, Errors: []string{}})
	})

	src := api.Profiling
	act := api.SuggestionActionUpdate
	if _, err := c.BulkAccept("test-token", 8, BulkSelector{Source: &src, Action: &act}); err != nil {
		t.Fatalf("BulkAccept: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if body["source"] != "profiling" {
		t.Errorf("source = %v, want profiling; body: %s", body["source"], raw)
	}
	if body["action"] != "update" {
		t.Errorf("action = %v, want update; body: %s", body["action"], raw)
	}
	// suggestion_ids has no omitempty in the generated struct, so it serialises
	// as an explicit null. The server's `is not None` check treats null as
	// absent, which is what a filter-only selection needs.
	if v, present := body["suggestion_ids"]; present && v != nil {
		t.Errorf("suggestion_ids must be null when selecting by filter, body: %s", raw)
	}
}

func TestBulkDismissSendsFamily(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("POST", bulkDismissPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	if _, err := c.BulkDismiss("test-token", 8, BulkSelector{IDs: []string{sugA}}, true); err != nil {
		t.Fatalf("BulkDismiss: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if body["dismiss_family"] != true {
		t.Errorf("dismiss_family missing, body: %s", raw)
	}
}

func TestBulkDismissOmitsFamilyWhenFalse(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("POST", bulkDismissPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	if _, err := c.BulkDismiss("test-token", 8, BulkSelector{IDs: []string{sugA}}, false); err != nil {
		t.Fatalf("BulkDismiss: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if _, present := body["dismiss_family"]; present {
		t.Errorf("dismiss_family must be omitted when false, body: %s", raw)
	}
}

// TestBulkRejectsMalformedIDBeforeSending proves the rejection is client-side.
// Asserting only err != nil would not: no handler fires on a 404 either, so
// such a test passes identically whether the id is refused here or round-trips
// to a server that rejects it. The `called` flag is what makes it a real test.
func TestBulkRejectsMalformedIDBeforeSending(t *testing.T) {
	cases := []struct {
		name string
		path string
		call func(*Client, BulkSelector) error
	}{
		{"BulkAccept", bulkAcceptPath, func(c *Client, sel BulkSelector) error {
			_, err := c.BulkAccept("test-token", 8, sel)
			return err
		}},
		{"BulkDismiss", bulkDismissPath, func(c *Client, sel BulkSelector) error {
			_, err := c.BulkDismiss("test-token", 8, sel, false)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock, c := newTestClient(t)
			called := false
			mock.RegisterHandler("POST", tc.path, func(w http.ResponseWriter, _ *http.Request) {
				called = true
				testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
			})

			// The good id is first, so this also pins that one bad entry
			// anywhere in the list aborts the whole call.
			err := tc.call(c, BulkSelector{IDs: []string{sugA, "not-a-uuid"}})
			if err == nil {
				t.Fatal("expected an error for a malformed UUID")
			}
			if called {
				t.Error("a request reached the server; a malformed id must be refused client-side")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestBulkDismissSendsFilterWithoutIDs covers the dismiss side of a filter-only
// selection, including that dismiss_family combines with it — the backend
// dismisses a matched revalidation candidate "including when the selection
// comes from a filter rather than explicit suggestion_ids".
func TestBulkDismissSendsFilterWithoutIDs(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("POST", bulkDismissPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Processed: 2, Errors: []string{}})
	})

	src := api.User
	act := api.SuggestionActionDelete
	if _, err := c.BulkDismiss("test-token", 8, BulkSelector{Source: &src, Action: &act}, true); err != nil {
		t.Fatalf("BulkDismiss: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	if body["source"] != "user" {
		t.Errorf("source = %v, want user; body: %s", body["source"], raw)
	}
	if body["action"] != "delete" {
		t.Errorf("action = %v, want delete; body: %s", body["action"], raw)
	}
	if body["dismiss_family"] != true {
		t.Errorf("dismiss_family = %v, want true; body: %s", body["dismiss_family"], raw)
	}
	if v, present := body["suggestion_ids"]; present && v != nil {
		t.Errorf("suggestion_ids must be null when selecting by filter, body: %s", raw)
	}
}

// TestBulkAcceptPassesThroughATotalFailure makes BulkResult's doc claim
// executable: the route answers 200 even when every row failed, so this must
// not surface as an error and all three counters must arrive intact — the
// body is the caller's only signal.
func TestBulkAcceptPassesThroughATotalFailure(t *testing.T) {
	mock, c := newTestClient(t)
	wantErr := sugA + ": boom"
	mock.RegisterHandler("POST", bulkAcceptPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{
			Processed: 0, Skipped: 1, Errors: []string{wantErr},
		})
	})

	got, err := c.BulkAccept("test-token", 8, BulkSelector{IDs: []string{sugA}})
	if err != nil {
		t.Fatalf("BulkAccept: %v", err)
	}
	if got.Processed != 0 {
		t.Errorf("Processed = %d, want 0", got.Processed)
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
	if len(got.Errors) != 1 || got.Errors[0] != wantErr {
		t.Errorf("Errors = %v, want exactly [%q]", got.Errors, wantErr)
	}
}

const patchPathA = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/" + sugA

func TestPatchSendsOnlyGivenFields(t *testing.T) {
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("PATCH", patchPathA, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": sugA, "action": "update", "state": "pending", "source": "user",
			"dataset_id": 8, "instance_name": "dedupe",
			"organization_id":     testOrgID,
			"rule_family_id":      "ffff0000-1111-2222-3333-444444444444",
			"created_at":          "2026-07-01T10:00:00Z",
			"rule_column_mapping": map[string]string{},
		})
	})

	severity := api.RuleSeverityBlocker
	if _, err := c.Patch("test-token", 8, sugA, PatchFields{Severity: &severity}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, raw)
	}
	// The enum WORD is what goes on the wire, not a boolean: severity is a
	// RuleSeverity on PatchSuggestionRequest, and the backend types the column
	// as that enum with no lenient _missing_.
	if body["severity"] != string(api.RuleSeverityBlocker) {
		t.Errorf("severity = %v, want %q; body: %s", body["severity"], api.RuleSeverityBlocker, raw)
	}
	// params and rule_column_mapping have no omitempty in the generated struct,
	// so they serialise as explicit nulls. The backend's `is not None` guards
	// skip them, which is what "only the flags the user typed are written"
	// depends on.
	if v, present := body["params"]; present && v != nil {
		t.Errorf("params must be null when not given, body: %s", raw)
	}
	if v, present := body["rule_column_mapping"]; present && v != nil {
		t.Errorf("rule_column_mapping must be null when not given, body: %s", raw)
	}
}

func TestPatchSurfaces409(t *testing.T) {
	mock, c := newTestClient(t)
	mock.RegisterHandler("PATCH", patchPathA, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict,
			map[string]interface{}{"detail": map[string]interface{}{"msg": "Can only update pending suggestions"}})
	})

	severity := api.RuleSeverityBlocker
	_, err := c.Patch("test-token", 8, sugA, PatchFields{Severity: &severity})
	if err == nil {
		t.Fatal("expected an error for a 409")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (409 is a non-404 4xx)", got)
	}
}

func TestGetPreservesParamNumbers(t *testing.T) {
	mock, c := newTestClient(t)
	huge := strings.Repeat("9", 400)
	mock.RegisterHandler("GET", patchPathA, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": sugA, "action": "update", "state": "pending", "source": "user",
			"dataset_id": 8, "instance_name": "dedupe", "severity": "warning",
			"organization_id":     testOrgID,
			"rule_family_id":      "ffff0000-1111-2222-3333-444444444444",
			"created_at":          "2026-07-01T10:00:00Z",
			"rule_column_mapping": map[string]string{},
			"params": map[string]interface{}{
				"big":     json.Number("9007199254740993"),
				"decimal": json.Number("1.20"),
				"huge":    json.Number(huge),
				"nested":  []interface{}{json.Number("9007199254740995")},
			},
		})
	})

	got, err := c.Get("test-token", 8, sugA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Params == nil {
		t.Fatal("Params is nil")
	}
	for key, want := range map[string]string{"big": "9007199254740993", "decimal": "1.20", "huge": huge} {
		n, ok := (*got.Params)[key].(json.Number)
		if !ok || n.String() != want {
			t.Errorf("params[%q] = %#v (%T), want json.Number(%q)", key, (*got.Params)[key], (*got.Params)[key], want)
		}
	}
	nested, ok := (*got.Params)["nested"].([]interface{})
	if !ok || len(nested) != 1 {
		t.Fatalf("nested = %#v, want one-element array", (*got.Params)["nested"])
	}
	if n, ok := nested[0].(json.Number); !ok || n.String() != "9007199254740995" {
		t.Errorf("nested[0] = %#v (%T), want lossless json.Number", nested[0], nested[0])
	}
}

// TestMutationsPreserveParamNumbers covers the suggestion each mutation
// returns: a float64 round trip prints 12345678901234567890 as
// 12345678901234567000 and 1.20 as 1.2, and the generated decoder rejects a
// 200 carrying 1e400 outright.
func TestMutationsPreserveParamNumbers(t *testing.T) {
	calls := []struct {
		name, method, path string
		call               func(*Client) (*Suggestion, error)
	}{
		{"accept", "POST", patchPathA + "/accept", func(c *Client) (*Suggestion, error) {
			return c.Accept("test-token", 8, sugA)
		}},
		{"patch", "PATCH", patchPathA, func(c *Client) (*Suggestion, error) {
			return c.Patch("test-token", 8, sugA, PatchFields{})
		}},
		{"dismiss", "POST", patchPathA + "/dismiss", func(c *Client) (*Suggestion, error) {
			return c.Dismiss("test-token", 8, sugA, false)
		}},
	}
	for _, tc := range calls {
		for _, want := range []string{"12345678901234567890", "1.20", "1e400"} {
			t.Run(tc.name+"/"+want, func(t *testing.T) {
				mock, c := newTestClient(t)
				mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
						"id": sugA, "action": "update", "state": "pending", "source": "user",
						"dataset_id": 8, "instance_name": "dedupe", "severity": "warning",
						"organization_id":     testOrgID,
						"rule_family_id":      "ffff0000-1111-2222-3333-444444444444",
						"created_at":          "2026-07-01T10:00:00Z",
						"rule_column_mapping": map[string]string{},
						"params":              map[string]interface{}{"n": json.Number(want)},
					})
				})

				got, err := tc.call(c)
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				if got.Params == nil {
					t.Fatal("Params is nil")
				}
				if n, ok := (*got.Params)["n"].(json.Number); !ok || n.String() != want {
					t.Errorf("params[\"n\"] = %#v (%T), want json.Number(%q)", (*got.Params)["n"], (*got.Params)["n"], want)
				}
			})
		}
	}
}

// sendPatch sends f through Patch to a server that answers 200 and returns the
// request body the server received.
func sendPatch(t *testing.T, f PatchFields) []byte {
	t.Helper()
	mock, c := newTestClient(t)
	var raw []byte
	mock.RegisterHandler("PATCH", patchPathA, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": sugA, "action": "update", "state": "pending", "source": "user",
			"dataset_id": 8, "instance_name": "dedupe",
			"organization_id":     testOrgID,
			"rule_family_id":      "ffff0000-1111-2222-3333-444444444444",
			"created_at":          "2026-07-01T10:00:00Z",
			"rule_column_mapping": map[string]string{},
		})
	})
	if _, err := c.Patch("test-token", 8, sugA, f); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	return raw
}

// patchBodyFields splits a request body into its top-level fields, so a test
// can tell an absent key from one sent as null.
func patchBodyFields(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("body not a JSON object: %v (%s)", err, raw)
	}
	return fields
}

func euRowsFilter() *row_filters.Filter {
	description := "EU rows only"
	value := "EU"
	return &row_filters.Filter{
		Description: &description,
		Conditions: []row_filters.Condition{
			{ColumnName: "region", Operator: api.Eq, Value: &value, CaseSensitive: true},
		},
		Predicate: &row_filters.Predicate{
			Source:  "def applies(row):\n    return row[\"amount\"] > 0\n",
			Columns: []string{"amount"},
		},
	}
}

// TestPatchKeepLeavesRowFilterOutOfTheBody pins that a caller who does not
// touch RowFilter sends exactly the body the generated PatchSuggestionWithResponse
// sent before the field existed.
func TestPatchKeepLeavesRowFilterOutOfTheBody(t *testing.T) {
	severity := api.RuleSeverityBlocker
	raw := sendPatch(t, PatchFields{Severity: &severity})

	fields := patchBodyFields(t, raw)
	if v, present := fields["row_filter"]; present {
		t.Errorf("row_filter must be absent for Keep, got %s; body: %s", v, raw)
	}
	if got := string(fields["severity"]); got != `"blocker"` {
		t.Errorf("severity = %s, want \"blocker\"; body: %s", got, raw)
	}
	for _, key := range []string{"params", "rule_column_mapping"} {
		if v, present := fields[key]; !present || string(v) != "null" {
			t.Errorf("%s must be an explicit null when not given; body: %s", key, raw)
		}
	}

	want, err := json.Marshal(api.PatchSuggestionJSONRequestBody{Severity: &severity})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(want) {
		t.Errorf("body = %s, want the generated client's %s", raw, want)
	}
}

func TestPatchRemoveSendsNullRowFilter(t *testing.T) {
	raw := sendPatch(t, PatchFields{RowFilter: row_filters.Remove()})

	fields := patchBodyFields(t, raw)
	v, present := fields["row_filter"]
	if !present {
		t.Fatalf("row_filter must be present for Remove; body: %s", raw)
	}
	if string(v) != "null" {
		t.Errorf("row_filter = %s, want null; body: %s", v, raw)
	}
	if _, present := fields["severity"]; present {
		t.Errorf("severity must stay omitted when not given; body: %s", raw)
	}
}

func TestPatchReplaceSendsTheFilter(t *testing.T) {
	raw := sendPatch(t, PatchFields{RowFilter: row_filters.Replace(euRowsFilter())})

	fields := patchBodyFields(t, raw)
	v, present := fields["row_filter"]
	if !present {
		t.Fatalf("row_filter must be present for Replace; body: %s", raw)
	}
	var got api.DescribedRowFilter
	if err := json.Unmarshal(v, &got); err != nil {
		t.Fatalf("row_filter not a DescribedRowFilter: %v (%s)", err, v)
	}
	value := "EU"
	caseSensitive := true
	want := api.DescribedRowFilter{
		Description: "EU rows only",
		Conditions: &[]api.RowFilterCondition{
			{ColumnName: "region", Operator: api.Eq, Value: &value, CaseSensitive: &caseSensitive},
		},
		Predicate: &api.RowFilterPredicate{
			Source:  "def applies(row):\n    return row[\"amount\"] > 0\n",
			Columns: []string{"amount"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("row_filter = %s, want %+v", v, want)
	}
}

// TestPatchSendsParamNumbersVerbatim covers both encodings patchBody can take:
// a float64 round trip would print 12345678901234567890 as
// 12345678901234567000 and 1.20 as 1.2.
func TestPatchSendsParamNumbersVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit row_filters.Edit
	}{
		{"keep", row_filters.Keep()},
		{"replace", row_filters.Replace(euRowsFilter())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]interface{}{
				"big":     json.Number("12345678901234567890"),
				"decimal": json.Number("1.20"),
			}
			raw := sendPatch(t, PatchFields{Params: &params, RowFilter: tc.edit})

			var got map[string]json.RawMessage
			if err := json.Unmarshal(patchBodyFields(t, raw)["params"], &got); err != nil {
				t.Fatalf("params not a JSON object: %v; body: %s", err, raw)
			}
			for key, want := range map[string]string{"big": "12345678901234567890", "decimal": "1.20"} {
				if string(got[key]) != want {
					t.Errorf("params[%q] = %s, want %s; body: %s", key, got[key], want, raw)
				}
			}
		})
	}
}

func TestPatchRowFilter422ExitsTwo(t *testing.T) {
	mock, c := newTestClient(t)
	mock.RegisterHandler("PATCH", patchPathA, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "Row filter invalid: unknown column 'regoin'",
				"error_code": "ERR_VALIDATION",
				"loc":        []string{"body", "row_filter"},
			},
		})
	})

	_, err := c.Patch("test-token", 8, sugA, PatchFields{RowFilter: row_filters.Replace(euRowsFilter())})
	if err == nil {
		t.Fatal("expected an error for a 422")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (422 is a non-404 4xx)", got)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want an *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "ERR_VALIDATION" {
		t.Errorf("APIError = {StatusCode: %d, Code: %q}, want {422, \"ERR_VALIDATION\"}", apiErr.StatusCode, apiErr.Code)
	}
}

func TestPatchWrapsAnEncodeFailure(t *testing.T) {
	mock, c := newTestClient(t)
	called := false
	mock.RegisterHandler("PATCH", patchPathA, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{})
	})

	params := map[string]interface{}{"bad": func() {}}
	_, err := c.Patch("test-token", 8, sugA, PatchFields{Params: &params})
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Errorf("err = %v, want it to wrap a *json.UnsupportedTypeError", err)
	}
	if called {
		t.Error("a request reached the server after the body failed to encode")
	}
}
