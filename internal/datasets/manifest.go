package datasets

import (
	"fmt"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// TableSpec defines the declarative configuration for a table.
//
// Its key set is api.DataSetPostRequestSchema's minus the keys named below,
// plus table_kinds and column_standard_fields, which use separate dataset PUTs.
// TestTableSpecCoversEveryDatasetWriteKey checks the dataset write keys.
//
// Two of the write schemas' keys are left off ON PURPOSE, so the next sweep does
// not re-file them (qctl-r4h):
//
//   - not_anomalies_per_column is an append-only ledger, not configuration a
//     person authors. Resolving an anomaly alert or warning with the
//     not_anomaly action appends to it (atlas AlertController.anomaly in
//     capi/capi/controllers/alert_controller.py and WarningController.anomaly
//     in capi/capi/controllers/warning_controller.py, both via
//     dataset.add_value), a path qctl itself drives through `qctl alert resolve
//     --action not_anomaly`. A spec key would go stale the moment anyone
//     resolved an alert, and the next apply of an exported manifest would
//     silently revert those judgements -- re-arming values a person had already
//     cleared. Same reason excluded_row_signatures is off. Reading it back is a
//     real gap and has its own issue (qctl-njt); the answer there is a
//     read-only view, not a spec key.
//   - redirect_post_submit_to is not a dataset column at all. It is a transient
//     response-only attribute the CAPI views stamp on the way out
//     (_create_new_dataset in settings_view_core.py sets it to "/datasets/{id}";
//     the model declares it only under TYPE_CHECKING, DataSet in
//     cettings/cettings/models/settings_model.py)
//     and it reaches the write schemas only because those are all-optional
//     clones of DataSetSchemaFull. Sending it is inert: update_from_body would
//     setattr it onto the ORM instance, where no column persists it, and the
//     view overwrites it on the way out. A manifest key would accept a value
//     and change nothing.
//
// The rest of the exclusions -- server-owned ids, read-only counters, the UI's
// options lists -- are named with their reasons in that test's exclusion map.
type TableSpec struct {
	DestinationID int `yaml:"destination_id" json:"destination_id"`

	DatabaseName string `yaml:"database_name" json:"database_name"`
	SchemaName   string `yaml:"schema_name" json:"schema_name"`
	TableName    string `yaml:"table_name" json:"table_name"`

	MigrationPolicy    MigrationPolicy    `yaml:"migration_policy" json:"migration_policy"`
	DataLoadingProcess DataLoadingProcess `yaml:"data_loading_process" json:"data_loading_process"`

	BackupSettingsID int    `yaml:"backup_settings_id" json:"backup_settings_id"`
	BackupKeyFormat  string `yaml:"backup_key_format,omitempty" json:"backup_key_format,omitempty"`

	// CompressionTypeOfBackupData is gzip, zip, snappy_stream or tgz. The column
	// is nullable, but neither write schema carries the key when it is absent
	// (both declare omitempty), so a manifest can choose a compression and
	// cannot clear one back to null.
	CompressionTypeOfBackupData *CompressionType `yaml:"compression_type_of_backup_data,omitempty" json:"compression_type_of_backup_data,omitempty"`

	AnomalyThreshold  *int `yaml:"anomaly_threshold,omitempty" json:"anomaly_threshold,omitempty"`
	MaxRetryCount     *int `yaml:"max_retry_count,omitempty" json:"max_retry_count,omitempty"`
	MaxTriesToFixJSON *int `yaml:"max_tries_to_fix_json,omitempty" json:"max_tries_to_fix_json,omitempty"`

	DetectAnomalies                                 *bool `yaml:"detect_anomalies,omitempty" json:"detect_anomalies,omitempty"`
	StrictlyOneDatetimeFormatInAColumn              *bool `yaml:"strictly_one_datetime_format_in_a_column,omitempty" json:"strictly_one_datetime_format_in_a_column,omitempty"`
	GuessDatetimeFormatInIngestion                  *bool `yaml:"guess_datetime_format_in_ingestion,omitempty" json:"guess_datetime_format_in_ingestion,omitempty"`
	EnableCellMoveSuggestions                       *bool `yaml:"enable_cell_move_suggestions,omitempty" json:"enable_cell_move_suggestions,omitempty"`
	EncryptRawDataDuringBackup                      *bool `yaml:"encrypt_raw_data_during_backup,omitempty" json:"encrypt_raw_data_during_backup,omitempty"`
	QuarantineRowsUntilApproved                     *bool `yaml:"quarantine_rows_until_approved,omitempty" json:"quarantine_rows_until_approved,omitempty"`
	RemoveOutliersWhenRecommendingNumericValidators *bool `yaml:"remove_outliers_when_recommending_numeric_validators,omitempty" json:"remove_outliers_when_recommending_numeric_validators,omitempty"`

	ColumnsForEntityResolution *[]string `yaml:"columns_for_entity_resolution,omitempty" json:"columns_for_entity_resolution,omitempty"`

	// PiiColumns names the fields marked by hand as holding personal data.
	// Omitting the key keeps the stored list; [] clears it.
	PiiColumns *[]string `yaml:"pii_columns,omitempty" json:"pii_columns,omitempty"`

	// ModelLabels maps a field name to the human-friendly label shown in its
	// place. The data route renders these as the column headers `qctl get data`
	// prints (atlas capi/capi/views/data_view_core.py DataPaginator.set_fields ->
	// api.DataViewSchema.Labels -> internal/dataview/normalize.go). Atlas seeds
	// labels from the source headers after a migration; this manifest key lets a
	// user override that map.
	ModelLabels *map[string]string `yaml:"model_labels,omitempty" json:"model_labels,omitempty"`
	TableKinds  *([]string)        `yaml:"table_kinds,omitempty" json:"table_kinds,omitempty"`

	// ColumnStandardFields maps a column to the slug of its standard field.
	// ParseTableManifest also accepts it as column_concepts.
	ColumnStandardFields *map[string]string `yaml:"column_standard_fields,omitempty" json:"column_standard_fields,omitempty"`

	// SettingsModel contains advanced ingestion settings like datetime formats, null values, etc.
	SettingsModel *SettingsModel `yaml:"settings_model,omitempty" json:"settings_model,omitempty"`
}

// TableStatus captures runtime information for a table
type TableStatus struct {
	ID              int          `yaml:"id" json:"id"`
	State           DataSetState `yaml:"state" json:"state"`
	VersionID       int          `yaml:"version_id" json:"version_id"`
	OrganizationID  string       `yaml:"organization_id" json:"organization_id"`
	DestinationID   int          `yaml:"destination_id" json:"destination_id"`
	DestinationName string       `yaml:"destination_name,omitempty" json:"destination_name,omitempty"`
	CleanRowsCount  *int         `yaml:"clean_rows_count,omitempty" json:"clean_rows_count,omitempty"`
	BadRowsCount    *int         `yaml:"bad_rows_count,omitempty" json:"bad_rows_count,omitempty"`
	// PiiVisibility is the reader's visibility of the table's personal data,
	// nil when the response carried none.
	PiiVisibility *PiiVisibility `yaml:"pii_visibility,omitempty" json:"pii_visibility,omitempty"`
	// EffectivePiiColumns is the table's personal-data columns, and
	// PiiColumnSources why each is one. Both are absent when the response
	// carried none.
	EffectivePiiColumns []string          `yaml:"effective_pii_columns,omitempty" json:"effective_pii_columns,omitempty"`
	PiiColumnSources    []PiiColumnSource `yaml:"pii_column_sources,omitempty" json:"pii_column_sources,omitempty"`
}

// TableManifest is the declarative representation of a table
type TableManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       TableSpec            `yaml:"spec" json:"spec"`
	// status is the status block the parsed document carried, nil without one.
	status *TableStatus
	// clearKeyColumns is whether the parsed document wrote
	// spec.settings_model.key_columns as null.
	clearKeyColumns bool
}

// TableManifestWithStatus extends TableManifest with runtime status
type TableManifestWithStatus struct {
	TableManifest `yaml:",inline" json:",inline"`
	Status        *TableStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// tableManifestInput is TableManifestWithStatus as a file may spell it.
type tableManifestInput struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       tableSpecInput       `yaml:"spec" json:"spec"`
	Status     *TableStatus         `yaml:"status,omitempty" json:"status,omitempty"`
}

// tableSpecInput is TableSpec plus column_concepts, the earlier name of
// column_standard_fields.
type tableSpecInput struct {
	TableSpec            `yaml:",inline" json:",inline"`
	LegacyColumnConcepts *map[string]string `yaml:"column_concepts,omitempty" json:"column_concepts,omitempty"`
}

// ParseTableManifest strict-decodes one apply-compatible Table document while
// tolerating the status block emitted by qctl get table -o yaml. A
// spec.column_concepts is returned as Spec.ColumnStandardFields; setting both
// keys exits 2.
func ParseTableManifest(data []byte) (*TableManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "Table" { //nolint:goconst // The wire kind belongs to this manifest type.
		return nil, qerrors.NewBadUsagef("expected kind 'Table', got '%s'", generic.Kind)
	}

	var tolerant tableManifestInput
	if err := pkgmanifest.StrictUnmarshal(data, &tolerant); err != nil {
		if hasSettingsModelPiiColumns(data) {
			return nil, qerrors.NewBadUsage("spec.settings_model.pii_columns has moved to spec.pii_columns: the list now belongs to the table, not its settings model, so move the key up one level")
		}
		return nil, err
	}
	spec := tolerant.Spec.TableSpec
	if legacy := tolerant.Spec.LegacyColumnConcepts; legacy != nil {
		if spec.ColumnStandardFields != nil {
			return nil, qerrors.NewBadUsage("set spec.column_standard_fields or spec.column_concepts, not both")
		}
		spec.ColumnStandardFields = legacy
	}
	return &TableManifest{
		APIVersion:      tolerant.APIVersion,
		Kind:            tolerant.Kind,
		Metadata:        tolerant.Metadata,
		Spec:            spec,
		status:          tolerant.Status,
		clearKeyColumns: settingsModelKeyColumnsNull(data),
	}, nil
}

// settingsModelKeyColumnsNull reports whether data writes
// spec.settings_model.key_columns as null.
func settingsModelKeyColumnsNull(data []byte) bool {
	var doc struct {
		Spec struct {
			SettingsModel map[string]any `yaml:"settings_model"`
		} `yaml:"spec"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	value, ok := doc.Spec.SettingsModel["key_columns"]
	return ok && value == nil
}

// hasSettingsModelPiiColumns reports whether data sets spec.settings_model.pii_columns.
func hasSettingsModelPiiColumns(data []byte) bool {
	var doc struct {
		Spec struct {
			SettingsModel map[string]any `yaml:"settings_model"`
		} `yaml:"spec"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	_, ok := doc.Spec.SettingsModel["pii_columns"]
	return ok
}

// ValidationError represents a manifest validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Validate ensures the manifest has the required fields for apply.
func (m *TableManifest) Validate() []ValidationError {
	var errs []ValidationError

	if m.APIVersion == "" {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: "required field is missing"})
	} else if m.APIVersion != pkgmanifest.APIVersionV1 {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: "must be 'qluster.ai/v1'"})
	}

	if m.Kind == "" {
		errs = append(errs, ValidationError{Field: "kind", Message: "required field is missing"})
	} else if m.Kind != "Table" {
		errs = append(errs, ValidationError{Field: "kind", Message: "must be 'Table'"})
	}

	if m.Metadata.Name == "" {
		errs = append(errs, ValidationError{Field: "metadata.name", Message: "required field is missing"})
	}

	if m.Spec.DestinationID <= 0 {
		errs = append(errs, ValidationError{Field: "spec.destination_id", Message: "must be a positive integer"})
	}
	if m.Spec.BackupSettingsID <= 0 {
		errs = append(errs, ValidationError{Field: "spec.backup_settings_id", Message: "must be a positive integer"})
	}
	if m.Spec.DatabaseName == "" {
		errs = append(errs, ValidationError{Field: "spec.database_name", Message: "required field is missing"})
	}
	if m.Spec.TableName == "" {
		errs = append(errs, ValidationError{Field: "spec.table_name", Message: "required field is missing"})
	}
	errs = append(errs, validateTableAssignments(m.Spec)...)

	return errs
}

func validateTableAssignments(spec TableSpec) []ValidationError {
	var errs []ValidationError
	if spec.TableKinds != nil {
		for _, slug := range *spec.TableKinds {
			if slug == "" {
				errs = append(errs, ValidationError{Field: "spec.table_kinds", Message: "slugs must not be empty"})
			}
		}
	}
	if spec.ColumnStandardFields != nil {
		for column, slug := range *spec.ColumnStandardFields {
			if column == "" || slug == "" {
				errs = append(errs, ValidationError{Field: "spec.column_standard_fields", Message: "columns and standard field slugs must not be empty"})
			}
		}
	}

	return errs
}

// validateFullCreate checks the fields required when using the full create route.
func (m *TableManifest) validateFullCreate() []ValidationError {
	var errs []ValidationError
	if m.Spec.SchemaName == "" {
		errs = append(errs, ValidationError{Field: "spec.schema_name", Message: "required for a full table create"})
	}
	if m.Spec.MigrationPolicy == "" {
		errs = append(errs, ValidationError{Field: "spec.migration_policy", Message: "required for a full table create"})
	}
	if m.Spec.DataLoadingProcess == "" {
		errs = append(errs, ValidationError{Field: "spec.data_loading_process", Message: "required for a full table create"})
	}
	return errs
}

// APIResponseToManifest converts a dataset API response into a manifest structure
func APIResponseToManifest(resp *DatasetFull) (*TableManifestWithStatus, error) {
	orgID := resp.OrganizationID.String()

	settings, err := settingsSchemaToSettingsModel(&resp.SettingsModel)
	if err != nil {
		return nil, err
	}
	tableKinds := make([]string, 0)
	if resp.ProblemDomains != nil {
		for _, kind := range *resp.ProblemDomains {
			tableKinds = append(tableKinds, kind.Slug)
		}
	}
	columnStandardFields := make(map[string]string)
	if resp.ColumnConcepts != nil {
		for _, assignment := range *resp.ColumnConcepts {
			columnStandardFields[assignment.DatasetCol] = assignment.Concept.Slug
		}
	}

	manifest := &TableManifestWithStatus{
		TableManifest: TableManifest{
			APIVersion: pkgmanifest.APIVersionV1,
			Kind:       "Table",
			Metadata: pkgmanifest.Metadata{
				Name: resp.Name,
			},
			Spec: TableSpec{
				DestinationID:      resp.DestinationID,
				DatabaseName:       resp.DatabaseName,
				SchemaName:         resp.SchemaName,
				TableName:          resp.TableName,
				MigrationPolicy:    resp.MigrationPolicy,
				DataLoadingProcess: resp.DataLoadingProcess,
				BackupSettingsID:   resp.BackupSettingsID,
				BackupKeyFormat:    resp.BackupKeyFormat,

				CompressionTypeOfBackupData: resp.CompressionTypeOfBackupData,

				AnomalyThreshold:  intPointer(resp.AnomalyThreshold),
				MaxRetryCount:     intPointer(resp.MaxRetryCount),
				MaxTriesToFixJSON: intPointer(resp.MaxTriesToFixJSON),

				DetectAnomalies:                                 resp.DetectAnomalies,
				StrictlyOneDatetimeFormatInAColumn:              resp.StrictlyOneDatetimeFormatInAColumn,
				GuessDatetimeFormatInIngestion:                  resp.GuessDatetimeFormatInIngestion,
				EnableCellMoveSuggestions:                       resp.EnableCellMoveSuggestions,
				EncryptRawDataDuringBackup:                      resp.EncryptRawDataDuringBackup,
				QuarantineRowsUntilApproved:                     resp.QuarantineRowsUntilApproved,
				RemoveOutliersWhenRecommendingNumericValidators: resp.RemoveOutliersWhenRecommendingNumericValidators,

				ColumnsForEntityResolution: resp.ColumnsForEntityResolution,
				PiiColumns:                 piiColumnsOrNil(resp.PiiColumns),
				ModelLabels:                resp.ModelLabels,
				TableKinds:                 &tableKinds,
				ColumnStandardFields:       &columnStandardFields,

				SettingsModel: settings,
			},
		},
		Status: &TableStatus{
			ID:              resp.ID,
			State:           resp.State,
			VersionID:       resp.VersionID,
			OrganizationID:  orgID,
			DestinationID:   resp.DestinationID,
			DestinationName: resp.DestinationName,
			CleanRowsCount:  resp.CleanRowsCount,
			BadRowsCount:    resp.BadRowsCount,
			PiiVisibility:   NewPiiVisibility(resp.PiiVisibility),

			EffectivePiiColumns: effectivePiiColumns(resp.EffectivePiiColumns),
			PiiColumnSources:    NewPiiColumnSources(resp.PiiColumnSources),
		},
	}

	return manifest, nil
}

// effectivePiiColumns returns *cols, or nil when cols is nil.
func effectivePiiColumns(cols *[]string) []string {
	if cols == nil {
		return nil
	}
	return *cols
}

// piiColumnsOrNil returns a pointer to cols, or nil when cols is empty.
func piiColumnsOrNil(cols []string) *[]string {
	if len(cols) == 0 {
		return nil
	}
	return &cols
}

// settingsSchemaToSettingsModel converts the settings model an API response
// carried into its manifest form. It cannot fail for any settings model a
// server actually sent -- both types are JSON all the way down and the manifest
// is generated from the wire type -- but a stale settings_model.gen.go could
// make it, so the error is reported rather than swallowed into an empty
// settings block. TestSettingsModelMirrorsTheWireSchema is what keeps that file
// current.
//
// Going through JSON is also what makes the read FAITHFUL, which the
// hand-written converter was not. It carried a list only when len > 0 and a
// scalar only when non-zero, so a table whose owner had cleared a list read back
// as a manifest with the key absent. For an update that was harmless -- since
// qctl-55o the PATCH is seeded from the read, so an absent key keeps the stored
// value -- but `qctl get table <id> -o yaml` is also the input to the clone
// workflow, and api.NewSettingsSchema declares omitempty only on xls_date_mode:
// an absent key went out as null, atlas set_field skips a null on a NOT NULL
// column (cettings/cettings/db.py), and the Python default landed. Eight of the
// settings those guards could drop have a NON-EMPTY default -- null_values,
// boolean_true, boolean_false, the two ignore_lines_that_include_only_subset_of_*
// lists, ignore_fields_in_signature_calculation, datetime_allowed_characters and
// decimal_field_padding -- so an explicitly emptied one came back populated on
// the clone with no warning at all (qctl-m5d). Most of the model's other columns
// have a non-empty default too; what made these eight the damage is that a
// legitimate value for them was one the guards threw away.
func settingsSchemaToSettingsModel(ss *SettingsSchema) (*SettingsModel, error) {
	if ss == nil {
		return nil, nil
	}

	// A response that carried NO settings model has no manifest form, and the
	// faithful read is what makes saying so necessary. api.DataSetSchemaFull
	// declares SettingsModel as a value rather than a pointer, so an absent or
	// null settings_model decodes to the zero struct with no error -- and every
	// required key on that struct then reads back as a legitimate zero:
	// decimal_field_padding: 0, datetime_allowed_characters: "", and
	// xls_date_mode: "", which is not a member of the enum at all (mode_0 and
	// mode_1 are). Applying that manifest would be refused on the enum, so the
	// key set the guards used to drop is the wrong thing to hand back here.
	//
	// The id is the sentinel for the same reason applyPatchStructuredOverrides
	// uses it: a settings model's version_id is legitimately 0, its primary key
	// never is. Both agree the state is unreachable through today's contract --
	// atlas declares `settings_model: SettingsSchema` on the detail response,
	// required and non-nullable (DataSetSchemaFull,
	// capi/capi/schemas/settings_schema.py) -- and
	// both handle it anyway, because the alternative is a manifest that looks
	// applyable and is not.
	if ss.ID == 0 {
		return nil, nil
	}

	sm := &SettingsModel{}
	if err := transcodeJSON(ss, sm); err != nil {
		return nil, fmt.Errorf("failed to read the table's settings model: %w", err)
	}
	return sm, nil
}
