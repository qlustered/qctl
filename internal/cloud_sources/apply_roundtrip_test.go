package cloud_sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	// rtID and rtVersion are the id and version_id of every rtLiveBody source.
	rtID      = 4242
	rtVersion = 7
	// rtUnknownKey is a member the read carries that no schema declares.
	rtUnknownKey = "last_polled_at"
	// rtPlainAccessKey is the s3_access_key a read returns unmasked.
	rtPlainAccessKey = "AKIA<EXAMPLE>&7"
)

// rtOverrides is every *_override member of a rich read: several of each shape
// SettingsOverrides declares, empty and false values among them, and some null.
func rtOverrides() map[string]any {
	return map[string]any{
		"array_delimiters_override":                                    []string{";", "|"},
		"array_delimiters_per_column_override":                         map[string][]string{"tags": {",", " / "}},
		"boolean_false_override":                                       []string{"no", "nein", "<false>"},
		"boolean_false_per_column_override":                            map[string][]string{},
		"boolean_true_override":                                        []string{},
		"boolean_true_per_column_override":                             nil,
		"date_formats_override":                                        []string{"%d.%m.%Y"},
		"date_formats_per_column_override":                             nil,
		"datetime_allowed_characters_override":                         "",
		"datetime_formats_override":                                    nil,
		"datetime_formats_per_column_override":                         map[string][]string{"ts": {"%Y-%m-%dT%H:%M:%S%z"}},
		"field_name_full_conversion_override":                          map[string]string{"Straße": "street", "a<b>": "a&b"},
		"field_name_part_conversion_override":                          map[string]string{},
		"fields_to_expand_override":                                    []string{"payload"},
		"ignore_column_names_override":                                 nil,
		"ignore_lines_that_include_only_subset_of_characters_override": []string{"-", "="},
		"ignore_lines_that_include_only_subset_of_words_override":      nil,
		"ignore_matchers_override":                                     map[string][]string{"zip": {"IntMatcher"}},
		"infer_datetime_override":                                      false,
		"infer_datetime_for_columns_override":                          []string{},
		"null_values_override":                                         []string{"N/A", "null", "—", ""},
		"null_values_per_column_override":                              map[string][]string{"price": {"-", "n/a"}},
		"trim_string_instead_of_raising_err_override":                  false,
		"use_text_instead_of_string_override":                          nil,
		"value_mapping_override":                                       map[string]string{"carot": "carrot", "20B0": "2020", "": "empty"},
		"value_mapping_per_column_override":                            map[string]map[string]string{"country": {"DE": "Deutschland", "Ö": "Österreich"}},
		"xls_date_mode_override":                                       "mode_1",
	}
}

// rtLiveBody is a read of a cloud source of sourceType: every member the
// server sends for any type, and rtOverrides. Every encrypted credential reads
// as secretMask, except gpg_private_key, which was never set, and a qluster_ui
// source's gpg_passphrase, which reads as '•'. s3_access_key reads unmasked on
// an s3 or s3_compatible source and as "" otherwise.
func rtLiveBody(sourceType DataSourceType) map[string]any {
	body := map[string]any{
		"id":                      rtID,
		"version_id":              rtVersion,
		"state":                   "active",
		"internal_id":             "7f3e2a",
		"bad_rows_count":          3,
		"delta_applied_cursor":    118,
		"created_at":              "2026-09-01T10:11:12.123456Z",
		"period_delta_cursors":    []any{},
		"email_intake_address":    nil,
		"email_intake_enabled":    false,
		"auto_send_issues_report": false,

		"name":              "orders <eu> & co — 注文",
		"dataset_id":        11,
		"dataset_name":      "Orders",
		"settings_model_id": 12,
		"data_source_type":  string(sourceType),

		"redirect_post_submit_to":          "https://example.com/done?a=1&b=<2>",
		"excluded_row_signatures_override": []string{"9f2c"},
		"ingestion_jobs_list":              nil,
		rtUnknownKey:                       "2026-09-02T00:00:00Z",

		"archive_pattern":                 "",
		"pattern":                         `^orders_.*\.csv$`,
		"encoding_probably":               "utf-8",
		"excel_sheet_name":                "Übersicht <Q1> & Q2 — 売上",
		"chunk_size_to_read_for_header":   65536,
		"connection_timeout":              30,
		"s3_default_buffer_size":          8388608,
		"sftp_port":                       22,
		"delete_source_file_after_backup": false,
		"identify_header_by_column_names": []string{"Name", "Straße"},
		"ignore_column_ids":               []string{"1", "B"},
		"raw_headers":                     []string{},
		"csv_delimiter":                   "",
		"csv_quotechar":                   `"`,
		"csv_escapechar":                  nil,
		"schedule":                        "",
		"table_series_id":                 nil,
		"routing_mode":                    nil,
		"routing_pattern":                 nil,
		"routing_column":                  nil,
		"start_period_key":                nil,
		"end_period_key":                  nil,
		"broker_due_days":                 nil,
		"preload_row_drop_share":          0.2,
		"source_time_zone":                "UTC",

		"s3_bucket":       "",
		"s3_prefix":       "",
		"s3_region_name":  "",
		"s3_endpoint_url": "",
		"gs_bucket":       "",
		"gs_prefix":       "",
		"sftp_host":       "",
		"sftp_user":       "",
		"sftp_folder":     "",
		"dropbox_folder":  "",
		"simple_url":      "",

		"s3_access_key":           "",
		"s3_secret_key":           secretMask,
		"gs_service_account_key":  secretMask,
		"dropbox_access_token":    secretMask,
		"sftp_password":           secretMask,
		"sftp_ssh_key":            secretMask,
		"sftp_ssh_key_passphrase": secretMask,
		"file_password":           secretMask,
		"gpg_passphrase":          secretMask,
		"gpg_private_key":         "",
	}
	for key, value := range rtOverrides() {
		body[key] = value
	}
	switch sourceType {
	case DataSourceTypeS3:
		body["s3_bucket"] = "raw-orders"
		body["s3_prefix"] = "incoming/"
		body["s3_region_name"] = "eu-central-1"
		body["s3_access_key"] = rtPlainAccessKey
	case DataSourceTypeS3Compatible:
		body["s3_bucket"] = "lake"
		body["s3_endpoint_url"] = "https://minio.internal:9000"
		body["s3_region_name"] = "us-east-1"
		body["s3_access_key"] = rtPlainAccessKey
		body["schedule"] = "*/15 * * * *"
		body["csv_delimiter"] = ";"
		body["csv_quotechar"] = nil
		body["csv_escapechar"] = `\`
	case DataSourceTypeGs:
		body["gs_bucket"] = "gcs-orders"
		body["gs_prefix"] = "in/"
		body["schedule"] = "@hourly"
	case DataSourceTypeSimpleURL:
		body["simple_url"] = "https://example.com/export.csv?token=<abc>&x=ü"
		body["schedule"] = "0 3 * * 1-5"
	case DataSourceTypeQlusterUI:
		body["gpg_passphrase"] = "••••••••"
		body["email_intake_address"] = "orders@example.com"
		body["email_intake_enabled"] = true
		body["auto_send_issues_report"] = true
	case DataSourceTypeSftp:
		body["sftp_host"] = "sftp.example.com"
		body["sftp_user"] = "ingest"
		body["sftp_folder"] = "/out"
	case DataSourceTypeDropbox:
		body["dropbox_folder"] = "/Exports"
	}
	return body
}

// rtCase is one source whose unedited export must re-apply as no change.
type rtCase struct {
	name string
	body map[string]any
}

// rtCases is a rich source of every type whose export apply accepts, a series
// source routed by file name and one routed by column, and one with no
// override at all.
func rtCases() []rtCase {
	noOverrides := rtLiveBody(DataSourceTypeGs)
	for key := range rtOverrides() {
		noOverrides[key] = nil
	}
	series := rtLiveBody(DataSourceTypeS3)
	series["table_series_id"] = "5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90"
	series["routing_mode"] = "file_name"
	series["routing_pattern"] = "orders_{period}.csv"
	series["period_delta_cursors"] = []map[string]any{
		{"dataset_id": 11, "period_key": "2026-09", "settings_model_id": 12, "delta_applied_cursor": 118},
		{"dataset_id": 13, "period_key": "2026-10", "settings_model_id": 14, "delta_applied_cursor": nil},
	}
	seriesByColumn := rtLiveBody(DataSourceTypeGs)
	seriesByColumn["table_series_id"] = "0b6a2f4e-1c3d-4e5f-8a9b-7c6d5e4f3a21"
	seriesByColumn["routing_mode"] = "column"
	seriesByColumn["routing_column"] = "period"
	unusualScalars := rtLiveBody(DataSourceTypeS3)
	unusualScalars["pattern"] = "\n"
	unusualScalars["excel_sheet_name"] = "a\u0085b"
	unusualScalars["archive_pattern"] = "a\x7fb"
	unusualScalars["field_name_full_conversion_override"] = map[string]string{"<<": "literal key"}
	preciseShare := rtLiveBody(DataSourceTypeS3)
	preciseShare["preload_row_drop_share"] = 0.123456789
	nullShare := rtLiveBody(DataSourceTypeS3)
	nullShare["preload_row_drop_share"] = nil
	return []rtCase{
		{"precise row-drop share", preciseShare},
		{"null row-drop share", nullShare},
		{"s3", rtLiveBody(DataSourceTypeS3)},
		{"series", series},
		{"series by column", seriesByColumn},
		{"unusual scalars", unusualScalars},
		{"s3_compatible", rtLiveBody(DataSourceTypeS3Compatible)},
		{"sftp", rtLiveBody(DataSourceTypeSftp)},
		{"gs", rtLiveBody(DataSourceTypeGs)},
		{"gs without overrides", noOverrides},
		{"simple_url", rtLiveBody(DataSourceTypeSimpleURL)},
		{"dropbox", rtLiveBody(DataSourceTypeDropbox)},
		{"qluster_ui", rtLiveBody(DataSourceTypeQlusterUI)},
	}
}

// rtEncodeBody encodes body as the server does, without HTML escaping.
func rtEncodeBody(t *testing.T, body map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		t.Fatalf("encode the read: %v", err)
	}
	return buf.Bytes()
}

// rtServe answers the by-name lookup and the read of rtID with body, verbatim.
func rtServe(t *testing.T, body []byte) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	var live struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &live); err != nil {
		t.Fatalf("decode the read's name: %v", err)
	}
	c, mock := newCSClient(t)
	mock.RegisterHandler(http.MethodGet, rtListPath(), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results":    []map[string]any{{"id": rtID, "name": live.Name}},
			"page":       1,
			"total_rows": 1,
		})
	})
	mock.RegisterHandler(http.MethodGet, rtReadPath(), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	return c, mock
}

func rtListPath() string { return "/api/orgs/" + csOrgID + "/data-sources" }

func rtReadPath() string { return fmt.Sprintf("/api/orgs/%s/data-sources/%d", csOrgID, rtID) }

// rtExport is the manifest `get cloud-source <id> -o yaml|json` prints for
// the source c serves.
func rtExport(t *testing.T, c *Client) *CloudSourceManifest {
	t.Helper()
	full, err := c.GetCloudSource("tok", rtID)
	if err != nil {
		t.Fatalf("GetCloudSource: %v", err)
	}
	m, err := APIResponseToManifest(full)
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}
	return m
}

// rtFormats are the encodings an export is written in: `get cloud-source -o
// yaml` and `-o json` exactly as encodeStructuredTo writes them, and the
// shared YAML marshaler.
var rtFormats = []struct {
	name   string
	encode func(v any) ([]byte, error)
}{
	{"yaml", func(v any) ([]byte, error) {
		var buf bytes.Buffer
		enc := jsonnum.NewYAMLEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}},
	{"json", func(v any) ([]byte, error) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}},
	{"MarshalYAML", jsonnum.MarshalYAML},
}

// rtEncode writes m in the named rtFormats encoding.
func rtEncode(t *testing.T, format string, m *CloudSourceManifest) []byte {
	t.Helper()
	for _, f := range rtFormats {
		if f.name == format {
			text, err := f.encode(m)
			if err != nil {
				t.Fatalf("encode the export as %s: %v", format, err)
			}
			return text
		}
	}
	t.Fatalf("no format %q", format)
	return nil
}

// rtParse reads text as `apply -f` reads a file holding one CloudSource.
func rtParse(t *testing.T, text []byte) *CloudSourceManifest {
	t.Helper()
	docs, err := pkgmanifest.SplitDocuments(text)
	if err != nil || len(docs) != 1 {
		t.Fatalf("SplitDocuments = %d documents, %v; want 1\n%s", len(docs), err, text)
	}
	if _, err := pkgmanifest.LoadBytes(docs[0]); err != nil {
		t.Fatalf("LoadBytes refuses the export: %v\n%s", err, text)
	}
	var m CloudSourceManifest
	if err := pkgmanifest.StrictUnmarshal(docs[0], &m); err != nil {
		t.Fatalf("StrictUnmarshal refuses the export: %v\n%s", err, text)
	}
	return &m
}

// rtCheckFixture fails unless body carries every member of a read, each
// under a key the read declares.
func rtCheckFixture(t *testing.T, body map[string]any) {
	t.Helper()
	declared := map[string]bool{}
	for _, key := range jsonKeys(reflect.TypeOf(CloudSourceFull{})) {
		declared[key] = true
		if _, ok := body[key]; !ok {
			t.Fatalf("fixture: the read carries no %s; add it to rtLiveBody", key)
		}
	}
	for key := range body {
		if !declared[key] && key != rtUnknownKey {
			t.Fatalf("fixture: %s is not a member of a read", key)
		}
	}
}

// rtCheckOverrideShapes fails unless rtOverrides sets an override of every
// shape SettingsOverrides declares.
func rtCheckOverrideShapes(t *testing.T) {
	t.Helper()
	set := rtOverrides()
	shapes := map[reflect.Type]bool{}
	overrides := reflect.TypeOf(SettingsOverrides{})
	for i := range overrides.NumField() {
		shapes[overrides.Field(i).Type] = false
	}
	for i, name := range overrideNames {
		if set[name+overrideSuffix] != nil {
			shapes[overrides.Field(i).Type] = true
		}
	}
	for shape, covered := range shapes {
		if !covered {
			t.Fatalf("fixture: no override of shape %s is set; add one to rtOverrides", shape)
		}
	}
}

// Re-applying an unedited export changes nothing, in every encoding, and
// sends no request.
func TestReappliedExportChangesNothing(t *testing.T) {
	rtCheckOverrideShapes(t)
	for _, tc := range rtCases() {
		rtCheckFixture(t, tc.body)
		body := rtEncodeBody(t, tc.body)
		for _, f := range rtFormats {
			t.Run(tc.name+"/"+f.name, func(t *testing.T) {
				c, mock := rtServe(t, body)
				text := rtEncode(t, f.name, rtExport(t, c))
				m := rtParse(t, text)
				spec, err := json.Marshal(m.Spec)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(spec, []byte("period_delta_cursors")) {
					t.Errorf("the spec carries the response-only period_delta_cursors:\n%s", text)
				}

				before := len(mock.Requests())
				plan, err := c.PrepareApply("tok", m)
				if err != nil {
					t.Fatalf("PrepareApply: %v\n%s", err, text)
				}
				if plan.IsCreate() {
					t.Fatalf("PrepareApply planned a create against the source it exported\n%s", text)
				}
				want := []string{http.MethodGet + " " + rtListPath(), http.MethodGet + " " + rtReadPath()}
				if got := mock.Requests()[before:]; !reflect.DeepEqual(got, want) {
					t.Errorf("PrepareApply requests = %v, want %v", got, want)
				}
				if changes := plan.Changes(); len(changes) != 0 {
					t.Errorf("the unedited export plans %d changes, want 0:\n%s\nexport:\n%s",
						len(changes), FormatChanges(changes, "  "), text)
				}
				if exported, live, ok := plan.StaleExport(); ok {
					t.Errorf("StaleExport = %d, %d on the version it was exported at", exported, live)
				}
				if effects := plan.SideEffects(); len(effects) != 0 {
					t.Errorf("SideEffects = %q, want none", effects)
				}

				before = len(mock.Requests())
				result, err := c.ApplyPrepared("tok", plan)
				if err != nil {
					t.Fatalf("ApplyPrepared: %v", err)
				}
				if result.Action != "unchanged" || result.ID != rtID {
					t.Errorf("ApplyPrepared = %+v, want action unchanged for %d", result, rtID)
				}
				if sent := mock.Requests()[before:]; len(sent) != 0 {
					t.Errorf("ApplyPrepared sent %v, want nothing", sent)
				}
			})
		}
	}
}

// An export carries no credential: no secret key, no masked value, and not
// the one credential a read returns unmasked.
func TestExportCarriesNoSecret(t *testing.T) {
	var secretKeys []string
	for _, key := range fieldKeys {
		if isSecret(key) {
			secretKeys = append(secretKeys, key)
		}
	}
	if !slices.Contains(secretKeys, "s3_access_key") || !slices.Contains(secretKeys, "s3_secret_key") {
		t.Fatalf("isSecret classifies %v as the credentials; s3_access_key and s3_secret_key must be among them", secretKeys)
	}

	cases := append(rtCases(),
		rtCase{"sftp", rtLiveBody(DataSourceTypeSftp)},
		rtCase{"dropbox", rtLiveBody(DataSourceTypeDropbox)})
	for _, tc := range cases {
		body := rtEncodeBody(t, tc.body)
		for _, f := range rtFormats {
			t.Run(tc.name+"/"+f.name, func(t *testing.T) {
				c, _ := rtServe(t, body)
				text := string(rtEncode(t, f.name, rtExport(t, c)))
				for _, key := range secretKeys {
					if strings.Contains(text, key) {
						t.Errorf("the export carries %s\n%s", key, text)
					}
				}
				for _, masked := range []string{secretMask, "•", `•`, rtPlainAccessKey, `AKIA<EXAMPLE>&7`} {
					if strings.Contains(text, masked) {
						t.Errorf("the export carries %q\n%s", masked, text)
					}
				}
			})
		}
	}
}

// An export leaves out "" in a field whose omission keeps the stored value, so
// re-applying it after the field was set keeps that value: the export of a
// source that does not poll does not say schedule: "". A "" in a field whose
// omission clears it is exported as itself.
func TestExportLeavesOutAnEmptyKeptField(t *testing.T) {
	exported := rtLiveBody(DataSourceTypeS3)
	c, _ := rtServe(t, rtEncodeBody(t, exported))
	text := rtEncode(t, "yaml", rtExport(t, c))
	m := rtParse(t, text)
	if m.Spec.Schedule != nil || m.Spec.S3EndpointURL != nil {
		t.Errorf("the export carries schedule %q and s3_endpoint_url %q, both \"\" on the read; want both left out\n%s",
			derefString(m.Spec.Schedule), derefString(m.Spec.S3EndpointURL), text)
	}
	if m.Spec.CsvDelimiter == nil || *m.Spec.CsvDelimiter != "" {
		t.Errorf("the export carries csv_delimiter %v, want \"\"\n%s", m.Spec.CsvDelimiter, text)
	}

	later := rtLiveBody(DataSourceTypeS3)
	later["version_id"] = rtVersion + 1
	later["schedule"] = "@hourly"
	c, _ = rtServe(t, rtEncodeBody(t, later))
	plan, err := c.PrepareApply("tok", m)
	if err != nil {
		t.Fatalf("PrepareApply: %v", err)
	}
	if changes := plan.Changes(); len(changes) != 0 {
		t.Errorf("re-applying the export after schedule was set plans:\n%s want nothing", FormatChanges(changes, "  "))
	}
	if from, to, ok := plan.StaleExport(); !ok || from != rtVersion || to != rtVersion+1 {
		t.Errorf("StaleExport = %d, %d, %v; want %d, %d, true", from, to, ok, rtVersion, rtVersion+1)
	}
}

// Editing one field of an export and deleting one override plans exactly
// those two changes, and the update carries only them.
func TestEditedExportChangesOnlyTheEdits(t *testing.T) {
	c, _ := rtServe(t, rtEncodeBody(t, rtLiveBody(DataSourceTypeS3)))
	m := rtParse(t, rtEncode(t, "yaml", rtExport(t, c)))
	schedule := "@daily"
	m.Spec.Schedule = &schedule
	if m.Spec.SettingsOverrides == nil || m.Spec.SettingsOverrides.NullValues == nil {
		t.Fatal("fixture: the export carries no null_values override to delete")
	}
	m.Spec.SettingsOverrides.NullValues = nil

	plan, err := c.PrepareApply("tok", m)
	if err != nil {
		t.Fatalf("PrepareApply: %v", err)
	}
	type change struct{ verb, path, old, new string }
	var got []change
	for _, ch := range plan.Changes() {
		got = append(got, change{ch.Verb(), ch.Path, string(ch.Old), string(ch.New)})
	}
	want := []change{
		{"SET", "spec.schedule", `""`, `"@daily"`},
		{"CLEAR", "spec.settings_overrides.null_values", `["N/A","null","—",""]`, ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Changes() = %q, want %q", got, want)
	}

	members := bodyMembers(t, "PATCH", plan.body)
	var keys []string
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	wantKeys := []string{"id", "null_values_override", "schedule", "version_id"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("PATCH keys = %v, want %v", keys, wantKeys)
	}
	if got := string(members["null_values_override"]); got != "null" {
		t.Errorf("PATCH null_values_override = %s, want null", got)
	}
	if got := string(members["version_id"]); got != fmt.Sprint(rtVersion) {
		t.Errorf("PATCH version_id = %s, want %d", got, rtVersion)
	}
	if effects := plan.SideEffects(); len(effects) != 0 {
		t.Errorf("SideEffects = %q, want none: null_values is not a boundary override", effects)
	}
}

// The export of a draft carries its state in status.state only, so it
// re-applies to the source as an update with no changes rather than being
// refused for carrying spec.state.
func TestDraftExportHasNoSpecState(t *testing.T) {
	body := rtLiveBody(DataSourceTypeS3)
	body["state"] = "draft"
	encoded := rtEncodeBody(t, body)
	for _, f := range rtFormats {
		t.Run(f.name, func(t *testing.T) {
			c, _ := rtServe(t, encoded)
			exported := rtExport(t, c)
			if exported.Spec.State != nil {
				t.Errorf("spec.state = %q, want it absent", *exported.Spec.State)
			}
			if exported.Status == nil || exported.Status.State != "draft" {
				t.Errorf("status = %+v, want state draft", exported.Status)
			}

			text := rtEncode(t, f.name, exported)
			m := rtParse(t, text)
			if m.Spec.State != nil {
				t.Errorf("the %s export parses back with spec.state = %q\n%s", f.name, *m.Spec.State, text)
			}
			plan, err := c.PrepareApply("tok", m)
			if err != nil {
				t.Fatalf("PrepareApply: %v\n%s", err, text)
			}
			if plan.IsCreate() || len(plan.Changes()) != 0 {
				t.Errorf("the unedited export of a draft plans create %v with %d changes, want an update with none",
					plan.IsCreate(), len(plan.Changes()))
			}
		})
	}
}
