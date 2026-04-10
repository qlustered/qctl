package datasets

import (
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// SettingsModel defines the settings configuration for a table.
// This mirrors the NewSettingsSchema from the API for write operations.
type SettingsModel struct {
	ArrayDelimiters                              *[]string            `yaml:"array_delimiters,omitempty" json:"array_delimiters,omitempty"`
	BooleanFalse                                 *[]string            `yaml:"boolean_false,omitempty" json:"boolean_false,omitempty"`
	BooleanFalsePerColumn                        map[string][]string  `yaml:"boolean_false_per_column,omitempty" json:"boolean_false_per_column,omitempty"`
	BooleanTrue                                  *[]string            `yaml:"boolean_true,omitempty" json:"boolean_true,omitempty"`
	BooleanTruePerColumn                         map[string][]string  `yaml:"boolean_true_per_column,omitempty" json:"boolean_true_per_column,omitempty"`
	DataMarkedAsValidForDBField                  map[string][]string  `yaml:"data_marked_as_valid_for_db_field,omitempty" json:"data_marked_as_valid_for_db_field,omitempty"`
	DatetimeAllowedCharacters                    *string              `yaml:"datetime_allowed_characters,omitempty" json:"datetime_allowed_characters,omitempty"`
	DatetimeFormats                              *[]string            `yaml:"datetime_formats,omitempty" json:"datetime_formats,omitempty"`
	DatetimeFormatsPerColumn                     map[string][]string  `yaml:"datetime_formats_per_column,omitempty" json:"datetime_formats_per_column,omitempty"`
	DecimalFieldPadding                          *int                 `yaml:"decimal_field_padding,omitempty" json:"decimal_field_padding,omitempty"`
	DefaultValueForFieldWhenCastingError         map[string]any       `yaml:"default_value_for_field_when_casting_error,omitempty" json:"default_value_for_field_when_casting_error,omitempty"`
	DollarToCent                                 *bool                `yaml:"dollar_to_cent,omitempty" json:"dollar_to_cent,omitempty"`
	DollarValueIfWordInFieldName                 *[]string            `yaml:"dollar_value_if_word_in_field_name,omitempty" json:"dollar_value_if_word_in_field_name,omitempty"`
	EnableInteger                                *bool                `yaml:"enable_integer,omitempty" json:"enable_integer,omitempty"`
	EnableSmallInteger                           *bool                `yaml:"enable_small_integer,omitempty" json:"enable_small_integer,omitempty"`
	EncryptColumns                               *[]string            `yaml:"encrypt_columns,omitempty" json:"encrypt_columns,omitempty"`
	FieldNameFullConversion                      map[string]string    `yaml:"field_name_full_conversion,omitempty" json:"field_name_full_conversion,omitempty"`
	FieldNamePartConversion                      map[string]string    `yaml:"field_name_part_conversion,omitempty" json:"field_name_part_conversion,omitempty"`
	FieldsToExpand                               *[]string            `yaml:"fields_to_expand,omitempty" json:"fields_to_expand,omitempty"`
	IgnoreColumnNames                            *[]string            `yaml:"ignore_column_names,omitempty" json:"ignore_column_names,omitempty"`
	IgnoreFieldsInSignatureCalculation           *[]string            `yaml:"ignore_fields_in_signature_calculation,omitempty" json:"ignore_fields_in_signature_calculation,omitempty"`
	IgnoreLinesThatIncludeOnlySubsetOfCharacters *[]string            `yaml:"ignore_lines_that_include_only_subset_of_characters,omitempty" json:"ignore_lines_that_include_only_subset_of_characters,omitempty"`
	IgnoreLinesThatIncludeOnlySubsetOfWords      *[]string            `yaml:"ignore_lines_that_include_only_subset_of_words,omitempty" json:"ignore_lines_that_include_only_subset_of_words,omitempty"`
	IgnoreMatchers                               map[string][]string  `yaml:"ignore_matchers,omitempty" json:"ignore_matchers,omitempty"`
	IgnoreNotSeenBeforeFieldsWhenImporting       *bool                `yaml:"ignore_not_seen_before_fields_when_importing,omitempty" json:"ignore_not_seen_before_fields_when_importing,omitempty"`
	InferDatetime                                *bool                `yaml:"infer_datetime,omitempty" json:"infer_datetime,omitempty"`
	InferDatetimeForColumns                      *[]string            `yaml:"infer_datetime_for_columns,omitempty" json:"infer_datetime_for_columns,omitempty"`
	MaxBoundaryElementLength                     *int                 `yaml:"max_boundary_element_length,omitempty" json:"max_boundary_element_length,omitempty"`
	MonetaryColumnsOverride                      map[string]bool      `yaml:"monetary_columns_override,omitempty" json:"monetary_columns_override,omitempty"`
	NonStringFieldsAreAllNullable                *bool                `yaml:"non_string_fields_are_all_nullable,omitempty" json:"non_string_fields_are_all_nullable,omitempty"`
	NullValues                                   *[]string            `yaml:"null_values,omitempty" json:"null_values,omitempty"`
	NullValuesPerColumn                          map[string][]string  `yaml:"null_values_per_column,omitempty" json:"null_values_per_column,omitempty"`
	PercentToDecimal                             *bool                `yaml:"percent_to_decimal,omitempty" json:"percent_to_decimal,omitempty"`
	StringFieldPadding                           *int                 `yaml:"string_field_padding,omitempty" json:"string_field_padding,omitempty"`
	StringFieldsCanBeNullable                    *bool                `yaml:"string_fields_can_be_nullable,omitempty" json:"string_fields_can_be_nullable,omitempty"`
	TrimStringInsteadOfRaisingErr                *bool                `yaml:"trim_string_instead_of_raising_err,omitempty" json:"trim_string_instead_of_raising_err,omitempty"`
	UseTextInsteadOfString                       *bool                `yaml:"use_text_instead_of_string,omitempty" json:"use_text_instead_of_string,omitempty"`
	ValueMapping                                 map[string]string    `yaml:"value_mapping,omitempty" json:"value_mapping,omitempty"`
	ValueMappingPerColumn                        map[string]map[string]string `yaml:"value_mapping_per_column,omitempty" json:"value_mapping_per_column,omitempty"`
	XlsDateMode                                  *string              `yaml:"xls_date_mode,omitempty" json:"xls_date_mode,omitempty"`
}

// TableSpec defines the declarative configuration for a table
type TableSpec struct {
	DestinationID int `yaml:"destination_id" json:"destination_id"`

	DatabaseName string `yaml:"database_name" json:"database_name"`
	SchemaName   string `yaml:"schema_name" json:"schema_name"`
	TableName    string `yaml:"table_name" json:"table_name"`

	MigrationPolicy    MigrationPolicy    `yaml:"migration_policy" json:"migration_policy"`
	DataLoadingProcess DataLoadingProcess `yaml:"data_loading_process" json:"data_loading_process"`

	BackupSettingsID int    `yaml:"backup_settings_id" json:"backup_settings_id"`
	BackupKeyFormat  string `yaml:"backup_key_format,omitempty" json:"backup_key_format,omitempty"`

	AnomalyThreshold  *int `yaml:"anomaly_threshold,omitempty" json:"anomaly_threshold,omitempty"`
	MaxRetryCount     *int `yaml:"max_retry_count,omitempty" json:"max_retry_count,omitempty"`
	MaxTriesToFixJSON *int `yaml:"max_tries_to_fix_json,omitempty" json:"max_tries_to_fix_json,omitempty"`

	ShouldReprocess                                 *bool `yaml:"should_reprocess,omitempty" json:"should_reprocess,omitempty"`
	DetectAnomalies                                 *bool `yaml:"detect_anomalies,omitempty" json:"detect_anomalies,omitempty"`
	StrictlyOneDatetimeFormatInAColumn              *bool `yaml:"strictly_one_datetime_format_in_a_column,omitempty" json:"strictly_one_datetime_format_in_a_column,omitempty"`
	GuessDatetimeFormatInIngestion                  *bool `yaml:"guess_datetime_format_in_ingestion,omitempty" json:"guess_datetime_format_in_ingestion,omitempty"`
	EnableRowLogs                                   *bool `yaml:"enable_row_logs,omitempty" json:"enable_row_logs,omitempty"`
	EnableCellMoveSuggestions                       *bool `yaml:"enable_cell_move_suggestions,omitempty" json:"enable_cell_move_suggestions,omitempty"`
	EncryptRawDataDuringBackup                      *bool `yaml:"encrypt_raw_data_during_backup,omitempty" json:"encrypt_raw_data_during_backup,omitempty"`
	QuarantineRowsUntilApproved                     *bool `yaml:"quarantine_rows_until_approved,omitempty" json:"quarantine_rows_until_approved,omitempty"`
	RemoveOutliersWhenRecommendingNumericValidators *bool `yaml:"remove_outliers_when_recommending_numeric_validators,omitempty" json:"remove_outliers_when_recommending_numeric_validators,omitempty"`

	ColumnsForEntityResolution *[]string `yaml:"columns_for_entity_resolution,omitempty" json:"columns_for_entity_resolution,omitempty"`

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
}

// TableManifest is the declarative representation of a table
type TableManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       TableSpec            `yaml:"spec" json:"spec"`
}

// TableManifestWithStatus extends TableManifest with runtime status
type TableManifestWithStatus struct {
	TableManifest `yaml:",inline" json:",inline"`
	Status        *TableStatus `yaml:"status,omitempty" json:"status,omitempty"`
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
	if m.Spec.SchemaName == "" {
		errs = append(errs, ValidationError{Field: "spec.schema_name", Message: "required field is missing"})
	}
	if m.Spec.TableName == "" {
		errs = append(errs, ValidationError{Field: "spec.table_name", Message: "required field is missing"})
	}
	if m.Spec.MigrationPolicy == "" {
		errs = append(errs, ValidationError{Field: "spec.migration_policy", Message: "required field is missing"})
	}
	if m.Spec.DataLoadingProcess == "" {
		errs = append(errs, ValidationError{Field: "spec.data_loading_process", Message: "required field is missing"})
	}

	return errs
}

// APIResponseToManifest converts a dataset API response into a manifest structure
func APIResponseToManifest(resp *DatasetFull) *TableManifestWithStatus {
	orgID := resp.OrganizationID.String()

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
				AnomalyThreshold:   intPointer(resp.AnomalyThreshold),
				MaxRetryCount:      intPointer(resp.MaxRetryCount),
				MaxTriesToFixJSON:  intPointer(resp.MaxTriesToFixJSON),

				ShouldReprocess:                                 resp.ShouldReprocess,
				DetectAnomalies:                                 resp.DetectAnomalies,
				StrictlyOneDatetimeFormatInAColumn:              resp.StrictlyOneDatetimeFormatInAColumn,
				GuessDatetimeFormatInIngestion:                  resp.GuessDatetimeFormatInIngestion,
				EnableRowLogs:                                   resp.EnableRowLogs,
				EnableCellMoveSuggestions:                       resp.EnableCellMoveSuggestions,
				EncryptRawDataDuringBackup:                      resp.EncryptRawDataDuringBackup,
				QuarantineRowsUntilApproved:                     resp.QuarantineRowsUntilApproved,
				RemoveOutliersWhenRecommendingNumericValidators: resp.RemoveOutliersWhenRecommendingNumericValidators,

				ColumnsForEntityResolution: resp.ColumnsForEntityResolution,

				SettingsModel: settingsSchemaToSettingsModel(&resp.SettingsModel),
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
		},
	}

	return manifest
}

// settingsSchemaToSettingsModel converts an API SettingsSchema to a manifest SettingsModel.
// This is used when converting API responses to manifest format.
func settingsSchemaToSettingsModel(ss *SettingsSchema) *SettingsModel {
	if ss == nil {
		return nil
	}

	sm := &SettingsModel{
		ArrayDelimiters:                        ss.ArrayDelimiters,
		DatetimeFormats:                        ss.DatetimeFormats,
		FieldsToExpand:                         ss.FieldsToExpand,
		DollarToCent:                           ss.DollarToCent,
		EnableInteger:                          ss.EnableInteger,
		EnableSmallInteger:                     ss.EnableSmallInteger,
		IgnoreNotSeenBeforeFieldsWhenImporting: ss.IgnoreNotSeenBeforeFieldsWhenImporting,
		InferDatetime:                          ss.InferDatetime,
		NonStringFieldsAreAllNullable:          ss.NonStringFieldsAreAllNullable,
		PercentToDecimal:                       ss.PercentToDecimal,
		StringFieldsCanBeNullable:              ss.StringFieldsCanBeNullable,
		TrimStringInsteadOfRaisingErr:          ss.TrimStringInsteadOfRaisingErr,
		UseTextInsteadOfString:                 ss.UseTextInsteadOfString,
	}

	// Convert non-pointer arrays to pointer arrays (these are []string in SettingsSchema)
	if len(ss.InferDatetimeForColumns) > 0 {
		sm.InferDatetimeForColumns = &ss.InferDatetimeForColumns
	}
	if len(ss.NullValues) > 0 {
		sm.NullValues = &ss.NullValues
	}

	// Convert non-pointer arrays to pointer arrays
	if len(ss.BooleanFalse) > 0 {
		sm.BooleanFalse = &ss.BooleanFalse
	}
	if len(ss.BooleanTrue) > 0 {
		sm.BooleanTrue = &ss.BooleanTrue
	}
	if len(ss.DollarValueIfWordInFieldName) > 0 {
		sm.DollarValueIfWordInFieldName = &ss.DollarValueIfWordInFieldName
	}
	if len(ss.EncryptColumns) > 0 {
		sm.EncryptColumns = &ss.EncryptColumns
	}
	if len(ss.IgnoreColumnNames) > 0 {
		sm.IgnoreColumnNames = &ss.IgnoreColumnNames
	}
	if len(ss.IgnoreFieldsInSignatureCalculation) > 0 {
		sm.IgnoreFieldsInSignatureCalculation = &ss.IgnoreFieldsInSignatureCalculation
	}
	if len(ss.IgnoreLinesThatIncludeOnlySubsetOfCharacters) > 0 {
		sm.IgnoreLinesThatIncludeOnlySubsetOfCharacters = &ss.IgnoreLinesThatIncludeOnlySubsetOfCharacters
	}
	if len(ss.IgnoreLinesThatIncludeOnlySubsetOfWords) > 0 {
		sm.IgnoreLinesThatIncludeOnlySubsetOfWords = &ss.IgnoreLinesThatIncludeOnlySubsetOfWords
	}

	// Convert non-empty strings to pointers
	if ss.DatetimeAllowedCharacters != "" {
		sm.DatetimeAllowedCharacters = &ss.DatetimeAllowedCharacters
	}

	// Convert non-zero ints to pointers
	if ss.DecimalFieldPadding != 0 {
		sm.DecimalFieldPadding = &ss.DecimalFieldPadding
	}
	if ss.MaxBoundaryElementLength != 0 {
		sm.MaxBoundaryElementLength = &ss.MaxBoundaryElementLength
	}
	if ss.StringFieldPadding != 0 {
		sm.StringFieldPadding = &ss.StringFieldPadding
	}

	// Convert map pointers (already in correct format)
	if ss.BooleanFalsePerColumn != nil {
		sm.BooleanFalsePerColumn = *ss.BooleanFalsePerColumn
	}
	if ss.BooleanTruePerColumn != nil {
		sm.BooleanTruePerColumn = *ss.BooleanTruePerColumn
	}
	if ss.DataMarkedAsValidForDBField != nil {
		sm.DataMarkedAsValidForDBField = *ss.DataMarkedAsValidForDBField
	}
	if ss.DatetimeFormatsPerColumn != nil {
		sm.DatetimeFormatsPerColumn = *ss.DatetimeFormatsPerColumn
	}
	if ss.DefaultValueForFieldWhenCastingError != nil {
		sm.DefaultValueForFieldWhenCastingError = ss.DefaultValueForFieldWhenCastingError
	}
	if ss.IgnoreMatchers != nil {
		sm.IgnoreMatchers = *ss.IgnoreMatchers
	}
	if ss.MonetaryColumnsOverride != nil {
		sm.MonetaryColumnsOverride = *ss.MonetaryColumnsOverride
	}
	if ss.NullValuesPerColumn != nil {
		sm.NullValuesPerColumn = *ss.NullValuesPerColumn
	}

	// Convert non-pointer maps
	if len(ss.FieldNameFullConversion) > 0 {
		sm.FieldNameFullConversion = ss.FieldNameFullConversion
	}
	if len(ss.FieldNamePartConversion) > 0 {
		sm.FieldNamePartConversion = ss.FieldNamePartConversion
	}
	if len(ss.ValueMapping) > 0 {
		sm.ValueMapping = ss.ValueMapping
	}
	if len(ss.ValueMappingPerColumn) > 0 {
		sm.ValueMappingPerColumn = ss.ValueMappingPerColumn
	}

	// Handle XlsDateMode enum conversion (XlsDateMode is a non-pointer enum in SettingsSchema)
	if ss.XlsDateMode != "" {
		xlsMode := string(ss.XlsDateMode)
		sm.XlsDateMode = &xlsMode
	}

	return sm
}
