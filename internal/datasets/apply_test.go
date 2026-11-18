package datasets

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qclient "github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestCanUseQuickCreate(t *testing.T) {
	tests := []struct {
		name     string
		manifest *TableManifest
		want     bool
	}{
		{
			name: "minimal manifest - can use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					TableName:        "tbl",
					BackupSettingsID: 1,
				},
			},
			want: true,
		},
		{
			name: "with schema_name - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					SchemaName:       "public",
					TableName:        "tbl",
					BackupSettingsID: 1,
				},
			},
			want: false,
		},
		{
			name: "with migration_policy - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:      1,
					DatabaseName:       "db",
					TableName:          "tbl",
					BackupSettingsID:   1,
					MigrationPolicy:    "apply_asap",
					DataLoadingProcess: "append",
				},
			},
			want: false,
		},
		{
			name: "with anomaly_threshold - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					TableName:        "tbl",
					BackupSettingsID: 1,
					AnomalyThreshold: intPtr(50),
				},
			},
			want: false,
		},
		{
			name: "with detect_anomalies - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					TableName:        "tbl",
					BackupSettingsID: 1,
					DetectAnomalies:  boolPtr(true),
				},
			},
			want: false,
		},
		{
			name: "with backup_key_format - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					TableName:        "tbl",
					BackupSettingsID: 1,
					BackupKeyFormat:  "{dataset_id}/{datetime}",
				},
			},
			want: false,
		},
		{
			name: "with columns_for_entity_resolution - cannot use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:              1,
					DatabaseName:               "db",
					TableName:                  "tbl",
					BackupSettingsID:           1,
					ColumnsForEntityResolution: &[]string{"col1", "col2"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canUseQuickCreate(tt.manifest)
			if got != tt.want {
				t.Errorf("canUseQuickCreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrepareFullCreateBodyStopsOnDefaultsAccessFailure(t *testing.T) {
	apierror.SetOrgScope("Qluster-dev", "from the current context")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	for _, tc := range []struct {
		status   int
		wantCode int
	}{
		{status: http.StatusForbidden, wantCode: qerrors.ExitUnauthorized},
		{status: http.StatusNotFound, wantCode: qerrors.ExitNotFound},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			path := "/api/orgs/" + testOrgID + "/datasets/new"
			mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{
					"detail": map[string]any{"msg": "defaults unavailable in this organization"},
				})
			})

			apiClient, err := qclient.New(qclient.Config{BaseURL: mock.URL(), AccessToken: "token"})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			manifest := &TableManifest{
				Metadata: pkgmanifest.Metadata{Name: "orders"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "analytics",
					SchemaName:       "public",
					TableName:        "orders",
					BackupSettingsID: 1,
				},
			}

			_, err = NewClient(mock.URL(), testOrgID, 0).prepareFullCreateBody(apiClient, manifest)
			if err == nil {
				t.Fatal("expected the defaults access failure to stop full-create preparation")
			}
			if got := qerrors.GetExitCode(err); got != tc.wantCode {
				t.Errorf("exit code = %d, want %d", got, tc.wantCode)
			}
			for _, want := range []string{
				"defaults unavailable in this organization",
				"Scoped to organization Qluster-dev",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if got := mock.Requests(); len(got) != 1 || got[0] != "GET "+path {
				t.Errorf("requests = %v, want only [GET %s]", got, path)
			}
		})
	}
}

func TestCanUseQuickCreate_ExplicitZeroValuesRequireFullCreate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TableSpec)
	}{
		{name: "zero anomaly threshold", mutate: func(spec *TableSpec) { spec.AnomalyThreshold = intPtr(0) }},
		{name: "zero max retry count", mutate: func(spec *TableSpec) { spec.MaxRetryCount = intPtr(0) }},
		{name: "zero max tries to fix JSON", mutate: func(spec *TableSpec) { spec.MaxTriesToFixJSON = intPtr(0) }},
		{name: "false detect anomalies", mutate: func(spec *TableSpec) { spec.DetectAnomalies = boolPtr(false) }},
		{name: "false strict datetime format", mutate: func(spec *TableSpec) { spec.StrictlyOneDatetimeFormatInAColumn = boolPtr(false) }},
		{name: "false guess datetime format", mutate: func(spec *TableSpec) { spec.GuessDatetimeFormatInIngestion = boolPtr(false) }},
		{name: "false cell move suggestions", mutate: func(spec *TableSpec) { spec.EnableCellMoveSuggestions = boolPtr(false) }},
		{name: "false raw backup encryption", mutate: func(spec *TableSpec) { spec.EncryptRawDataDuringBackup = boolPtr(false) }},
		{name: "false quarantine until approved", mutate: func(spec *TableSpec) { spec.QuarantineRowsUntilApproved = boolPtr(false) }},
		{name: "false remove outliers", mutate: func(spec *TableSpec) { spec.RemoveOutliersWhenRecommendingNumericValidators = boolPtr(false) }},
		{name: "empty entity resolution columns", mutate: func(spec *TableSpec) { spec.ColumnsForEntityResolution = &[]string{} }},
		{name: "empty pii columns", mutate: func(spec *TableSpec) { spec.PiiColumns = &[]string{} }},
		{name: "empty settings model", mutate: func(spec *TableSpec) { spec.SettingsModel = &SettingsModel{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &TableManifest{Spec: TableSpec{}}
			tt.mutate(&manifest.Spec)

			if canUseQuickCreate(manifest) {
				t.Fatal("explicit non-quick field must require full create")
			}
		})
	}
}

func TestManifestToQuickPostRequest(t *testing.T) {
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			DestinationID:    123,
			DatabaseName:     "analytics",
			TableName:        "orders",
			BackupSettingsID: 456,
		},
	}

	req := manifestToQuickPostRequest(manifest)

	if req.Name == nil || *req.Name != "test-table" {
		t.Errorf("Name = %v, want %v", req.Name, "test-table")
	}
	if req.DestinationID != 123 {
		t.Errorf("DestinationID = %v, want %v", req.DestinationID, 123)
	}
	if req.DatabaseName != "analytics" {
		t.Errorf("DatabaseName = %v, want %v", req.DatabaseName, "analytics")
	}
	if req.TableName == nil || *req.TableName != "orders" {
		t.Errorf("TableName = %v, want %v", req.TableName, "orders")
	}
	if req.BackupSettingsID != 456 {
		t.Errorf("BackupSettingsID = %v, want %v", req.BackupSettingsID, 456)
	}
}

func TestQuickCreateLoadingProcessRoutingAndBody(t *testing.T) {
	for _, tc := range []struct {
		name    string
		process DataLoadingProcess
		quick   bool
	}{
		{name: "omitted", quick: true},
		{name: "append", process: api.DataLoadingProcessAppend, quick: true},
		{name: "append_skip_identical_rows", process: api.DataLoadingProcessAppendSkipIdenticalRows, quick: true},
		{name: "mirror", process: api.DataLoadingProcessMirror, quick: true},
		{name: "composite", process: api.DataLoadingProcessComposite},
		{name: "unknown", process: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := &TableManifest{
				Metadata: pkgmanifest.Metadata{Name: "orders"},
				Spec: TableSpec{
					DestinationID:      1,
					DatabaseName:       "analytics",
					TableName:          "orders",
					BackupSettingsID:   2,
					DataLoadingProcess: tc.process,
				},
			}
			if got := canUseQuickCreate(manifest); got != tc.quick {
				t.Fatalf("canUseQuickCreate() = %v, want %v", got, tc.quick)
			}
			if !tc.quick {
				return
			}
			body, err := json.Marshal(manifestToQuickPostRequest(manifest))
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatal(err)
			}
			if tc.process == "" {
				if wire["data_loading_process"] != nil {
					t.Errorf("omitted loading process = %v, want null", wire["data_loading_process"])
				}
			} else if got := wire["data_loading_process"]; got != string(tc.process) {
				t.Errorf("data_loading_process = %v, want %q", got, tc.process)
			}
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestCanUseQuickCreate_WithSettingsModel(t *testing.T) {
	// settings_model is NOT in quick create schema, so it should force full create
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test"},
		Spec: TableSpec{
			DestinationID:    1,
			DatabaseName:     "db",
			TableName:        "tbl",
			BackupSettingsID: 1,
			SettingsModel: &SettingsModel{
				NullValues: &[]string{"N/A", "null"},
			},
		},
	}

	if canUseQuickCreate(manifest) {
		t.Error("canUseQuickCreate() should return false when settings_model is set")
	}
}

func TestManifestToPostRequest_WithSettingsModel(t *testing.T) {
	nullValues := []string{"N/A", "null", ""}
	decimalPadding := 2
	datetimeChars := "/-: "

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			DestinationID:      1,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   1,
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
			SettingsModel: &SettingsModel{
				NullValues:                &nullValues,
				DecimalFieldPadding:       &decimalPadding,
				DatetimeAllowedCharacters: &datetimeChars,
				BooleanFalsePerColumn: &map[string][]string{
					"is_active": {"no", "false", "0"},
				},
			},
		},
	}

	req, err := manifestToPostRequest(manifest)
	if err != nil {
		t.Fatalf("manifestToPostRequest: %v", err)
	}

	if req.SettingsModel == nil {
		t.Fatal("SettingsModel should not be nil")
	}

	if req.SettingsModel.NullValues == nil || len(*req.SettingsModel.NullValues) != 3 {
		t.Errorf("NullValues = %v, want 3 items", req.SettingsModel.NullValues)
	}

	if req.SettingsModel.DecimalFieldPadding == nil || *req.SettingsModel.DecimalFieldPadding != 2 {
		t.Errorf("DecimalFieldPadding = %v, want 2", req.SettingsModel.DecimalFieldPadding)
	}

	if req.SettingsModel.DatetimeAllowedCharacters == nil || *req.SettingsModel.DatetimeAllowedCharacters != "/-: " {
		t.Errorf("DatetimeAllowedCharacters = %v, want '/-: '", req.SettingsModel.DatetimeAllowedCharacters)
	}

	if req.SettingsModel.BooleanFalsePerColumn == nil {
		t.Error("BooleanFalsePerColumn should not be nil")
	} else {
		vals, ok := (*req.SettingsModel.BooleanFalsePerColumn)["is_active"]
		if !ok || len(vals) != 3 {
			t.Errorf("BooleanFalsePerColumn[is_active] = %v, want 3 items", vals)
		}
	}
}

// TestManifestToPostRequest_CarriesTheThreeLateSettings retires the sharper half
// of qctl-o22. key_columns was not merely lossy on the read: it was
// unsettable from qctl at all, and the backend refuses to CHANGE it once a
// migration has been applied to the table (atlas
// capi/capi/views/settings_view_core.py patch_dataset_core), so a sync table
// qctl created could not get key fields once it had a schema. The web UI
// is no fallback: cosmosview mentions neither key_columns nor
// limit_fields_in_signature_calculation_to outside its generated client
// (types.gen.ts, reasonFields.gen.ts), so no page writes either one.
//
// It asserts the marshalled body rather than the request struct, because the
// struct is not where the gap was: api.NewSettingsSchema has carried all three
// fields all along; nothing ever put a value in them.
func TestManifestToPostRequest_CarriesTheThreeLateSettings(t *testing.T) {
	syncKeys := []string{"order_id", "line_no"}
	signatureFields := []string{"order_id", "amount"}
	ignoreWords := []string{"internal", "debug"}

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			DestinationID:      1,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   1,
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "mirror",
			SettingsModel: &SettingsModel{
				KeyColumns:                          &syncKeys,
				LimitFieldsInSignatureCalculationTo: &signatureFields,
				IgnoreColumnNamesThatHaveAnyOfWords: &ignoreWords,
			},
		},
	}

	post, err := manifestToPostRequest(manifest)
	if err != nil {
		t.Fatalf("manifestToPostRequest: %v", err)
	}
	raw, err := json.Marshal(post)
	if err != nil {
		t.Fatalf("marshal POST request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal POST request: %v", err)
	}
	settings, ok := body["settings_model"].(map[string]any)
	if !ok {
		t.Fatalf("settings_model is %T on the POST body, want an object", body["settings_model"])
	}

	for key, want := range map[string][]any{
		"key_columns": {"order_id", "line_no"},
		"limit_fields_in_signature_calculation_to":   {"order_id", "amount"},
		"ignore_column_names_that_have_any_of_words": {"internal", "debug"},
	} {
		got := settings[key]
		if !reflect.DeepEqual(got, want) {
			t.Errorf("settings_model.%s = %v, want %v -- the manifest asked for it and the create body did not carry it", key, got, want)
		}
	}
}

func TestManifestToPatchRequest_WithSettingsModel(t *testing.T) {
	nullValues := []string{"N/A"}

	// The settings model carries an id because a real read always does; a zero
	// one is refused by applyPatchStructuredOverrides
	// (TestPatchRefusesASettingsEditWhenTheReadHasNoSettingsModel).
	existing := &DatasetFull{
		ID:            123,
		VersionID:     5,
		SettingsModel: SettingsSchema{ID: 9, VersionID: 2},
	}

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
			SettingsModel: &SettingsModel{
				NullValues: &nullValues,
			},
		},
	}

	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	if req.ID != 123 {
		t.Errorf("ID = %v, want 123", req.ID)
	}

	if req.VersionID != 5 {
		t.Errorf("VersionID = %v, want 5", req.VersionID)
	}

	if req.SettingsModel == nil {
		t.Fatal("SettingsModel should not be nil")
	}

	if req.SettingsModel.NullValues == nil || len(*req.SettingsModel.NullValues) != 1 {
		t.Errorf("NullValues = %v, want 1 item", req.SettingsModel.NullValues)
	}
}

func TestManifestToPostRequest_WithoutSettingsModel(t *testing.T) {
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			DestinationID:      1,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   1,
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
		},
	}

	req, err := manifestToPostRequest(manifest)
	if err != nil {
		t.Fatalf("manifestToPostRequest: %v", err)
	}

	if req.SettingsModel != nil {
		t.Error("SettingsModel should be nil when not set in manifest")
	}
}

func TestDefaultsToPostRequest(t *testing.T) {
	defaults := &api.DataSetSchemaFullDraft{
		AnomalyThreshold:   50,
		MaxRetryCount:      3,
		MaxTriesToFixJSON:  3,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.DataLoadingProcessAppend,
		BackupKeyFormat:    "{dataset_id}/{datetime}",
		DetectAnomalies:    boolPtr(false),
	}

	req := defaultsToPostRequest(defaults)

	if req.AnomalyThreshold == nil || *req.AnomalyThreshold != 50 {
		t.Errorf("expected AnomalyThreshold 50, got %v", req.AnomalyThreshold)
	}
	if req.MaxRetryCount == nil || *req.MaxRetryCount != 3 {
		t.Errorf("expected MaxRetryCount 3, got %v", req.MaxRetryCount)
	}
	if req.MaxTriesToFixJSON == nil || *req.MaxTriesToFixJSON != 3 {
		t.Errorf("expected MaxTriesToFixJSON 3, got %v", req.MaxTriesToFixJSON)
	}
	if req.DetectAnomalies == nil || *req.DetectAnomalies != false {
		t.Errorf("expected DetectAnomalies false, got %v", req.DetectAnomalies)
	}
	// Required fields should be zero-valued (set by applyManifestOverrides)
	if req.Name != "" {
		t.Errorf("expected empty Name, got %q", req.Name)
	}
}

func TestApplyManifestOverrides(t *testing.T) {
	// Start with defaults
	defaults := &api.DataSetSchemaFullDraft{
		AnomalyThreshold:   50,
		MaxRetryCount:      3,
		MaxTriesToFixJSON:  3,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.DataLoadingProcessAppend,
		DetectAnomalies:    boolPtr(false),
	}
	req := defaultsToPostRequest(defaults)

	t.Run("nil fields keep defaults", func(t *testing.T) {
		manifest := &TableManifest{
			Metadata: pkgmanifest.Metadata{Name: "test"},
			Spec: TableSpec{
				DestinationID:      1,
				DatabaseName:       "db",
				SchemaName:         "public",
				TableName:          "tbl",
				BackupSettingsID:   1,
				MigrationPolicy:    "apply_asap",
				DataLoadingProcess: "append",
				// AnomalyThreshold, MaxRetryCount, MaxTriesToFixJSON are nil
			},
		}

		reqCopy := req // copy the struct
		applyManifestOverrides(&reqCopy, manifest)

		// Required fields should be set
		if reqCopy.Name != "test" {
			t.Errorf("expected Name 'test', got %q", reqCopy.Name)
		}

		// Nil fields should keep defaults
		if reqCopy.AnomalyThreshold == nil || *reqCopy.AnomalyThreshold != 50 {
			t.Errorf("expected AnomalyThreshold 50 (default), got %v", reqCopy.AnomalyThreshold)
		}
		if reqCopy.MaxRetryCount == nil || *reqCopy.MaxRetryCount != 3 {
			t.Errorf("expected MaxRetryCount 3 (default), got %v", reqCopy.MaxRetryCount)
		}
		if reqCopy.DetectAnomalies == nil || *reqCopy.DetectAnomalies != false {
			t.Errorf("expected DetectAnomalies false (default), got %v", reqCopy.DetectAnomalies)
		}
	})

	t.Run("set fields override defaults", func(t *testing.T) {
		manifest := &TableManifest{
			Metadata: pkgmanifest.Metadata{Name: "test"},
			Spec: TableSpec{
				DestinationID:      1,
				DatabaseName:       "db",
				SchemaName:         "public",
				TableName:          "tbl",
				BackupSettingsID:   1,
				MigrationPolicy:    "apply_asap",
				DataLoadingProcess: "append",
				AnomalyThreshold:   intPtr(10),
				MaxRetryCount:      intPtr(5),
				DetectAnomalies:    boolPtr(true),
			},
		}

		reqCopy := req
		applyManifestOverrides(&reqCopy, manifest)

		if reqCopy.AnomalyThreshold == nil || *reqCopy.AnomalyThreshold != 10 {
			t.Errorf("expected AnomalyThreshold 10 (override), got %v", reqCopy.AnomalyThreshold)
		}
		if reqCopy.MaxRetryCount == nil || *reqCopy.MaxRetryCount != 5 {
			t.Errorf("expected MaxRetryCount 5 (override), got %v", reqCopy.MaxRetryCount)
		}
		// MaxTriesToFixJSON should keep default since manifest didn't set it
		if reqCopy.MaxTriesToFixJSON == nil || *reqCopy.MaxTriesToFixJSON != 3 {
			t.Errorf("expected MaxTriesToFixJSON 3 (default), got %v", reqCopy.MaxTriesToFixJSON)
		}
		if reqCopy.DetectAnomalies == nil || *reqCopy.DetectAnomalies != true {
			t.Errorf("expected DetectAnomalies true (override), got %v", reqCopy.DetectAnomalies)
		}
	})
}

func TestManifestToPatchRequest_PreservesExistingValues(t *testing.T) {
	existing := &DatasetFull{
		ID:                                 3,
		VersionID:                          2,
		Name:                               "bdx-insurance-test",
		MigrationPolicy:                    "apply_asap",
		DataLoadingProcess:                 "append",
		AnomalyThreshold:                   80,
		MaxRetryCount:                      5,
		MaxTriesToFixJSON:                  100,
		BackupKeyFormat:                    "%Y/%m/{dataset_name}",
		DetectAnomalies:                    boolPtr(false),
		EnableCellMoveSuggestions:          boolPtr(false),
		EncryptRawDataDuringBackup:         boolPtr(false),
		QuarantineRowsUntilApproved:        boolPtr(false),
		StrictlyOneDatetimeFormatInAColumn: boolPtr(true),
		GuessDatetimeFormatInIngestion:     boolPtr(false),
		RemoveOutliersWhenRecommendingNumericValidators: boolPtr(true),
	}

	// Manifest only specifies required fields — everything else should come from existing
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "bdx-insurance-test"},
		Spec: TableSpec{
			DestinationID:      1,
			DatabaseName:       "bdx_insurance",
			SchemaName:         "default",
			TableName:          "bdx_insurance_test",
			BackupSettingsID:   1,
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
		},
	}

	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	// Existing values should be preserved, not null
	if req.AnomalyThreshold == nil || *req.AnomalyThreshold != 80 {
		t.Errorf("AnomalyThreshold = %v, want 80 (preserved from existing)", req.AnomalyThreshold)
	}
	if req.MaxRetryCount == nil || *req.MaxRetryCount != 5 {
		t.Errorf("MaxRetryCount = %v, want 5 (preserved from existing)", req.MaxRetryCount)
	}
	if req.MaxTriesToFixJSON == nil || *req.MaxTriesToFixJSON != 100 {
		t.Errorf("MaxTriesToFixJSON = %v, want 100 (preserved from existing)", req.MaxTriesToFixJSON)
	}
	if req.DetectAnomalies == nil || *req.DetectAnomalies != false {
		t.Errorf("DetectAnomalies = %v, want false (preserved from existing)", req.DetectAnomalies)
	}
	if req.BackupKeyFormat == nil || *req.BackupKeyFormat != "%Y/%m/{dataset_name}" {
		t.Errorf("BackupKeyFormat = %v, want preserved from existing", req.BackupKeyFormat)
	}
}

func TestManifestToPatchRequest_OverridesExistingValues(t *testing.T) {
	existing := &DatasetFull{
		ID:                 3,
		VersionID:          2,
		Name:               "my-table",
		MigrationPolicy:    "apply_asap",
		DataLoadingProcess: "append",
		AnomalyThreshold:   80,
		MaxRetryCount:      5,
		DetectAnomalies:    boolPtr(false),
	}

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "my-table"},
		Spec: TableSpec{
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
			AnomalyThreshold:   intPtr(50),
			DetectAnomalies:    boolPtr(true),
		},
	}

	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	// Manifest-specified fields should override
	if req.AnomalyThreshold == nil || *req.AnomalyThreshold != 50 {
		t.Errorf("AnomalyThreshold = %v, want 50 (overridden by manifest)", req.AnomalyThreshold)
	}
	if req.DetectAnomalies == nil || *req.DetectAnomalies != true {
		t.Errorf("DetectAnomalies = %v, want true (overridden by manifest)", req.DetectAnomalies)
	}
	// Non-specified fields should keep existing values
	if req.MaxRetryCount == nil || *req.MaxRetryCount != 5 {
		t.Errorf("MaxRetryCount = %v, want 5 (preserved from existing)", req.MaxRetryCount)
	}
}

// quickCreateSpecFields are the TableSpec fields DataSetCreateQuickPostRequest
// can carry (manifestToQuickPostRequest sends these five plus metadata.name).
// Other fields need full create unless a dedicated PUT follows creation.
var quickCreateSpecFields = map[string]bool{
	"DestinationID":      true,
	"DatabaseName":       true,
	"TableName":          true,
	"BackupSettingsID":   true,
	"DataLoadingProcess": true,
}

// TestQuickCreateSeesEveryManifestField is the tripwire for the way a new spec
// key gets lost.
//
// canUseQuickCreate enumerates TableSpec BY HAND across three helpers, and
// manifestToQuickPostRequest carries seven values. Apart from fields with a
// dedicated PUT, a field nobody adds to those helpers is not refused or
// reported -- a manifest that sets it and is
// otherwise quick-create shaped takes the quick path, the key never reaches the
// wire, and apply prints "table created successfully" over a create that
// dropped it. Nothing else in the suite can see that: the existing
// canUseQuickCreate tests name their fields one at a time, so a field they do
// not name is a field they never ask about. Verified by mutation -- deleting
// either of the two lines qctl-r4h added to hasNonQuickCreateAdvancedFields
// fails here and nowhere else.
//
// It walks the spec instead of listing it for the reason the whole repo prefers
// generated lists: a hand-transcribed inventory of what a struct has is wrong
// within months, and this one would be wrong silently.
func TestQuickCreateSeesEveryManifestField(t *testing.T) {
	base := func() *TableManifest {
		return &TableManifest{
			APIVersion: pkgmanifest.APIVersionV1,
			Kind:       "Table",
			Metadata:   pkgmanifest.Metadata{Name: "orders"},
			Spec: TableSpec{
				DestinationID:    1,
				DatabaseName:     "analytics",
				TableName:        "orders",
				BackupSettingsID: 1,
			},
		}
	}

	// The control: without it every assertion below would pass against a
	// canUseQuickCreate that had simply stopped returning true.
	if !canUseQuickCreate(base()) {
		t.Fatal("a manifest holding only quick-create fields must still use quick create; the rest of this test proves nothing otherwise")
	}

	rt := reflect.TypeOf(TableSpec{})
	for name := range quickCreateSpecFields {
		if _, ok := rt.FieldByName(name); !ok {
			t.Errorf("quickCreateSpecFields names TableSpec.%s, which no longer exists; fix the entry rather than leaving it to exempt a field a later rename gives that name", name)
		}
	}

	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if quickCreateSpecFields[f.Name] || separateDatasetWriteKeys[tagKey(f.Tag.Get("yaml"), f.Name)] {
			continue
		}

		t.Run(f.Name, func(t *testing.T) {
			manifest := base()
			field := reflect.ValueOf(&manifest.Spec).Elem().Field(i)
			field.Set(nonQuickCreateValue(t, f.Type))

			if canUseQuickCreate(manifest) {
				t.Errorf("a manifest that sets spec.%s still takes the quick-create path, which cannot carry it: apply would report success over a create that dropped the value. Add it to canUseQuickCreate", tagKey(f.Tag.Get("yaml"), f.Name))
			}
		})
	}
}

// nonQuickCreateValue builds the value that must make canUseQuickCreate say no.
//
// For a pointer field that is the ZERO pointee, not an interesting one: the
// manifest's whole reason for pointers is that `detect_anomalies: false` is a
// value a user asked for, and a check written as `*spec.X` rather than
// `spec.X != nil` would pass on a true and fail here
// (TestCanUseQuickCreate_ExplicitZeroValuesRequireFullCreate covers the same
// ground for the fields that existed when it was written).
func nonQuickCreateValue(t *testing.T, rt reflect.Type) reflect.Value {
	t.Helper()

	switch rt.Kind() {
	case reflect.Pointer:
		return reflect.New(rt.Elem())
	case reflect.String:
		return reflect.ValueOf("x").Convert(rt)
	case reflect.Int:
		return reflect.ValueOf(1).Convert(rt)
	default:
		t.Fatalf("TableSpec grew a %s field and this helper has no non-zero value for that kind; add one rather than letting the field skip the check", rt.Kind())
		return reflect.Value{}
	}
}
