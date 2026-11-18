package datasets

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// TableConfig is a read-only view centered on the settings model, backup,
// migration policy and thresholds. It can also carry the not-anomaly ledger
// when a caller explicitly requests it.
//
// It is a second view over the same GET /datasets/{id} response the manifest
// comes from — nothing extra is fetched — and it exists because this is the
// part of a table nobody wants by default and everybody occasionally needs.
type TableConfig struct {
	Name string `json:"name" yaml:"name"`
	ID   int    `json:"id" yaml:"id"`

	NotAnomaliesPerColumn *map[string]any `json:"not_anomalies_per_column,omitempty" yaml:"not_anomalies_per_column,omitempty"`

	MigrationPolicy    string `json:"migration_policy" yaml:"migration_policy"`
	DataLoadingProcess string `json:"data_loading_process" yaml:"data_loading_process"`

	BackupSettingsID            int                  `json:"backup_settings_id" yaml:"backup_settings_id"`
	BackupKeyFormat             string               `json:"backup_key_format,omitempty" yaml:"backup_key_format,omitempty"`
	EncryptRawDataDuringBackup  *bool                `json:"encrypt_raw_data_during_backup,omitempty" yaml:"encrypt_raw_data_during_backup,omitempty"`
	CompressionTypeOfBackupData *api.CompressionType `json:"compression_type_of_backup_data,omitempty" yaml:"compression_type_of_backup_data,omitempty"`

	AnomalyThreshold                                int   `json:"anomaly_threshold" yaml:"anomaly_threshold"`
	DetectAnomalies                                 *bool `json:"detect_anomalies,omitempty" yaml:"detect_anomalies,omitempty"`
	RemoveOutliersWhenRecommendingNumericValidators *bool `json:"remove_outliers_when_recommending_numeric_validators,omitempty" yaml:"remove_outliers_when_recommending_numeric_validators,omitempty"`

	MaxRetryCount     int `json:"max_retry_count" yaml:"max_retry_count"`
	MaxTriesToFixJSON int `json:"max_tries_to_fix_json" yaml:"max_tries_to_fix_json"`

	QuarantineRowsUntilApproved        *bool `json:"quarantine_rows_until_approved,omitempty" yaml:"quarantine_rows_until_approved,omitempty"`
	EnableCellMoveSuggestions          *bool `json:"enable_cell_move_suggestions,omitempty" yaml:"enable_cell_move_suggestions,omitempty"`
	StrictlyOneDatetimeFormatInAColumn *bool `json:"strictly_one_datetime_format_in_a_column,omitempty" yaml:"strictly_one_datetime_format_in_a_column,omitempty"`
	GuessDatetimeFormatInIngestion     *bool `json:"guess_datetime_format_in_ingestion,omitempty" yaml:"guess_datetime_format_in_ingestion,omitempty"`

	ColumnsForEntityResolution *[]string `json:"columns_for_entity_resolution,omitempty" yaml:"columns_for_entity_resolution,omitempty"`

	SettingsModel *SettingsModel `json:"settings_model,omitempty" yaml:"settings_model,omitempty"`
}

// TableConfigFromAPIResponse projects the configuration half of a dataset
// response and, when requested, its not-anomaly ledger. The settings model goes
// through the same converter the manifest uses, so the two views cannot
// disagree about a settings field's shape.
func TableConfigFromAPIResponse(resp *DatasetFull, includeNotAnomalies bool) (*TableConfig, error) {
	settings, err := settingsSchemaToSettingsModel(&resp.SettingsModel)
	if err != nil {
		return nil, err
	}
	var notAnomaliesPerColumn *map[string]any
	if includeNotAnomalies {
		notAnomaliesPerColumn = resp.NotAnomaliesPerColumn
		if notAnomaliesPerColumn == nil {
			// Preserve an explicit null in structured output when the backend did
			// not return the deferred ledger. A nil pointer would omit the field
			// and make --show-not-anomalies indistinguishable from the default.
			var missing map[string]any
			notAnomaliesPerColumn = &missing
		}
	}

	return &TableConfig{
		Name:                  resp.Name,
		ID:                    resp.ID,
		NotAnomaliesPerColumn: notAnomaliesPerColumn,

		MigrationPolicy:             string(resp.MigrationPolicy),
		DataLoadingProcess:          string(resp.DataLoadingProcess),
		BackupSettingsID:            resp.BackupSettingsID,
		BackupKeyFormat:             resp.BackupKeyFormat,
		EncryptRawDataDuringBackup:  resp.EncryptRawDataDuringBackup,
		CompressionTypeOfBackupData: resp.CompressionTypeOfBackupData,

		AnomalyThreshold: resp.AnomalyThreshold,
		DetectAnomalies:  resp.DetectAnomalies,
		RemoveOutliersWhenRecommendingNumericValidators: resp.RemoveOutliersWhenRecommendingNumericValidators,

		MaxRetryCount:     resp.MaxRetryCount,
		MaxTriesToFixJSON: resp.MaxTriesToFixJSON,

		QuarantineRowsUntilApproved:        resp.QuarantineRowsUntilApproved,
		EnableCellMoveSuggestions:          resp.EnableCellMoveSuggestions,
		StrictlyOneDatetimeFormatInAColumn: resp.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:     resp.GuessDatetimeFormatInIngestion,

		ColumnsForEntityResolution: resp.ColumnsForEntityResolution,

		SettingsModel: settings,
	}, nil
}

// RenderTableConfig writes the text view: a header of the settings people
// actually change, the settings model verbatim and the optional not-anomaly
// ledger.
//
// The settings model is rendered by marshalling it rather than field by field.
// It has ~50 fields, every one of which the backend may add to, and a
// hand-written renderer would silently stop showing whatever was added next.
//
// EVERY key the server sent is printed, an empty list or map included, and that
// is a decision rather than an oversight. The read converter stopped dropping
// emptied values (qctl-m5d), which grows this block by ~17 lines on a stock
// table. Filtering the empties back out here would put the text view and
// `get table -o yaml` into disagreement about the same table, and it would hide
// the one state the change exists to make visible: a list the owner has
// explicitly CLEARED, which reads back as [] and means something different from
// a key nobody ever set. It also ends an inconsistency nobody could have
// explained: before this, an empty datetime_formats printed and an empty
// encrypt_columns did not, because the first is nullable on the wire and the
// second is not -- a distinction invisible from the CLI.
// TestRenderTableConfig_PrintsEmptySettings pins it.
func RenderTableConfig(cmd *cobra.Command, c *TableConfig) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "%-17s %s (id: %d)\n", "Table:", terminal.Sanitize(c.Name), c.ID)
	fmt.Fprintf(out, "%-17s %s\n", "Migration policy:", terminal.Sanitize(c.MigrationPolicy))
	fmt.Fprintf(out, "%-17s %s\n", "Loading:", terminal.Sanitize(c.DataLoadingProcess))

	backup := fmt.Sprintf("settings %d", c.BackupSettingsID)
	if c.BackupKeyFormat != "" {
		backup += ", key format " + c.BackupKeyFormat
	}
	backup += ", encrypt=" + formatBoolPtr(c.EncryptRawDataDuringBackup)
	if c.CompressionTypeOfBackupData != nil {
		backup += ", compression=" + string(*c.CompressionTypeOfBackupData)
	}
	fmt.Fprintf(out, "%-17s %s\n", "Backup:", terminal.Sanitize(backup))

	fmt.Fprintf(out, "%-17s detect=%s, threshold=%d, remove_outliers=%s\n", "Anomalies:",
		formatBoolPtr(c.DetectAnomalies), c.AnomalyThreshold,
		formatBoolPtr(c.RemoveOutliersWhenRecommendingNumericValidators))
	fmt.Fprintf(out, "%-17s max_retry_count=%d, max_tries_to_fix_json=%d\n", "Retries:",
		c.MaxRetryCount, c.MaxTriesToFixJSON)
	fmt.Fprintf(out, "%-17s quarantine_until_approved=%s\n", "Rows:",
		formatBoolPtr(c.QuarantineRowsUntilApproved))
	fmt.Fprintf(out, "%-17s cell_moves=%s, one_datetime_format=%s, guess_datetime=%s\n", "Ingestion:",
		formatBoolPtr(c.EnableCellMoveSuggestions),
		formatBoolPtr(c.StrictlyOneDatetimeFormatInAColumn),
		formatBoolPtr(c.GuessDatetimeFormatInIngestion))
	if c.ColumnsForEntityResolution != nil && len(*c.ColumnsForEntityResolution) > 0 {
		fmt.Fprintf(out, "%-17s %s\n", "Entity resolution:", terminal.Sanitize(strings.Join(*c.ColumnsForEntityResolution, ", ")))
	}

	fmt.Fprintln(out, "\nSETTINGS MODEL")
	if c.SettingsModel == nil {
		fmt.Fprintln(out, "  (none)")
	} else {
		raw, err := jsonnum.MarshalYAML(c.SettingsModel)
		if err != nil {
			return fmt.Errorf("failed to render the settings model: %w", err)
		}
		writeIndentedYAML(out, raw)
	}

	if c.NotAnomaliesPerColumn != nil {
		fmt.Fprintln(out, "\nNOT ANOMALIES PER COLUMN")
		raw, err := jsonnum.MarshalYAML(c.NotAnomaliesPerColumn)
		if err != nil {
			return fmt.Errorf("failed to render values marked as not anomalies: %w", err)
		}
		writeIndentedYAML(out, raw)
	}
	return nil
}

func writeIndentedYAML(out io.Writer, raw []byte) {
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		fmt.Fprintf(out, "  %s\n", terminal.Sanitize(line))
	}
}
