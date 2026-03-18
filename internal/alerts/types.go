package alerts

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

func boolValue(v *bool) bool {
	return v != nil && *v
}

func pointerIntValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func pointerString(val string) *string {
	return &val
}

// AlertMetadata holds metadata for an alert resource
type AlertMetadata struct {
	// ID is the unique identifier for the alert
	ID int32 `yaml:"id" json:"id"`
}

// AlertUserInfo holds simplified user information for alerts
type AlertUserInfo struct {
	ID    string `yaml:"id" json:"id"`
	Email string `yaml:"email" json:"email"`
}

// DependentAlertInfo holds information about a dependent alert
type DependentAlertInfo struct {
	Message string `yaml:"message" json:"message"`
	ID      int    `yaml:"id" json:"id"`
}

// ValueRecommendation holds a suggested value with confidence indicator
type ValueRecommendation struct {
	Value  string `yaml:"value" json:"value"`
	Strong *bool  `yaml:"strong,omitempty" json:"strong,omitempty"`
}

// DialectInfoDisplay holds CSV dialect information for display
type DialectInfoDisplay struct {
	Delimiter *string `yaml:"delimiter,omitempty" json:"delimiter,omitempty"`
	Quotechar *string `yaml:"quotechar,omitempty" json:"quotechar,omitempty"`
	Escapechar *string `yaml:"escapechar,omitempty" json:"escapechar,omitempty"`
}

// AlertBodyInfo holds human-readable body fields surfaced in Tier 2 (-v)
type AlertBodyInfo struct {
	FileName                     string               `yaml:"file_name,omitempty" json:"file_name,omitempty"`
	AnomalyScore                 *int                 `yaml:"anomaly_score,omitempty" json:"anomaly_score,omitempty"`
	AllowedValues                []string             `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
	TopSuggestedValues           []ValueRecommendation `yaml:"top_suggested_values,omitempty" json:"top_suggested_values,omitempty"`
	RulePath                     []string             `yaml:"rule_path,omitempty" json:"rule_path,omitempty"`
	DetectedHeaderLineNo         *int                 `yaml:"detected_header_line_no,omitempty" json:"detected_header_line_no,omitempty"`
	DialectInfo                  *DialectInfoDisplay  `yaml:"dialect_info,omitempty" json:"dialect_info,omitempty"`
	DuplicatesHeaders            map[string]string    `yaml:"duplicates_headers,omitempty" json:"duplicates_headers,omitempty"`
	InvalidChars                 []string             `yaml:"invalid_chars,omitempty" json:"invalid_chars,omitempty"`
	RecommendedSettingsFieldName string               `yaml:"recommended_settings_field_name,omitempty" json:"recommended_settings_field_name,omitempty"`
	RecommendedSettingsValue     interface{}          `yaml:"recommended_settings_value,omitempty" json:"recommended_settings_value,omitempty"`
}

// AlertSpec defines the specification/details of an alert
type AlertSpec struct {
	// IssueType is the type/category of alert (many alerts can share the same type)
	IssueType string `yaml:"issue_type" json:"issue_type"`

	// Message is what we show to the user
	Message string `yaml:"message,omitempty" json:"message,omitempty"`

	// Settings reference (Tier 3 only)
	SettingsModelID int32 `yaml:"settings_model_id,omitempty" json:"settings_model_id,omitempty"`

	// Dataset information
	DatasetID   int32  `yaml:"dataset_id" json:"dataset_id"`
	DatasetName string `yaml:"dataset_name,omitempty" json:"dataset_name,omitempty"`

	// Data source information
	DataSourceModelID   int32  `yaml:"data_source_model_id,omitempty" json:"data_source_model_id,omitempty"`
	DataSourceModelName string `yaml:"data_source_model_name,omitempty" json:"data_source_model_name,omitempty"`

	// Migration information (Tier 2+)
	MigrationModelID int32 `yaml:"migration_model_id,omitempty" json:"migration_model_id,omitempty"`

	// Field information
	FieldName         string `yaml:"field_name,omitempty" json:"field_name,omitempty"`
	FieldValue        string `yaml:"field_value,omitempty" json:"field_value,omitempty"`
	AnotherFieldName  string `yaml:"another_field_name,omitempty" json:"another_field_name,omitempty"`
	AnotherFieldValue string `yaml:"another_field_value,omitempty" json:"another_field_value,omitempty"`

	// Publisher and debugging (Tier 2+)
	Publisher  string `yaml:"publisher,omitempty" json:"publisher,omitempty"`
	StackTrace string `yaml:"stack_trace,omitempty" json:"stack_trace,omitempty"`

	// URLs (Tier 2+)
	RedirectURL string `yaml:"redirect_url,omitempty" json:"redirect_url,omitempty"`

	// Blocking flags (shown only when true via omitempty)
	BlocksIngestionForDataSource bool `yaml:"blocks_ingestion_for_data_source,omitempty" json:"blocks_ingestion_for_data_source,omitempty"`
	BlocksIngestionForDataset    bool `yaml:"blocks_ingestion_for_dataset,omitempty" json:"blocks_ingestion_for_dataset,omitempty"`
	BlocksProfiling              bool `yaml:"blocks_profiling,omitempty" json:"blocks_profiling,omitempty"`
	BlocksStoredItem             bool `yaml:"blocks_stored_item,omitempty" json:"blocks_stored_item,omitempty"`

	// Resolution configuration (Tier 2+)
	ResolvableByUser      bool `yaml:"resolvable_by_user,omitempty" json:"resolvable_by_user,omitempty"`
	ResolveAfterMigration bool `yaml:"resolve_after_migration,omitempty" json:"resolve_after_migration,omitempty"`

	// Available actions
	Actions []string `yaml:"actions,omitempty" json:"actions,omitempty"`

	// Ingestion job IDs affected (Tier 2+)
	IngestionJobIDs []int `yaml:"ingestion_job_ids,omitempty" json:"ingestion_job_ids,omitempty"`

	// User information
	AssignedUser *AlertUserInfo `yaml:"assigned_user,omitempty" json:"assigned_user,omitempty"`
	Whodunit     *AlertUserInfo `yaml:"whodunit,omitempty" json:"whodunit,omitempty"`

	// Dependent alert information
	DependentAlert *DependentAlertInfo `yaml:"dependent_alert,omitempty" json:"dependent_alert,omitempty"`

	// Body highlights (Tier 2: -v)
	Body *AlertBodyInfo `yaml:"body,omitempty" json:"body,omitempty"`
}

// AlertStatus holds runtime status information
type AlertStatus struct {
	Resolved           bool   `yaml:"resolved" json:"resolved"`
	IsRowLevel         bool   `yaml:"is_row_level,omitempty" json:"is_row_level,omitempty"`
	Count              int32  `yaml:"count" json:"count"`
	AffectingRowsCount int32  `yaml:"affecting_rows_count,omitempty" json:"affecting_rows_count,omitempty"`
	AffectedFilesCount int32  `yaml:"affected_files_count,omitempty" json:"affected_files_count,omitempty"`
	CreatedAt          string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	ResolvedAt         string `yaml:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	ExpiredAt          string `yaml:"expired_at,omitempty" json:"expired_at,omitempty"`
}

// AlertManifestWithStatus is the manifest format for displaying alerts
type AlertManifestWithStatus struct {
	Status     *AlertStatus  `yaml:"status,omitempty" json:"status,omitempty"`
	Spec       *AlertSpec    `yaml:"spec" json:"spec"`
	Metadata   AlertMetadata `yaml:"metadata" json:"metadata"`
	APIVersion string        `yaml:"apiVersion" json:"apiVersion"`
	Kind       string        `yaml:"kind" json:"kind"`
}

// AlertRawManifest is the manifest format for -vv raw dump output.
// It embeds the full API response as-is for debugging/automation.
type AlertRawManifest struct {
	RawResponse interface{}   `yaml:"raw_response" json:"raw_response"`
	Metadata    AlertMetadata `yaml:"metadata" json:"metadata"`
	APIVersion  string        `yaml:"apiVersion" json:"apiVersion"`
	Kind        string        `yaml:"kind" json:"kind"`
}

// APIResponseToManifest converts an API AlertSchema to an AlertManifestWithStatus.
// The verbosity parameter controls which fields are included:
//   - 0: Essential fields only (Tier 1) — omits debugging/niche fields
//   - 1: Full details + body highlights (Tier 2)
//   - 2+: Returns nil (caller should use APIResponseToRawManifest for raw dump)
func APIResponseToManifest(resp *AlertFull, verbosity int) *AlertManifestWithStatus {
	spec := &AlertSpec{
		IssueType:                    string(resp.IssueType),
		Message:                      resp.Msg,
		DatasetID:                    int32(resp.DatasetID),
		DatasetName:                  resp.DatasetName,
		BlocksIngestionForDataSource: boolValue(resp.BlocksIngestionForDataSource),
		BlocksIngestionForDataset:    boolValue(resp.BlocksIngestionForDataset),
		BlocksProfiling:              boolValue(resp.BlocksProfiling),
		BlocksStoredItem:             boolValue(resp.BlocksStoredItem),
	}

	// Optional string fields shown in all tiers (Tier 1+)
	if resp.FieldName != nil {
		spec.FieldName = *resp.FieldName
	}
	if resp.DataSourceModelName != nil {
		spec.DataSourceModelName = *resp.DataSourceModelName
	}

	// Assigned user (shown in all tiers when present)
	if resp.AssignedUser != nil {
		spec.AssignedUser = &AlertUserInfo{
			ID:    resp.AssignedUser.ID.String(),
			Email: resp.AssignedUser.Email,
		}
	}

	// Dependent alert (shown in all tiers when present)
	if resp.DependentAlert != nil {
		spec.DependentAlert = &DependentAlertInfo{
			ID:      resp.DependentAlert.ID,
			Message: resp.DependentAlert.Msg,
		}
	}

	// Alert actions (shown in all tiers when present)
	if resp.AlertActionsList != nil {
		actions := make([]string, 0, len(*resp.AlertActionsList))
		for _, action := range *resp.AlertActionsList {
			actions = append(actions, string(action))
		}
		spec.Actions = actions
	}

	// --- Tier 2 fields: shown only with -v (verbosity >= 1) ---
	if verbosity >= 1 {
		spec.DataSourceModelID = int32(pointerIntValue(resp.DataSourceModelID))
		spec.MigrationModelID = int32(pointerIntValue(resp.MigrationModelID))
		spec.ResolvableByUser = boolValue(resp.ResolvableByUser)
		spec.ResolveAfterMigration = boolValue(resp.ResolveAfterMigration)

		if resp.FieldValue != nil {
			spec.FieldValue = *resp.FieldValue
		}
		if resp.AnotherFieldName != nil {
			spec.AnotherFieldName = *resp.AnotherFieldName
		}
		if resp.AnotherFieldValue != nil {
			spec.AnotherFieldValue = *resp.AnotherFieldValue
		}
		if resp.Publisher != nil {
			spec.Publisher = *resp.Publisher
		}
		if resp.StackTrace != nil {
			spec.StackTrace = *resp.StackTrace
		}
		if resp.RedirectURL != nil {
			spec.RedirectURL = *resp.RedirectURL
		}

		// Whodunit (Tier 2+)
		if resp.Whodunit != nil {
			spec.Whodunit = &AlertUserInfo{
				ID:    resp.Whodunit.ID.String(),
				Email: resp.Whodunit.Email,
			}
		}

		// Ingestion job IDs (Tier 2+)
		if resp.IngestionJobIds != nil {
			spec.IngestionJobIDs = *resp.IngestionJobIds
		}

		// Body highlights (Tier 2+)
		spec.Body = buildBodyInfo(resp)
	}

	// Build status
	status := &AlertStatus{
		Resolved:           boolValue(resp.Resolved),
		IsRowLevel:         resp.IsRowLevel,
		Count:              int32(resp.Count),
		AffectingRowsCount: int32(pointerIntValue(resp.AffectingRowsCount)),
	}

	// Affected files count
	if resp.StoredItemsToAlerts != nil {
		status.AffectedFilesCount = int32(len(*resp.StoredItemsToAlerts))
	}

	// Timestamps - use human-readable relative format for Tier 1 and Tier 2
	if resp.CreatedAt != nil {
		status.CreatedAt = timeutil.FormatRelative(*resp.CreatedAt)
	}
	if resp.ResolvedAt != nil {
		status.ResolvedAt = timeutil.FormatRelative(*resp.ResolvedAt)
	}

	// Expired at (Tier 2+ only)
	if verbosity >= 1 && resp.ExpiredAt != nil {
		status.ExpiredAt = timeutil.FormatRelative(*resp.ExpiredAt)
	}

	return &AlertManifestWithStatus{
		APIVersion: "qluster.ai/v1",
		Kind:       "Alert",
		Metadata: AlertMetadata{
			ID: int32(resp.ID),
		},
		Spec:   spec,
		Status: status,
	}
}

// APIResponseToRawManifest converts an API AlertSchema to a raw manifest
// for -vv (Tier 3) output. It includes the full API response as-is.
// The response is marshaled to JSON and back to interface{} so that
// both YAML and JSON encoders can handle it natively.
func APIResponseToRawManifest(resp *AlertFull) (*AlertRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal alert response: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal alert response: %w", err)
	}

	return &AlertRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "Alert",
		Metadata: AlertMetadata{
			ID: int32(resp.ID),
		},
		RawResponse: rawData,
	}, nil
}

// buildBodyInfo extracts human-readable fields from the alert body.
// Returns nil if no body fields are present.
func buildBodyInfo(resp *AlertFull) *AlertBodyInfo {
	if resp.Body == nil {
		return nil
	}
	body := resp.Body

	info := &AlertBodyInfo{}
	hasContent := false

	if body.FileName != nil && *body.FileName != "" {
		info.FileName = *body.FileName
		hasContent = true
	}
	if body.AnomalyScore != nil {
		info.AnomalyScore = body.AnomalyScore
		hasContent = true
	}
	if body.AllowedValues != nil && len(*body.AllowedValues) > 0 {
		info.AllowedValues = *body.AllowedValues
		hasContent = true
	}
	if body.TopSuggestedValues != nil && len(*body.TopSuggestedValues) > 0 {
		recs := make([]ValueRecommendation, 0, len(*body.TopSuggestedValues))
		for _, v := range *body.TopSuggestedValues {
			recs = append(recs, ValueRecommendation{
				Value:  v.Value,
				Strong: v.Strong,
			})
		}
		info.TopSuggestedValues = recs
		hasContent = true
	}
	if body.RulePath != nil && len(*body.RulePath) > 0 {
		info.RulePath = *body.RulePath
		hasContent = true
	}
	if body.DetectedHeaderLineNo != nil {
		info.DetectedHeaderLineNo = body.DetectedHeaderLineNo
		hasContent = true
	}
	if body.DialectInfo != nil {
		info.DialectInfo = &DialectInfoDisplay{
			Delimiter:  body.DialectInfo.CsvDelimiter,
			Quotechar:  body.DialectInfo.CsvQuotechar,
			Escapechar: body.DialectInfo.CsvEscapechar,
		}
		hasContent = true
	}
	if body.DuplicatesHeaders != nil && len(*body.DuplicatesHeaders) > 0 {
		info.DuplicatesHeaders = *body.DuplicatesHeaders
		hasContent = true
	}
	if body.InvalidChars != nil && len(*body.InvalidChars) > 0 {
		info.InvalidChars = *body.InvalidChars
		hasContent = true
	}
	if body.RecommendedSettingsFieldName != nil && *body.RecommendedSettingsFieldName != "" {
		info.RecommendedSettingsFieldName = *body.RecommendedSettingsFieldName
		hasContent = true
	}
	if body.RecommendedSettingsValue != nil {
		info.RecommendedSettingsValue = *body.RecommendedSettingsValue
		hasContent = true
	}

	if !hasContent {
		return nil
	}
	return info
}
