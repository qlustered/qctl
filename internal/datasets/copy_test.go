package datasets

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const datasetCopyPath = "/api/orgs/" + testOrgID + "/datasets/42/copy"

func newCopyClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

// The GET body in the backend's shape (get_dataset_copy_options_async,
// dataset_copy_controller.py): options with default/always/count, and each
// data source with its distinguishing fields, a null value included.
const copyOptionsBody = `{
  "source_dataset_id": 42,
  "name": "Orders (2)",
  "table_name": "orders_2",
  "destination_id": 3,
  "database_name": "analytics",
  "schema_name": "public",
  "options": [
    {"option": "settings", "count": 1, "default": true, "always": true},
    {"option": "rules", "count": 7, "default": true, "always": false}
  ],
  "data_sources": [
    {"id": 11, "name": "orders-s3", "data_source_type": "s3", "state": "active",
     "schedule": "0 * * * *", "location_field": "bucket_name", "location": "acme-bucket",
     "distinguishing_fields": {"s3_prefix": "in/", "pattern": null}}
  ]
}`

func TestCopyOptions_DecodesOptionsAndDataSources(t *testing.T) {
	c, mock := newCopyClient(t)
	recordJSON(t, mock, "GET", datasetCopyPath, http.StatusOK, copyOptionsBody)

	got, body, err := c.CopyOptions("tok", 42, nil)
	if err != nil {
		t.Fatalf("CopyOptions: %v", err)
	}
	var gotBody, wantBody any
	if err := json.Unmarshal(body, &gotBody); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	if err := json.Unmarshal([]byte(copyOptionsBody), &wantBody); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotBody, wantBody) {
		t.Errorf("body = %s, want the response as received", body)
	}
	if got.Name != "Orders (2)" || got.TableName != "orders_2" {
		t.Errorf("suggested name/table_name = %q/%q, want Orders (2)/orders_2", got.Name, got.TableName)
	}
	if got.SourceDatasetID != 42 || got.DestinationID != 3 ||
		got.DatabaseName != "analytics" || got.SchemaName != "public" {
		t.Errorf("header = %+v", got)
	}
	wantOptions := []api.DatasetCopyOptionInfo{
		{Option: api.DatasetCopyOptionSettings, Count: 1, Default: true, Always: true},
		{Option: api.DatasetCopyOptionRules, Count: 7, Default: true, Always: false},
	}
	if !reflect.DeepEqual(got.Options, wantOptions) {
		t.Errorf("Options = %+v, want %+v", got.Options, wantOptions)
	}
	if len(got.DataSources) != 1 {
		t.Fatalf("DataSources = %+v, want one entry", got.DataSources)
	}
	ds := got.DataSources[0]
	if ds.ID != 11 || ds.Name != "orders-s3" || ds.DataSourceType != "s3" || ds.State != "active" {
		t.Errorf("data source = %+v", ds)
	}
	if ds.LocationField == nil || *ds.LocationField != "bucket_name" || ds.Location == nil || *ds.Location != "acme-bucket" {
		t.Errorf("location_field/location = %v/%v, want bucket_name/acme-bucket", ds.LocationField, ds.Location)
	}
	if prefix := ds.DistinguishingFields["s3_prefix"]; prefix == nil || *prefix != "in/" {
		t.Errorf("distinguishing s3_prefix = %v, want in/", prefix)
	}
	pattern, present := ds.DistinguishingFields["pattern"]
	if !present || pattern != nil {
		t.Errorf("distinguishing pattern = %v (present %v), want a present null", pattern, present)
	}
}

// The POST body the backend received is the request exactly as given, and the
// response keeps a whole number under an interface{} as an int64 rather than
// the float64 a plain decode makes of it.
func TestCopy_SendsTheRequestAndDecodesTheCopyRecord(t *testing.T) {
	c, mock := newCopyClient(t)
	got := recordJSON(t, mock, "POST", datasetCopyPath, http.StatusOK, `{
	  "id": 99, "name": "Orders B", "table_name": "orders_b", "state": "active",
	  "settings_model": {"default_value_for_field_when_casting_error": {"amount": 1234567}},
	  "dataset_copy": {
	    "id": 5, "kind": "new_dataset", "state": "applied",
	    "source_dataset_id": 42, "target_dataset_id": 99,
	    "created_at": "2026-09-25T10:00:00Z",
	    "options": {"include": ["rules", "settings"]},
	    "items": [
	      {"id": 1, "item_type": "settings", "action": "created",
	       "source_object_id": "42", "target_object_id": "99", "note": null},
	      {"id": 2, "item_type": "data_source", "action": "created",
	       "source_object_id": "11", "target_object_id": "12", "note": "Copied as draft."}
	    ]
	  }
	}`)

	include := []api.DatasetCopyOption{api.DatasetCopyOptionSettings, api.DatasetCopyOptionRules}
	prefix := "in-b/"
	state := api.DatasetCopyDataSourceRequestStateDraft
	res, _, err := c.Copy("tok", 42, CopyRequest{
		Name:      stringPtr("Orders B"),
		TableName: stringPtr("orders_b"),
		Include:   &include,
		DataSources: &[]api.DatasetCopyDataSourceRequest{
			{SourceID: 11, S3Prefix: &prefix, State: &state},
		},
	})
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", got.body, err)
	}
	if body["name"] != "Orders B" || body["table_name"] != "orders_b" {
		t.Errorf("name/table_name = %v/%v, want Orders B/orders_b", body["name"], body["table_name"])
	}
	if want := []any{"settings", "rules"}; !reflect.DeepEqual(body["include"], want) {
		t.Errorf("include = %v, want %v", body["include"], want)
	}
	sources, ok := body["data_sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("data_sources = %v, want one entry", body["data_sources"])
	}
	entry, ok := sources[0].(map[string]any)
	if !ok {
		t.Fatalf("data_sources[0] = %v, want an object", sources[0])
	}
	wantSet := map[string]any{"source_id": float64(11), "s3_prefix": "in-b/", "state": "draft"}
	for key, want := range wantSet {
		if entry[key] != want {
			t.Errorf("data_sources[0].%s = %v, want %v", key, entry[key], want)
		}
	}
	for _, key := range []string{"name", "schedule", "pattern", "archive_pattern", "sftp_folder", "gs_prefix", "excel_sheet_name"} {
		value, present := entry[key]
		if !present || value != nil {
			t.Errorf("data_sources[0].%s = %v (present %v), want a present null", key, value, present)
		}
	}

	if res.ID != 99 || res.Name != "Orders B" {
		t.Errorf("new dataset = %d %q, want 99 Orders B", res.ID, res.Name)
	}
	record := res.DatasetCopy
	if record.ID != 5 || record.Kind != api.NewDataset || record.State != api.DatasetCopyStateApplied ||
		record.SourceDatasetID != 42 || record.TargetDatasetID != 99 {
		t.Errorf("dataset_copy = %+v", record)
	}
	if want := []any{"rules", "settings"}; !reflect.DeepEqual(record.Options["include"], want) {
		t.Errorf("dataset_copy.options.include = %v, want %v", record.Options["include"], want)
	}
	if len(record.Items) != 2 {
		t.Fatalf("dataset_copy.items = %+v, want two", record.Items)
	}
	item := record.Items[1]
	if item.ItemType != api.DatasetCopyItemTypeDataSource || item.Action != api.DatasetCopyItemActionCreated ||
		item.SourceObjectID == nil || *item.SourceObjectID != "11" ||
		item.TargetObjectID == nil || *item.TargetObjectID != "12" ||
		item.Note == nil || *item.Note != "Copied as draft." {
		t.Errorf("dataset_copy.items[1] = %+v", item)
	}
	if record.Items[0].Note != nil {
		t.Errorf("dataset_copy.items[0].note = %v, want nil", *record.Items[0].Note)
	}
	amount := res.SettingsModel.DefaultValueForFieldWhenCastingError["amount"]
	if amount != int64(1234567) {
		t.Errorf("settings_model default amount = %#v (%T), want int64(1234567)", amount, amount)
	}
}

func TestCopy_NameTaken409ExitsTwoWithTheServerMessage(t *testing.T) {
	c, mock := newCopyClient(t)
	recordJSON(t, mock, "POST", datasetCopyPath, http.StatusConflict,
		`{"detail":{"msg":"The name X is already used in this organization.","loc":["body","name"],"suggestions":["X (2)"]}}`)

	_, _, err := c.Copy("tok", 42, CopyRequest{Name: stringPtr("X"), TableName: stringPtr("x")})
	assertServerMessage(t, err, "The name X is already used in this organization.", qerrors.ExitBadUsage)
	assertAPIError(t, err, http.StatusConflict)
	if !strings.Contains(err.Error(), "X (2)") {
		t.Errorf("error %q does not carry the server's suggestion", err.Error())
	}
}

func TestCopy_SameFiles400ExitsTwoWithTheServerMessage(t *testing.T) {
	c, mock := newCopyClient(t)
	recordJSON(t, mock, "POST", datasetCopyPath, http.StatusBadRequest,
		`{"detail":{"msg":"Another active source already reads bucket_name acme-bucket with the same s3_prefix and pattern. `+
			`Change at least one of them so this source does not read the same files.",`+
			`"loc":["body","data_sources","0","s3_prefix"]}}`)

	_, _, err := c.Copy("tok", 42, CopyRequest{
		Name: stringPtr("X"), TableName: stringPtr("x"),
		DataSources: &[]api.DatasetCopyDataSourceRequest{{SourceID: 11}},
	})
	assertServerMessage(t, err, "does not read the same files", qerrors.ExitBadUsage)
	assertAPIError(t, err, http.StatusBadRequest)
	if !strings.Contains(err.Error(), "Field: data_sources.0.s3_prefix") {
		t.Errorf("error %q does not identify the failed data-source entry", err.Error())
	}
}

// statusExitCases includes an object-shaped 422, which the generated parser
// cannot decode, so that case goes through wrapAPIError rather than
// handleError.
func TestCopyOptions_StatusCodesMapToExitCodes(t *testing.T) {
	for _, tc := range statusExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newCopyClient(t)
			recordJSON(t, mock, "GET", datasetCopyPath, tc.status, tc.body)
			_, _, err := c.CopyOptions("tok", 42, nil)
			assertExitCode(t, err, tc.exit)
			assertAPIError(t, err, tc.status)
		})
	}
}

func TestCopy_StatusCodesMapToExitCodes(t *testing.T) {
	for _, tc := range statusExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newCopyClient(t)
			recordJSON(t, mock, "POST", datasetCopyPath, tc.status, tc.body)
			_, _, err := c.Copy("tok", 42, CopyRequest{Name: stringPtr("X"), TableName: stringPtr("x")})
			assertExitCode(t, err, tc.exit)
			assertAPIError(t, err, tc.status)
		})
	}
}

func TestCopyOptions_QueryCarriesTheSeriesOnlyWhenGiven(t *testing.T) {
	seriesID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	for _, tc := range []struct {
		name   string
		series *api.DatasetCopySeriesRequest
		want   url.Values
	}{
		{name: "no series", want: url.Values{}},
		{
			name:   "series",
			series: &api.DatasetCopySeriesRequest{SeriesID: seriesID, PeriodKey: "2026-Q4"},
			want:   url.Values{"series_id": {seriesID.String()}, "period_key": {"2026-Q4"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newCopyClient(t)
			got := recordJSON(t, mock, "GET", datasetCopyPath, http.StatusOK, copyOptionsBody)
			if _, _, err := c.CopyOptions("tok", 42, tc.series); err != nil {
				t.Fatalf("CopyOptions: %v", err)
			}
			query, err := url.ParseQuery(got.query)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(query, tc.want) {
				t.Errorf("query = %v, want %v", query, tc.want)
			}
		})
	}
}
