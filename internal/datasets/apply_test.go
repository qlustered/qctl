package datasets

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
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
			name: "with enable_row_logs - can use quick",
			manifest: &TableManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "Table",
				Metadata:   pkgmanifest.Metadata{Name: "test"},
				Spec: TableSpec{
					DestinationID:    1,
					DatabaseName:     "db",
					TableName:        "tbl",
					BackupSettingsID: 1,
					EnableRowLogs:    boolPtr(true),
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
					DataLoadingProcess: "snapshot",
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

func TestManifestToQuickPostRequest(t *testing.T) {
	enableRowLogs := true
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			DestinationID:    123,
			DatabaseName:     "analytics",
			TableName:        "orders",
			BackupSettingsID: 456,
			EnableRowLogs:    &enableRowLogs,
		},
	}

	req := manifestToQuickPostRequest(manifest)

	if req.Name != "test-table" {
		t.Errorf("Name = %v, want %v", req.Name, "test-table")
	}
	if req.DestinationID != 123 {
		t.Errorf("DestinationID = %v, want %v", req.DestinationID, 123)
	}
	if req.DatabaseName != "analytics" {
		t.Errorf("DatabaseName = %v, want %v", req.DatabaseName, "analytics")
	}
	if req.TableName != "orders" {
		t.Errorf("TableName = %v, want %v", req.TableName, "orders")
	}
	if req.BackupSettingsID != 456 {
		t.Errorf("BackupSettingsID = %v, want %v", req.BackupSettingsID, 456)
	}
	if req.EnableRowLogs == nil || *req.EnableRowLogs != true {
		t.Errorf("EnableRowLogs = %v, want true", req.EnableRowLogs)
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
	dollarToCent := true
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
			DataLoadingProcess: "snapshot",
			SettingsModel: &SettingsModel{
				NullValues:                &nullValues,
				DollarToCent:              &dollarToCent,
				DecimalFieldPadding:       &decimalPadding,
				DatetimeAllowedCharacters: &datetimeChars,
				BooleanFalsePerColumn: map[string][]string{
					"is_active": {"no", "false", "0"},
				},
			},
		},
	}

	req := manifestToPostRequest(manifest)

	if req.SettingsModel == nil {
		t.Fatal("SettingsModel should not be nil")
	}

	if req.SettingsModel.NullValues == nil || len(*req.SettingsModel.NullValues) != 3 {
		t.Errorf("NullValues = %v, want 3 items", req.SettingsModel.NullValues)
	}

	if req.SettingsModel.DollarToCent == nil || *req.SettingsModel.DollarToCent != true {
		t.Errorf("DollarToCent = %v, want true", req.SettingsModel.DollarToCent)
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

func TestManifestToPatchRequest_WithSettingsModel(t *testing.T) {
	nullValues := []string{"N/A"}

	existing := &DatasetFull{
		ID:        123,
		VersionID: 5,
	}

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "test-table"},
		Spec: TableSpec{
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "snapshot",
			SettingsModel: &SettingsModel{
				NullValues: &nullValues,
			},
		},
	}

	req := manifestToPatchRequest(existing, manifest)

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
			DataLoadingProcess: "snapshot",
		},
	}

	req := manifestToPostRequest(manifest)

	if req.SettingsModel != nil {
		t.Error("SettingsModel should be nil when not set in manifest")
	}
}

func TestDefaultsToPostRequest(t *testing.T) {
	defaults := &api.DataSetSchemaFullDraft{
		AnomalyThreshold:  50,
		MaxRetryCount:     3,
		MaxTriesToFixJSON: 3,
		MigrationPolicy:   api.ApplyAsap,
		DataLoadingProcess: api.Snapshot,
		BackupKeyFormat:   "{dataset_id}/{datetime}",
		DetectAnomalies:   boolPtr(false),
		EnableRowLogs:     boolPtr(true),
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
	if req.EnableRowLogs == nil || *req.EnableRowLogs != true {
		t.Errorf("expected EnableRowLogs true, got %v", req.EnableRowLogs)
	}
	// Required fields should be zero-valued (set by applyManifestOverrides)
	if req.Name != "" {
		t.Errorf("expected empty Name, got %q", req.Name)
	}
}

func TestApplyManifestOverrides(t *testing.T) {
	// Start with defaults
	defaults := &api.DataSetSchemaFullDraft{
		AnomalyThreshold:  50,
		MaxRetryCount:     3,
		MaxTriesToFixJSON: 3,
		MigrationPolicy:   api.ApplyAsap,
		DataLoadingProcess: api.Snapshot,
		DetectAnomalies:   boolPtr(false),
		EnableRowLogs:     boolPtr(true),
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
				DataLoadingProcess: "snapshot",
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
				DataLoadingProcess: "snapshot",
				AnomalyThreshold:  intPtr(10),
				MaxRetryCount:     intPtr(5),
				DetectAnomalies:   boolPtr(true),
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
		DataLoadingProcess:                 "snapshot",
		AnomalyThreshold:                   80,
		MaxRetryCount:                      5,
		MaxTriesToFixJSON:                   100,
		BackupKeyFormat:                    "%Y/%m/{dataset_name}",
		ShouldReprocess:                    boolPtr(false),
		DetectAnomalies:                    boolPtr(false),
		EnableRowLogs:                      boolPtr(true),
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
			DataLoadingProcess: "snapshot",
		},
	}

	req := manifestToPatchRequest(existing, manifest)

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
	if req.EnableRowLogs == nil || *req.EnableRowLogs != true {
		t.Errorf("EnableRowLogs = %v, want true (preserved from existing)", req.EnableRowLogs)
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
		ID:               3,
		VersionID:        2,
		Name:             "my-table",
		MigrationPolicy:  "apply_asap",
		DataLoadingProcess: "snapshot",
		AnomalyThreshold: 80,
		MaxRetryCount:    5,
		DetectAnomalies:  boolPtr(false),
		EnableRowLogs:    boolPtr(true),
	}

	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "my-table"},
		Spec: TableSpec{
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "snapshot",
			AnomalyThreshold:  intPtr(50),
			DetectAnomalies:   boolPtr(true),
		},
	}

	req := manifestToPatchRequest(existing, manifest)

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
	if req.EnableRowLogs == nil || *req.EnableRowLogs != true {
		t.Errorf("EnableRowLogs = %v, want true (preserved from existing)", req.EnableRowLogs)
	}
}
