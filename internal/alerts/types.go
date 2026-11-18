package alerts

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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

// AlertMetadata holds metadata for an alert resource
type AlertMetadata struct {
	// ID is the unique identifier for the alert
	ID int32 `yaml:"id" json:"id"`
}

// AlertUserInfo holds simplified user information for alerts
type AlertUserInfo struct {
	ID    string `yaml:"id" json:"id"`
	Email string `yaml:"email" json:"email"`
	Role  string `yaml:"role,omitempty" json:"role,omitempty"`
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
	Delimiter  *string `yaml:"delimiter,omitempty" json:"delimiter,omitempty"`
	Quotechar  *string `yaml:"quotechar,omitempty" json:"quotechar,omitempty"`
	Escapechar *string `yaml:"escapechar,omitempty" json:"escapechar,omitempty"`
}

// PreloadCheckFindingInfo is one trigger the pre-load check tripped on a held
// file. Only the fields of its reason's trigger are set. ReplacedLoadedAt is
// RFC3339 in UTC.
type PreloadCheckFindingInfo struct {
	Reason               string   `yaml:"reason" json:"reason"`
	RowCount             *int     `yaml:"row_count,omitempty" json:"row_count,omitempty"`
	BaselineRowCount     *int     `yaml:"baseline_row_count,omitempty" json:"baseline_row_count,omitempty"`
	RowDropShare         *float64 `yaml:"row_drop_share,omitempty" json:"row_drop_share,omitempty"`
	MissingColumns       []string `yaml:"missing_columns,omitempty" json:"missing_columns,omitempty"`
	LargestBucket        string   `yaml:"largest_bucket,omitempty" json:"largest_bucket,omitempty"`
	LargestPeriodKey     *string  `yaml:"largest_period_key,omitempty" json:"largest_period_key,omitempty"`
	LargestRowCount      *int     `yaml:"largest_row_count,omitempty" json:"largest_row_count,omitempty"`
	RoutedPeriodKey      *string  `yaml:"routed_period_key,omitempty" json:"routed_period_key,omitempty"`
	RoutedRowCount       *int     `yaml:"routed_row_count,omitempty" json:"routed_row_count,omitempty"`
	NonBlankRowCount     *int     `yaml:"non_blank_row_count,omitempty" json:"non_blank_row_count,omitempty"`
	ReplacedStoredItemID *int     `yaml:"replaced_stored_item_id,omitempty" json:"replaced_stored_item_id,omitempty"`
	ReplacedFileName     string   `yaml:"replaced_file_name,omitempty" json:"replaced_file_name,omitempty"`
	ReplacedLoadedAt     string   `yaml:"replaced_loaded_at,omitempty" json:"replaced_loaded_at,omitempty"`
	ReplacedRowCount     *int     `yaml:"replaced_row_count,omitempty" json:"replaced_row_count,omitempty"`
	PeriodLabel          string   `yaml:"period_label,omitempty" json:"period_label,omitempty"`
}

// AlertBodyInfo holds selected alert body fields.
type AlertBodyInfo struct {
	// DeclaredSeverity is the severity the RULE gave this issue, not this
	// alert's own — the backend records it only when the rule instance's
	// spec.severity policy overrode it. Its bare value misreads in this
	// position (on an alert it can only say `warning`), which is what the
	// footer's DECLARED SEVERITY note (RenderDeclaredSeverity) is for; the
	// value is here so `-o json` carries it without the prose. Declaration
	// order buys nothing — describe renders the manifest through a map, so
	// both encoders sort these keys alphabetically.
	DeclaredSeverity             string                `yaml:"declared_severity,omitempty" json:"declared_severity,omitempty"`
	FileName                     string                `yaml:"file_name,omitempty" json:"file_name,omitempty"`
	AnomalyScore                 *int                  `yaml:"anomaly_score,omitempty" json:"anomaly_score,omitempty"`
	AllowedValues                []string              `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
	TopSuggestedValues           []ValueRecommendation `yaml:"top_suggested_values,omitempty" json:"top_suggested_values,omitempty"`
	RulePath                     []string              `yaml:"rule_path,omitempty" json:"rule_path,omitempty"`
	RelatedAlertIDs              []int                 `yaml:"related_alert_ids,omitempty" json:"related_alert_ids,omitempty"`
	PrefixCandidates             []string              `yaml:"prefix_candidates,omitempty" json:"prefix_candidates,omitempty"`
	DetectedHeaderLineNo         *int                  `yaml:"detected_header_line_no,omitempty" json:"detected_header_line_no,omitempty"`
	DialectInfo                  *DialectInfoDisplay   `yaml:"dialect_info,omitempty" json:"dialect_info,omitempty"`
	DuplicatesHeaders            map[string]string     `yaml:"duplicates_headers,omitempty" json:"duplicates_headers,omitempty"`
	InvalidChars                 []string              `yaml:"invalid_chars,omitempty" json:"invalid_chars,omitempty"`
	RecommendedSettingsFieldName string                `yaml:"recommended_settings_field_name,omitempty" json:"recommended_settings_field_name,omitempty"`
	RecommendedSettingsValue     interface{}           `yaml:"recommended_settings_value,omitempty" json:"recommended_settings_value,omitempty"`
	// RecommendedSettingsModuleToUpdate says WHICH object the recommended field
	// lives on, and the field name alone does not imply it: the backend
	// dispatches on this across five modules
	// (_update_configs_per_recommended_value, atlas
	// capi/capi/controllers/alert_controller.py), and the two producers today
	// disagree — `DataSet` for a migration_frozen alert's `migration_policy`
	// (Combiner._refuse_migration_if_locked, cmapper/cmapper/combiner.py) and
	// `SettingsModel` for a bad_settings alert's `max_boundary_element_length`
	// (BoundaryDataCollector.__init__, cmapper/cmapper/boundary_data.py). qctl
	// exposes both surfaces in one `kind: Table` manifest — top-level
	// `spec.migration_policy` versus `spec.settings_model.<key>` — so a reader
	// applying the recommendation by hand needs this to know which half to
	// edit. It is never noise: the action is offered only when all three keys
	// are present (filter_update_configs_to_recommended_value,
	// capi/capi/controllers/alert_actions.py).
	RecommendedSettingsModuleToUpdate string `yaml:"recommended_settings_module_to_update,omitempty" json:"recommended_settings_module_to_update,omitempty"`
	// Reason says why a series_file_routing alert's file waits for a
	// reporting period, or why an inbound_email_held alert holds an emailed
	// file (auth_failed or multiple_data_files). PeriodKey is the routing
	// alert's period when one is known, or a late_file alert's period.
	Reason                string `yaml:"reason,omitempty" json:"reason,omitempty"`
	FromAddress           string `yaml:"from_address,omitempty" json:"from_address,omitempty"`
	PeriodKey             string `yaml:"period_key,omitempty" json:"period_key,omitempty"`
	PipelineValue         *any   `yaml:"pipeline_value,omitempty" json:"pipeline_value,omitempty"`
	PipelineValueIsNumber *bool  `yaml:"pipeline_value_is_number,omitempty" json:"pipeline_value_is_number,omitempty"`
	BaseDisplacedValue    *any   `yaml:"base_displaced_value,omitempty" json:"base_displaced_value,omitempty"`
	// Findings are the triggers a preload_check_held alert's file tripped.
	// AskedUserID and NotAskedReason name the sender of the emailed file asked
	// about a preload_check_held or series_file_routing alert, or why nobody
	// was asked; OfferedPeriodKeys are the periods a series_file_routing
	// alert's question offered. RemovedRowCount is how many rows loading a
	// preload_check_held alert's file would remove, and SentToPeriodKey the
	// period send_held_file_to_period sent it to.
	Findings          []PreloadCheckFindingInfo `yaml:"findings,omitempty" json:"findings,omitempty"`
	AskedUserID       string                    `yaml:"asked_user_id,omitempty" json:"asked_user_id,omitempty"`
	NotAskedReason    string                    `yaml:"not_asked_reason,omitempty" json:"not_asked_reason,omitempty"`
	OfferedPeriodKeys []string                  `yaml:"offered_period_keys,omitempty" json:"offered_period_keys,omitempty"`
	RemovedRowCount   *int                      `yaml:"removed_row_count,omitempty" json:"removed_row_count,omitempty"`
	SentToPeriodKey   string                    `yaml:"sent_to_period_key,omitempty" json:"sent_to_period_key,omitempty"`
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

	// Rule information. Present only on alerts a rule raised — which is a
	// minority of issue types, most of which come from ingestion. On a
	// severe_rule_bug alert this names the rule the crash-loop circuit breaker
	// tripped on, and it is the field the whole alert is about.
	DatasetRuleID   string `yaml:"dataset_rule_id,omitempty" json:"dataset_rule_id,omitempty"`
	DatasetRuleName string `yaml:"dataset_rule_name,omitempty" json:"dataset_rule_name,omitempty"`

	// Data source information
	DataSourceModelID   int32  `yaml:"data_source_model_id,omitempty" json:"data_source_model_id,omitempty"`
	DataSourceModelName string `yaml:"data_source_model_name,omitempty" json:"data_source_model_name,omitempty"`

	// Migration information (Tier 2+)
	MigrationModelID int32 `yaml:"migration_model_id,omitempty" json:"migration_model_id,omitempty"`

	// Field information
	FieldName         string  `yaml:"field_name,omitempty" json:"field_name,omitempty"`
	FieldValue        *string `yaml:"field_value,omitempty" json:"field_value,omitempty"`
	AnotherFieldName  string  `yaml:"another_field_name,omitempty" json:"another_field_name,omitempty"`
	AnotherFieldValue string  `yaml:"another_field_value,omitempty" json:"another_field_value,omitempty"`

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

	// FieldMappingScopes are the scopes the caller may give a column mapping,
	// widest first.
	FieldMappingScopes []string `yaml:"field_mapping_scopes,omitempty" json:"field_mapping_scopes,omitempty"`

	// Ingestion job IDs affected (Tier 2+)
	IngestionJobIDs []int `yaml:"ingestion_job_ids,omitempty" json:"ingestion_job_ids,omitempty"`

	// Ingestion jobs affected (Tier 2+)
	IngestionJobs []AlertIngestionJob `yaml:"ingestion_jobs,omitempty" json:"ingestion_jobs,omitempty"`

	// User information
	AssignedUser *AlertUserInfo `yaml:"assigned_user,omitempty" json:"assigned_user,omitempty"`
	Whodunit     *AlertUserInfo `yaml:"whodunit,omitempty" json:"whodunit,omitempty"`

	// Dependent alert information
	DependentAlert *DependentAlertInfo `yaml:"dependent_alert,omitempty" json:"dependent_alert,omitempty"`

	// Body highlights (Tier 2: -v; AlertBodyInfo names the Tier 1 exceptions)
	Body *AlertBodyInfo `yaml:"body,omitempty" json:"body,omitempty"`

	// UserSubmittedIssues are the flags people raised on this alert's rows,
	// retracted and detached ones included.
	UserSubmittedIssues []AlertUserSubmittedIssue `yaml:"user_submitted_issues,omitempty" json:"user_submitted_issues,omitempty"`
}

// AlertFlagger is the person who raised a user-submitted issue.
type AlertFlagger struct {
	ID        string `yaml:"id" json:"id"`
	Email     string `yaml:"email" json:"email"`
	FirstName string `yaml:"first_name" json:"first_name"`
	LastName  string `yaml:"last_name" json:"last_name"`
	Role      string `yaml:"role,omitempty" json:"role,omitempty"`
}

// AlertUserSubmittedIssue is one user-submitted issue on an alert. Times are
// RFC3339 in UTC.
type AlertUserSubmittedIssue struct {
	ID          string       `yaml:"id" json:"id"`
	RowID       int          `yaml:"row_id" json:"row_id"`
	FieldNames  []string     `yaml:"field_names" json:"field_names"`
	SubmittedBy AlertFlagger `yaml:"submitted_by" json:"submitted_by"`
	ThreadID    string       `yaml:"thread_id,omitempty" json:"thread_id,omitempty"`
	CreatedAt   string       `yaml:"created_at" json:"created_at"`
	RetractedAt string       `yaml:"retracted_at,omitempty" json:"retracted_at,omitempty"`
	DetachedAt  string       `yaml:"detached_at,omitempty" json:"detached_at,omitempty"`
}

// AlertIngestionJobRule is a dataset rule a dry run ran.
type AlertIngestionJobRule struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// AlertIngestionJob is an ingestion job linked to an alert.
type AlertIngestionJob struct {
	ID                   int                     `yaml:"id" json:"id"`
	Kind                 string                  `yaml:"kind" json:"kind"`
	State                string                  `yaml:"state" json:"state"`
	IsDryRun             bool                    `yaml:"is_dry_run,omitempty" json:"is_dry_run,omitempty"`
	StoredItemID         *int                    `yaml:"stored_item_id,omitempty" json:"stored_item_id,omitempty"`
	FileName             string                  `yaml:"file_name,omitempty" json:"file_name,omitempty"`
	DryRunJobID          *int                    `yaml:"dry_run_job_id,omitempty" json:"dry_run_job_id,omitempty"`
	CompositeDryRunJobID *int                    `yaml:"composite_dry_run_job_id,omitempty" json:"composite_dry_run_job_id,omitempty"`
	DatasetRules         []AlertIngestionJobRule `yaml:"dataset_rules,omitempty" json:"dataset_rules,omitempty"`
}

// AlertStatus holds runtime status information
type AlertStatus struct {
	Resolved    bool  `yaml:"resolved" json:"resolved"`
	IsRowLevel  bool  `yaml:"is_row_level,omitempty" json:"is_row_level,omitempty"`
	Count       int32 `yaml:"count" json:"count"`
	CountCapped bool  `yaml:"count_capped,omitempty" json:"count_capped,omitempty"`
	// AffectingRowsCount is the stats response's affecting_rows_count; nil
	// without one.
	AffectingRowsCount *int  `yaml:"affecting_rows_count,omitempty" json:"affecting_rows_count,omitempty"`
	AffectedFilesCount int32 `yaml:"affected_files_count,omitempty" json:"affected_files_count,omitempty"`
	// AffectedFiles are the alert's files, each once, in stored_items_to_alerts
	// order.
	AffectedFiles []AlertAffectedFile `yaml:"affected_files,omitempty" json:"affected_files,omitempty"`
	CreatedAt     string              `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	ResolvedAt    string              `yaml:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	ExpiredAt     string              `yaml:"expired_at,omitempty" json:"expired_at,omitempty"`
	// ChangeProposal is the change proposal holding the alert; nil when none
	// does.
	ChangeProposal *AlertChangeProposal `yaml:"change_proposal,omitempty" json:"change_proposal,omitempty"`
}

// AlertAffectedFile is one file linked to an alert.
type AlertAffectedFile struct {
	ID       int    `yaml:"id" json:"id"`
	FileName string `yaml:"file_name,omitempty" json:"file_name,omitempty"`
}

// AlertChangeProposal is the id and state of the change proposal holding an
// alert.
type AlertChangeProposal struct {
	ID    string `yaml:"id" json:"id"`
	State string `yaml:"state" json:"state"`
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
	RawResponse interface{} `yaml:"raw_response" json:"raw_response"`
	// RawStats is the alert stats response in the same form as RawResponse;
	// nil without one.
	RawStats   interface{}   `yaml:"raw_stats,omitempty" json:"raw_stats,omitempty"`
	Metadata   AlertMetadata `yaml:"metadata" json:"metadata"`
	APIVersion string        `yaml:"apiVersion" json:"apiVersion"`
	Kind       string        `yaml:"kind" json:"kind"`
}

// APIResponseToManifest converts an API AlertSchema, and the alert's stats
// when stats is non-nil, to an AlertManifestWithStatus.
// The verbosity parameter controls which fields are included:
//   - 0: Essential fields only (Tier 1) — omits debugging/niche fields
//   - 1+: Full details + body highlights (Tier 2)
//
// The -vv raw dump is APIResponseToRawManifest.
func APIResponseToManifest(resp *AlertFull, stats *IssueStats, verbosity int) *AlertManifestWithStatus {
	spec := buildAlertSpec(resp, verbosity)
	status := buildAlertStatus(resp, stats, verbosity)

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

func buildAlertSpec(resp *AlertFull, verbosity int) *AlertSpec {
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

	populateBaseAlertSpec(spec, resp)
	if verbosity >= 1 {
		populateVerboseAlertSpec(spec, resp)
	}
	if resp.IssueType == api.AlertTypeCellOverrideConflict && resp.FieldValue != nil {
		spec.FieldValue = resp.FieldValue
	}
	return spec
}

// populateBaseAlertSpec adds the optional fields shown at every verbosity,
// including the default one: buildAlertSpec calls it unconditionally, and only
// populateVerboseAlertSpec is gated on -v. dataset_rule_id / dataset_rule_name
// are the clearest case — naming the crashed rule with no -v is their point.
func populateBaseAlertSpec(spec *AlertSpec, resp *AlertFull) {
	if resp.FieldName != nil {
		spec.FieldName = *resp.FieldName
	}
	if resp.DataSourceModelName != nil {
		spec.DataSourceModelName = *resp.DataSourceModelName
	}

	if resp.DatasetRuleID != nil {
		spec.DatasetRuleID = resp.DatasetRuleID.String()
	}
	if resp.DatasetRuleName != nil {
		spec.DatasetRuleName = *resp.DatasetRuleName
	}

	// Related alerts are not diagnostic body detail: they are the other alert
	// resources this alert is about, and two severe_rule_bug actions resolve
	// them as a side effect. Keep their wire nesting while exposing them at the
	// default tier so a reader can inspect those alerts before choosing an
	// action. Prefix candidates are the answer to accept_gtin_prefix's required
	// new_value. The full -v body builder below preserves both fields.
	spec.Body = defaultTierBody(resp.Body)

	// Assigned user (shown in all tiers when present)
	if resp.AssignedUser != nil {
		spec.AssignedUser = newAlertUserInfo(*resp.AssignedUser)
	}

	// Dependent alert (shown in all tiers when present)
	if resp.DependentAlert != nil {
		spec.DependentAlert = &DependentAlertInfo{
			ID:      resp.DependentAlert.ID,
			Message: resp.DependentAlert.Msg,
		}
	}

	// Parked behind a migration (shown in all tiers when present). This is the
	// whole explanation for an alert with no actions, so hiding it behind -v
	// left the default view with nothing to say. omitempty on a bool means
	// this only ever ADDS a line, never changes one.
	spec.ResolveAfterMigration = boolValue(resp.ResolveAfterMigration)

	// Alert actions (shown in all tiers when present)
	if resp.AlertActionsList != nil {
		actions := make([]string, 0, len(*resp.AlertActionsList))
		for _, action := range *resp.AlertActionsList {
			actions = append(actions, string(action))
		}
		spec.Actions = actions
	}

	if resp.FieldMappingScopes != nil {
		spec.FieldMappingScopes = scopeNames(*resp.FieldMappingScopes)
	}

	if resp.UserSubmittedIssues != nil {
		spec.UserSubmittedIssues = buildUserSubmittedIssues(*resp.UserSubmittedIssues)
	}
}

// newAlertUserInfo converts u, keeping its role when it has one.
func newAlertUserInfo(u api.UserInfoTinySchema) *AlertUserInfo {
	return &AlertUserInfo{
		ID:    u.ID.String(),
		Email: u.Email,
		Role:  roleString(u.Role),
	}
}

// roleString returns role as a string, or "" when role is nil.
func roleString(role *api.RoleEnum) string {
	if role == nil {
		return ""
	}
	return string(*role)
}

// buildUserSubmittedIssues converts the alert's user-submitted issues, or
// returns nil for none.
func buildUserSubmittedIssues(issues []api.UserSubmittedIssueReadSchema) []AlertUserSubmittedIssue {
	if len(issues) == 0 {
		return nil
	}
	out := make([]AlertUserSubmittedIssue, 0, len(issues))
	for _, issue := range issues {
		item := AlertUserSubmittedIssue{
			ID:         issue.ID.String(),
			RowID:      issue.RowID,
			FieldNames: append([]string{}, issue.FieldNames...),
			SubmittedBy: AlertFlagger{
				ID:        issue.SubmittedBy.ID.String(),
				Email:     issue.SubmittedBy.Email,
				FirstName: issue.SubmittedBy.FirstName,
				LastName:  issue.SubmittedBy.LastName,
				Role:      roleString(issue.SubmittedBy.Role),
			},
			CreatedAt:   formatTime(&issue.CreatedAt),
			RetractedAt: formatTime(issue.RetractedAt),
			DetachedAt:  formatTime(issue.DetachedAt),
		}
		if issue.ThreadID != nil {
			item.ThreadID = issue.ThreadID.String()
		}
		out = append(out, item)
	}
	return out
}

// populateVerboseAlertSpec adds Tier 2 fields shown only with -v.
func populateVerboseAlertSpec(spec *AlertSpec, resp *AlertFull) {
	spec.DataSourceModelID = int32(pointerIntValue(resp.DataSourceModelID))
	spec.MigrationModelID = int32(pointerIntValue(resp.MigrationModelID))
	spec.ResolvableByUser = boolValue(resp.ResolvableByUser)

	if resp.FieldValue != nil && *resp.FieldValue != "" {
		spec.FieldValue = resp.FieldValue
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

	if resp.Whodunit != nil {
		spec.Whodunit = newAlertUserInfo(*resp.Whodunit)
	}

	if resp.IngestionJobIds != nil {
		spec.IngestionJobIDs = *resp.IngestionJobIds
	}
	if resp.IngestionJobs != nil {
		spec.IngestionJobs = buildIngestionJobs(*resp.IngestionJobs)
	}

	spec.Body = buildBodyInfo(resp)
}

func buildAlertStatus(resp *AlertFull, stats *IssueStats, verbosity int) *AlertStatus {
	status := &AlertStatus{
		Resolved:    boolValue(resp.Resolved),
		IsRowLevel:  resp.IsRowLevel,
		Count:       int32(resp.Count),
		CountCapped: boolValue(resp.CountCapped),
	}
	if stats != nil {
		status.AffectingRowsCount = stats.AffectingRowsCount
	}

	// Affected files count
	if resp.StoredItemsToAlerts != nil {
		status.AffectedFilesCount = int32(len(*resp.StoredItemsToAlerts))
		status.AffectedFiles = affectedFiles(*resp.StoredItemsToAlerts)
	}

	status.CreatedAt = formatTime(resp.CreatedAt)
	status.ResolvedAt = formatTime(resp.ResolvedAt)
	if cp := resp.ChangeProposal; cp != nil {
		status.ChangeProposal = &AlertChangeProposal{ID: cp.ID.String(), State: string(cp.State)}
	}

	// Expired at (Tier 2+ only)
	if verbosity >= 1 {
		status.ExpiredAt = formatTime(resp.ExpiredAt)
	}
	return status
}

// formatTime returns *t as RFC3339 in UTC, or "" for nil or the zero time.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// buildIngestionJobs converts the alert's linked ingestion jobs.
func buildIngestionJobs(jobs []api.AlertIngestionJobSchema) []AlertIngestionJob {
	out := make([]AlertIngestionJob, 0, len(jobs))
	for _, j := range jobs {
		job := AlertIngestionJob{
			ID:                   j.ID,
			Kind:                 string(j.Kind),
			State:                string(j.State),
			IsDryRun:             j.IsDryRun,
			StoredItemID:         j.StoredItemID,
			DryRunJobID:          j.DryRunJobID,
			CompositeDryRunJobID: j.CompositeDryRunJobID,
		}
		if j.FileName != nil {
			job.FileName = *j.FileName
		}
		if j.DatasetRules != nil {
			for _, r := range *j.DatasetRules {
				rule := AlertIngestionJobRule{ID: r.ID.String()}
				if r.Name != nil {
					rule.Name = *r.Name
				}
				job.DatasetRules = append(job.DatasetRules, rule)
			}
		}
		out = append(out, job)
	}
	return out
}

// APIResponseToRawManifest converts an API AlertSchema, and the alert's stats
// when stats is non-nil, to a raw manifest for -vv (Tier 3) output. Both
// responses are included as-is, JSON round-tripped so the field names stay the
// wire names in YAML as well as in JSON.
func APIResponseToRawManifest(resp *AlertFull, stats *IssueStats) (*AlertRawManifest, error) {
	rawData, err := wireForm(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to convert alert response: %w", err)
	}

	manifest := &AlertRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "Alert",
		Metadata: AlertMetadata{
			ID: int32(resp.ID),
		},
		RawResponse: rawData,
	}
	if stats != nil {
		if manifest.RawStats, err = wireForm(stats); err != nil {
			return nil, fmt.Errorf("failed to convert alert stats response: %w", err)
		}
	}
	return manifest, nil
}

// wireForm returns v JSON round-tripped into plain values.
func wireForm(v interface{}) (interface{}, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal: %w", err)
	}

	var data interface{}
	if err := jsonnum.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal: %w", err)
	}
	return data, nil
}

// buildBodyInfo extracts human-readable fields from the alert body.
// Returns nil if no body fields are present.
func buildBodyInfo(resp *AlertFull) *AlertBodyInfo {
	if resp.Body == nil {
		return nil
	}

	info := &AlertBodyInfo{}
	hasScalars := populateBodyScalars(info, resp.Body)
	hasCollections := populateBodyCollections(info, resp.Body)
	hasRecommendation := populateBodyRecommendation(info, resp.Body)

	if !hasScalars && !hasCollections && !hasRecommendation {
		return nil
	}
	return info
}

func populateBodyScalars(info *AlertBodyInfo, body *api.AlertBodySchema) bool {
	hasContent := false
	// Present exactly when the rule instance's severity policy overrode the
	// severity the rule declared -- `is_promoted` is `severity !=
	// declared_severity` (atlas common/common/exceptions.py), so it
	// fires on a demotion as readily as on a promotion despite the name, and
	// the write in rule_checker.py get_issue_exception_from_issue_context is the
	// field's only source.
	if body.DeclaredSeverity != nil && *body.DeclaredSeverity != "" {
		info.DeclaredSeverity = string(*body.DeclaredSeverity)
		hasContent = true
	}
	if body.FileName != nil && *body.FileName != "" {
		info.FileName = *body.FileName
		hasContent = true
	}
	if body.AnomalyScore != nil {
		info.AnomalyScore = body.AnomalyScore
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
	return hasContent
}

func populateBodyCollections(info *AlertBodyInfo, body *api.AlertBodySchema) bool {
	hasContent := false
	if body.AllowedValues != nil && len(*body.AllowedValues) > 0 {
		info.AllowedValues = *body.AllowedValues
		hasContent = true
	}
	if body.TopSuggestedValues != nil && len(*body.TopSuggestedValues) > 0 {
		recs := make([]ValueRecommendation, 0, len(*body.TopSuggestedValues))
		for _, v := range *body.TopSuggestedValues {
			recs = append(recs, ValueRecommendation{Value: v.Value, Strong: v.Strong})
		}
		info.TopSuggestedValues = recs
		hasContent = true
	}
	if body.RulePath != nil && len(*body.RulePath) > 0 {
		info.RulePath = *body.RulePath
		hasContent = true
	}
	if always := defaultTierBody(body); always != nil {
		info.RelatedAlertIDs = always.RelatedAlertIDs
		info.PrefixCandidates = always.PrefixCandidates
		info.Reason = always.Reason
		info.FromAddress = always.FromAddress
		info.PeriodKey = always.PeriodKey
		info.PipelineValue = always.PipelineValue
		info.PipelineValueIsNumber = always.PipelineValueIsNumber
		info.BaseDisplacedValue = always.BaseDisplacedValue
		info.Findings = always.Findings
		info.AskedUserID = always.AskedUserID
		info.NotAskedReason = always.NotAskedReason
		info.OfferedPeriodKeys = always.OfferedPeriodKeys
		info.RemovedRowCount = always.RemovedRowCount
		info.SentToPeriodKey = always.SentToPeriodKey
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
	return hasContent
}

// defaultTierBody copies the body fields shown at every verbosity.
func defaultTierBody(body *api.AlertBodySchema) *AlertBodyInfo {
	if body == nil {
		return nil
	}
	var info AlertBodyInfo
	if body.RelatedAlertIds != nil && len(*body.RelatedAlertIds) > 0 {
		info.RelatedAlertIDs = append([]int(nil), (*body.RelatedAlertIds)...)
	}
	if body.PrefixCandidates != nil && len(*body.PrefixCandidates) > 0 {
		info.PrefixCandidates = append([]string(nil), (*body.PrefixCandidates)...)
	}
	if body.Reason != nil {
		info.Reason = unionReason(*body.Reason)
	}
	if body.PeriodKey != nil {
		info.PeriodKey = *body.PeriodKey
	}
	hasConflict := populateConflictBody(&info, body)
	hasHeld := populateHeldFileBody(&info, body)
	if info.RelatedAlertIDs == nil && info.PrefixCandidates == nil && info.Reason == "" && info.PeriodKey == "" && !hasConflict && !hasHeld {
		return nil
	}
	return &info
}

func populateHeldFileBody(info *AlertBodyInfo, body *api.AlertBodySchema) bool {
	if body.FromAddress != nil {
		info.FromAddress = *body.FromAddress
	}
	if body.Findings != nil {
		for _, f := range *body.Findings {
			info.Findings = append(info.Findings, preloadCheckFindingInfo(f))
		}
	}
	if body.AskedUserID != nil {
		info.AskedUserID = body.AskedUserID.String()
	}
	if body.NotAskedReason != nil {
		info.NotAskedReason = string(*body.NotAskedReason)
	}
	if body.OfferedPeriodKeys != nil && len(*body.OfferedPeriodKeys) > 0 {
		info.OfferedPeriodKeys = append([]string(nil), (*body.OfferedPeriodKeys)...)
	}
	if body.RemovedRowCount != nil {
		n := *body.RemovedRowCount
		info.RemovedRowCount = &n
	}
	if body.SentToPeriodKey != nil {
		info.SentToPeriodKey = *body.SentToPeriodKey
	}
	return hasHeldFileBody(info)
}

// hasHeldFileBody reports whether populateHeldFileBody set any field of info.
func hasHeldFileBody(info *AlertBodyInfo) bool {
	return info.FromAddress != "" || info.Findings != nil || info.AskedUserID != "" || info.NotAskedReason != "" || info.OfferedPeriodKeys != nil ||
		info.RemovedRowCount != nil || info.SentToPeriodKey != ""
}

func preloadCheckFindingInfo(f api.PreloadCheckFinding) PreloadCheckFindingInfo {
	info := PreloadCheckFindingInfo{
		Reason:           string(f.Reason),
		RowCount:         f.RowCount,
		BaselineRowCount: f.BaselineRowCount,
		RowDropShare:     f.RowDropShare,
		LargestPeriodKey: f.LargestPeriodKey,
		LargestRowCount:  f.LargestRowCount,
		RoutedPeriodKey:  f.RoutedPeriodKey,
		RoutedRowCount:   f.RoutedRowCount,
		NonBlankRowCount: f.NonBlankRowCount,

		ReplacedStoredItemID: f.ReplacedStoredItemID,
		ReplacedLoadedAt:     formatTime(f.ReplacedLoadedAt),
		ReplacedRowCount:     f.ReplacedRowCount,
	}
	if f.ReplacedFileName != nil {
		info.ReplacedFileName = *f.ReplacedFileName
	}
	if f.PeriodLabel != nil {
		info.PeriodLabel = *f.PeriodLabel
	}
	if f.MissingColumns != nil {
		info.MissingColumns = append([]string(nil), (*f.MissingColumns)...)
	}
	if f.LargestBucket != nil {
		info.LargestBucket = string(*f.LargestBucket)
	}
	return info
}

// unionReason returns the alert body's reason, which is a SeriesFileRoutingReason
// or an InboundEmailHeldReason, as its wire string.
func unionReason(reason api.AlertBodySchema_Reason) string {
	raw, err := reason.MarshalJSON()
	if err != nil {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

func populateConflictBody(info *AlertBodyInfo, body *api.AlertBodySchema) bool {
	if body.PipelineValue != nil {
		value, _ := jsonnum.Remarshal(body.PipelineValue)
		info.PipelineValue = &value
	}
	info.PipelineValueIsNumber = body.PipelineValueIsNumber
	if body.BaseDisplacedValue != nil {
		value, _ := jsonnum.Remarshal(body.BaseDisplacedValue)
		info.BaseDisplacedValue = &value
	}
	return info.PipelineValue != nil || info.PipelineValueIsNumber != nil || info.BaseDisplacedValue != nil
}

func populateBodyRecommendation(info *AlertBodyInfo, body *api.AlertBodySchema) bool {
	hasContent := false
	if body.RecommendedSettingsFieldName != nil && *body.RecommendedSettingsFieldName != "" {
		info.RecommendedSettingsFieldName = *body.RecommendedSettingsFieldName
		hasContent = true
	}
	if body.RecommendedSettingsValue != nil {
		info.RecommendedSettingsValue = *body.RecommendedSettingsValue
		hasContent = true
	}
	if body.RecommendedSettingsModuleToUpdate != nil && *body.RecommendedSettingsModuleToUpdate != "" {
		info.RecommendedSettingsModuleToUpdate = string(*body.RecommendedSettingsModuleToUpdate)
		hasContent = true
	}
	return hasContent
}

// affectedFiles lists each stored item of links once, keeping its first file
// name.
func affectedFiles(links []api.StoredItemToAlertSchema) []AlertAffectedFile {
	var files []AlertAffectedFile
	seen := map[int]bool{}
	for _, link := range links {
		if seen[link.StoredItemID] {
			continue
		}
		seen[link.StoredItemID] = true
		file := AlertAffectedFile{ID: link.StoredItemID}
		if link.FileName != nil {
			file.FileName = *link.FileName
		}
		files = append(files, file)
	}
	return files
}
