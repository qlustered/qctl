package datasets

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// getDataParamsField looks a field up on api.GetDataParams by Go name.
func getDataParamsField(t *testing.T, name string) (reflect.Type, bool) {
	t.Helper()
	f, ok := reflect.TypeOf(api.GetDataParams{}).FieldByName(name)
	if !ok {
		return nil, false
	}
	return f.Type, true
}

// The whole point of WithAlertItemID is that api.GetDataParams.AlertItemID
// cannot carry the value, so the assertion has to be on the WIRE: the query
// string the backend actually receives.
func TestGetDataSendsAlertItemIDOnTheWire(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"dataset_name":"t","fields":[],"labels":[],` +
			`"field_types":[],"results":[],"total_page_count":1,"total_rows":0}`))
	}))
	defer srv.Close()

	isBad := true
	page := 1
	if _, err := NewClient(srv.URL, testOrgID, 0).GetData(
		"tok", 42, &api.GetDataParams{IsBadData: &isBad, Page: &page}, WithAlertItemID(8817),
	); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got.Get("alert_item_id") != "8817" {
		t.Errorf("alert_item_id = %q, want %q", got.Get("alert_item_id"), "8817")
	}
	// The editor rewrites RawQuery wholesale, so the parameters the generated
	// builder already put there must survive it.
	if got.Get("is_bad_data") != "true" || got.Get("page") != "1" {
		t.Errorf("editor dropped generated params: %v", got)
	}
}

// No option means no parameter -- not alert_item_id=0, which the backend would
// read as a real (and missing) alert id and answer 404 for.
func TestGetDataOmitsAlertItemIDWhenUnset(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"dataset_name":"t","fields":[],"labels":[],` +
			`"field_types":[],"results":[],"total_page_count":1,"total_rows":0}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, testOrgID, 0).GetData("tok", 42, &api.GetDataParams{}); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if strings.Contains(raw, "alert_item_id") {
		t.Errorf("alert_item_id sent with no option set, query = %q", raw)
	}
}

func TestPutDataForDownloadSendsAlertItemIDOnTheWire(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer srv.Close()

	isBad := true
	if _, err := NewClient(srv.URL, dlOrgID, 0).PutDataForDownload(
		"tok", 42, &api.PutDataForDownloadParams{IsBadData: &isBad}, WithAlertItemID(8817),
	); err != nil {
		t.Fatalf("PutDataForDownload: %v", err)
	}
	if got.Get("alert_item_id") != "8817" {
		t.Errorf("alert_item_id = %q, want %q", got.Get("alert_item_id"), "8817")
	}
	if got.Get("is_bad_data") != "true" {
		t.Errorf("editor dropped is_bad_data: %v", got)
	}
}

// A guard on the premise this whole file rests on. If a future
// `make generate-client` turns AlertItemID into a plain *int, the union is
// gone, the request editor is no longer the only way to send the parameter,
// and WithAlertItemID should be retired in favour of the generated field.
func TestAlertItemIDIsStillAnUnusableUnion(t *testing.T) {
	field, ok := getDataParamsField(t, "AlertItemID")
	if !ok {
		t.Fatal("api.GetDataParams has no AlertItemID field; the spec moved -- re-derive alert_item_filter.go")
	}
	if field.Kind().String() != "ptr" || field.Elem().Kind().String() != "struct" {
		t.Fatalf("AlertItemID is now %s, not the inline union this file works around; "+
			"send it through api.GetDataParams and delete WithAlertItemID", field)
	}
	if n := field.Elem().NumField(); n != 1 || field.Elem().Field(0).IsExported() {
		t.Fatalf("AlertItemID's union struct changed shape (%d fields, exported=%v); re-derive alert_item_filter.go",
			n, field.Elem().Field(0).IsExported())
	}
}
