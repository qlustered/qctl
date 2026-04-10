package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// ApplyResult represents the outcome of an apply operation.
type ApplyResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Action  string `json:"action,omitempty"` // "created" or "updated"
	ID      int    `json:"id,omitempty"`
}

// GetDatasetByName retrieves a dataset by exact name.
func (c *Client) GetDatasetByName(accessToken, name string) (*DatasetFull, error) {
	params := GetDatasetsParams{
		Name: &name,
	}

	resp, err := c.GetDatasets(accessToken, params)
	if err != nil {
		return nil, err
	}

	var matches []DatasetTiny
	for _, ds := range resp.Results {
		if ds.Name == name {
			matches = append(matches, ds)
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple tables found with name %q", name)
	}

	return c.GetDataset(accessToken, matches[0].ID)
}

// CreateDataset creates a dataset based on the manifest.
// It automatically chooses the quick or full endpoint based on manifest content.
func (c *Client) CreateDataset(accessToken string, manifest *TableManifest) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Determine if we can use quick create
	if canUseQuickCreate(manifest) {
		return c.createDatasetQuick(apiClient, accessToken, manifest)
	}

	return c.createDatasetFull(apiClient, accessToken, manifest)
}

// createDatasetQuick creates a dataset using the /datasets/quick endpoint.
func (c *Client) createDatasetQuick(apiClient *client.Client, accessToken string, manifest *TableManifest) (*DatasetFull, error) {
	body := manifestToQuickPostRequest(manifest)
	resp, err := apiClient.API.CreateQuickDatasetWithResponse(
		context.Background(),
		c.organizationID,
		&api.CreateQuickDatasetParams{},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "quick create dataset failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "quick create dataset failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	// Quick create returns a different response type, fetch the full dataset
	return c.GetDataset(accessToken, resp.JSON200.ID)
}

// createDatasetFull creates a dataset using the /datasets endpoint.
// It first fetches server defaults from GET /datasets/new, then overlays
// the manifest values on top to avoid overriding server defaults with Go zero values.
func (c *Client) createDatasetFull(apiClient *client.Client, accessToken string, manifest *TableManifest) (*DatasetFull, error) {
	var body api.DataSetPostRequestSchema

	defaults, err := c.fetchDefaults(apiClient)
	if err != nil {
		// Fall back to direct manifest conversion if defaults fetch fails
		body = manifestToPostRequest(manifest)
	} else {
		body = defaultsToPostRequest(defaults)
		applyManifestOverrides(&body, manifest)
	}

	resp, err := apiClient.API.PostDatasetWithResponse(
		context.Background(),
		c.organizationID,
		&api.PostDatasetParams{},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "create dataset failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "create dataset failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// canUseQuickCreate determines if a manifest can use the quick create endpoint.
// Quick create is used when the manifest only contains fields available in
// DataSetCreateQuickPostRequest.
func canUseQuickCreate(manifest *TableManifest) bool {
	spec := manifest.Spec

	// Check if any fields outside of quick create are set
	// Quick create fields: backup_settings_id, database_name, destination_id, enable_row_logs, name, table_name

	// These fields are NOT in quick create schema
	if spec.SchemaName != "" {
		return false
	}
	if spec.MigrationPolicy != "" {
		return false
	}
	if spec.DataLoadingProcess != "" {
		return false
	}
	if spec.BackupKeyFormat != "" {
		return false
	}
	if spec.AnomalyThreshold != nil {
		return false
	}
	if spec.MaxRetryCount != nil {
		return false
	}
	if spec.MaxTriesToFixJSON != nil {
		return false
	}
	if spec.ShouldReprocess != nil {
		return false
	}
	if spec.DetectAnomalies != nil {
		return false
	}
	if spec.StrictlyOneDatetimeFormatInAColumn != nil {
		return false
	}
	if spec.GuessDatetimeFormatInIngestion != nil {
		return false
	}
	if spec.EnableCellMoveSuggestions != nil {
		return false
	}
	if spec.EncryptRawDataDuringBackup != nil {
		return false
	}
	if spec.QuarantineRowsUntilApproved != nil {
		return false
	}
	if spec.RemoveOutliersWhenRecommendingNumericValidators != nil {
		return false
	}
	if spec.ColumnsForEntityResolution != nil {
		return false
	}
	if spec.SettingsModel != nil {
		return false
	}

	return true
}

// manifestToQuickPostRequest converts a TableManifest to DataSetCreateQuickPostRequest.
func manifestToQuickPostRequest(manifest *TableManifest) api.DataSetCreateQuickPostRequest {
	return api.DataSetCreateQuickPostRequest{
		Name:             manifest.Metadata.Name,
		DestinationID:    manifest.Spec.DestinationID,
		DatabaseName:     manifest.Spec.DatabaseName,
		TableName:        manifest.Spec.TableName,
		BackupSettingsID: manifest.Spec.BackupSettingsID,
		EnableRowLogs:    manifest.Spec.EnableRowLogs,
	}
}

// UpdateDataset patches an existing dataset using the manifest values.
func (c *Client) UpdateDataset(accessToken string, existing *DatasetFull, manifest *TableManifest) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	body := manifestToPatchRequest(existing, manifest)
	resp, err := apiClient.API.PatchDatasetWithResponse(
		context.Background(),
		c.organizationID,
		&api.PatchDatasetParams{},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "update dataset failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "update dataset failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// Apply performs idempotent create/update semantics.
func (c *Client) Apply(accessToken string, manifest *TableManifest) (*ApplyResult, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is required")
	}

	if errs := manifest.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("manifest validation failed: %s", errs[0].Error())
	}

	existing, err := c.GetDatasetByName(accessToken, manifest.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up table: %w", err)
	}

	if existing == nil {
		result, err := c.CreateDataset(accessToken, manifest)
		if err != nil {
			return nil, err
		}
		return &ApplyResult{
			Status:  "applied",
			Name:    result.Name,
			ID:      result.ID,
			Action:  "created",
			Message: "table created successfully",
		}, nil
	}

	// Validate immutable fields
	if manifest.Spec.DestinationID != existing.DestinationID {
		return nil, fmt.Errorf("table already exists with destination_id %d (cannot change destination)", existing.DestinationID)
	}
	if manifest.Spec.DatabaseName != existing.DatabaseName {
		return nil, fmt.Errorf("table already exists with database_name %s (cannot change database)", existing.DatabaseName)
	}
	if manifest.Spec.SchemaName != existing.SchemaName {
		return nil, fmt.Errorf("table already exists with schema_name %s (cannot change schema)", existing.SchemaName)
	}
	if manifest.Spec.TableName != existing.TableName {
		return nil, fmt.Errorf("table already exists with table_name %s (cannot change table name)", existing.TableName)
	}
	if manifest.Spec.BackupSettingsID != existing.BackupSettingsID {
		return nil, fmt.Errorf("table already exists with backup_settings_id %d (cannot change backup settings)", existing.BackupSettingsID)
	}

	updated, err := c.UpdateDataset(accessToken, existing, manifest)
	if err != nil {
		return nil, err
	}

	return &ApplyResult{
		Status:  "applied",
		Name:    updated.Name,
		ID:      updated.ID,
		Action:  "updated",
		Message: "table updated successfully",
	}, nil
}

// fetchDefaults fetches server-side defaults from GET /datasets/new.
func (c *Client) fetchDefaults(apiClient *client.Client) (*api.DataSetSchemaFullDraft, error) {
	resp, err := apiClient.API.GetNewDatasetWithResponse(
		context.Background(),
		c.organizationID,
		&api.GetNewDatasetParams{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch defaults: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("fetch defaults returned status %d", resp.StatusCode())
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty defaults response")
	}

	return resp.JSON200, nil
}

// defaultsToPostRequest converts server defaults into a base POST request body.
// Required fields (Name, DestinationID, etc.) are left at zero values and must
// be set by applyManifestOverrides.
func defaultsToPostRequest(defaults *api.DataSetSchemaFullDraft) api.DataSetPostRequestSchema {
	req := api.DataSetPostRequestSchema{
		AnomalyThreshold:                   intPointer(defaults.AnomalyThreshold),
		MaxRetryCount:                      intPointer(defaults.MaxRetryCount),
		MaxTriesToFixJSON:                  intPointer(defaults.MaxTriesToFixJSON),
		BackupKeyFormat:                    stringPtrIfNotEmpty(defaults.BackupKeyFormat),
		ShouldReprocess:                    defaults.ShouldReprocess,
		DetectAnomalies:                    defaults.DetectAnomalies,
		StrictlyOneDatetimeFormatInAColumn: defaults.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:     defaults.GuessDatetimeFormatInIngestion,
		EnableRowLogs:                      defaults.EnableRowLogs,
		EnableCellMoveSuggestions:          defaults.EnableCellMoveSuggestions,
		EncryptRawDataDuringBackup:         defaults.EncryptRawDataDuringBackup,
		QuarantineRowsUntilApproved:        defaults.QuarantineRowsUntilApproved,
		RemoveOutliersWhenRecommendingNumericValidators: defaults.RemoveOutliersWhenRecommendingNumericValidators,
		ColumnsForEntityResolution:                      defaults.ColumnsForEntityResolution,
		MigrationPolicy: (*api.MigrationPolicy)(&defaults.MigrationPolicy),
		DataLoadingProcess: (*api.DataLoadingProcess)(&defaults.DataLoadingProcess),
	}

	return req
}

// applyManifestOverrides overlays manifest values on top of a base POST request.
// Required fields are always set. Optional fields are only set if non-nil/non-empty.
func applyManifestOverrides(req *api.DataSetPostRequestSchema, manifest *TableManifest) {
	// Required fields — always set from manifest
	req.Name = manifest.Metadata.Name
	req.DestinationID = manifest.Spec.DestinationID
	req.DatabaseName = manifest.Spec.DatabaseName
	req.SchemaName = manifest.Spec.SchemaName
	req.TableName = manifest.Spec.TableName
	req.BackupSettingsID = manifest.Spec.BackupSettingsID

	// *int fields — only override if manifest explicitly set them
	if manifest.Spec.AnomalyThreshold != nil {
		req.AnomalyThreshold = manifest.Spec.AnomalyThreshold
	}
	if manifest.Spec.MaxRetryCount != nil {
		req.MaxRetryCount = manifest.Spec.MaxRetryCount
	}
	if manifest.Spec.MaxTriesToFixJSON != nil {
		req.MaxTriesToFixJSON = manifest.Spec.MaxTriesToFixJSON
	}

	// *bool fields — only override if manifest explicitly set them
	if manifest.Spec.ShouldReprocess != nil {
		req.ShouldReprocess = manifest.Spec.ShouldReprocess
	}
	if manifest.Spec.DetectAnomalies != nil {
		req.DetectAnomalies = manifest.Spec.DetectAnomalies
	}
	if manifest.Spec.StrictlyOneDatetimeFormatInAColumn != nil {
		req.StrictlyOneDatetimeFormatInAColumn = manifest.Spec.StrictlyOneDatetimeFormatInAColumn
	}
	if manifest.Spec.GuessDatetimeFormatInIngestion != nil {
		req.GuessDatetimeFormatInIngestion = manifest.Spec.GuessDatetimeFormatInIngestion
	}
	if manifest.Spec.EnableRowLogs != nil {
		req.EnableRowLogs = manifest.Spec.EnableRowLogs
	}
	if manifest.Spec.EnableCellMoveSuggestions != nil {
		req.EnableCellMoveSuggestions = manifest.Spec.EnableCellMoveSuggestions
	}
	if manifest.Spec.EncryptRawDataDuringBackup != nil {
		req.EncryptRawDataDuringBackup = manifest.Spec.EncryptRawDataDuringBackup
	}
	if manifest.Spec.QuarantineRowsUntilApproved != nil {
		req.QuarantineRowsUntilApproved = manifest.Spec.QuarantineRowsUntilApproved
	}
	if manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators != nil {
		req.RemoveOutliersWhenRecommendingNumericValidators = manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators
	}

	// String enums — only override if manifest explicitly set them
	if manifest.Spec.MigrationPolicy != "" {
		mp := manifest.Spec.MigrationPolicy
		req.MigrationPolicy = &mp
	}
	if manifest.Spec.DataLoadingProcess != "" {
		dlp := manifest.Spec.DataLoadingProcess
		req.DataLoadingProcess = &dlp
	}

	// String fields — only override if non-empty
	if manifest.Spec.BackupKeyFormat != "" {
		req.BackupKeyFormat = stringPtrIfNotEmpty(manifest.Spec.BackupKeyFormat)
	}

	// Slice fields
	if manifest.Spec.ColumnsForEntityResolution != nil {
		req.ColumnsForEntityResolution = manifest.Spec.ColumnsForEntityResolution
	}

	// SettingsModel — only override if set
	if manifest.Spec.SettingsModel != nil {
		req.SettingsModel = settingsModelToNewSettingsSchema(manifest.Spec.SettingsModel)
	}
}

func manifestToPostRequest(manifest *TableManifest) api.DataSetPostRequestSchema {
	mp := manifest.Spec.MigrationPolicy
	dlp := manifest.Spec.DataLoadingProcess

	req := api.DataSetPostRequestSchema{
		Name:                               manifest.Metadata.Name,
		DestinationID:                      manifest.Spec.DestinationID,
		DatabaseName:                       manifest.Spec.DatabaseName,
		SchemaName:                         manifest.Spec.SchemaName,
		TableName:                          manifest.Spec.TableName,
		BackupSettingsID:                   manifest.Spec.BackupSettingsID,
		BackupKeyFormat:                    stringPtrIfNotEmpty(manifest.Spec.BackupKeyFormat),
		MigrationPolicy:                    &mp,
		DataLoadingProcess:                 &dlp,
		AnomalyThreshold:                   manifest.Spec.AnomalyThreshold,
		MaxRetryCount:                      manifest.Spec.MaxRetryCount,
		MaxTriesToFixJSON:                  manifest.Spec.MaxTriesToFixJSON,
		ShouldReprocess:                    manifest.Spec.ShouldReprocess,
		DetectAnomalies:                    manifest.Spec.DetectAnomalies,
		StrictlyOneDatetimeFormatInAColumn: manifest.Spec.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:     manifest.Spec.GuessDatetimeFormatInIngestion,
		EnableRowLogs:                      manifest.Spec.EnableRowLogs,
		EnableCellMoveSuggestions:          manifest.Spec.EnableCellMoveSuggestions,
		EncryptRawDataDuringBackup:         manifest.Spec.EncryptRawDataDuringBackup,
		QuarantineRowsUntilApproved:        manifest.Spec.QuarantineRowsUntilApproved,
		RemoveOutliersWhenRecommendingNumericValidators: manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators,
		ColumnsForEntityResolution:                      manifest.Spec.ColumnsForEntityResolution,
	}

	if manifest.Spec.SettingsModel != nil {
		req.SettingsModel = settingsModelToNewSettingsSchema(manifest.Spec.SettingsModel)
	}

	return req
}

func manifestToPatchRequest(existing *DatasetFull, manifest *TableManifest) api.DataSetPatchRequestSchema {
	// Start with existing values as the base so unspecified fields are preserved,
	// then overlay only fields explicitly set in the manifest.
	existingMP := api.MigrationPolicy(existing.MigrationPolicy)
	existingDLP := api.DataLoadingProcess(existing.DataLoadingProcess)

	req := api.DataSetPatchRequestSchema{
		ID:                                              existing.ID,
		VersionID:                                       existing.VersionID,
		Name:                                            &existing.Name,
		MigrationPolicy:                                 &existingMP,
		DataLoadingProcess:                               &existingDLP,
		AnomalyThreshold:                                &existing.AnomalyThreshold,
		MaxRetryCount:                                   &existing.MaxRetryCount,
		MaxTriesToFixJSON:                                &existing.MaxTriesToFixJSON,
		ShouldReprocess:                                 existing.ShouldReprocess,
		DetectAnomalies:                                 existing.DetectAnomalies,
		StrictlyOneDatetimeFormatInAColumn:              existing.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:                  existing.GuessDatetimeFormatInIngestion,
		EnableRowLogs:                                   existing.EnableRowLogs,
		EnableCellMoveSuggestions:                        existing.EnableCellMoveSuggestions,
		EncryptRawDataDuringBackup:                      existing.EncryptRawDataDuringBackup,
		QuarantineRowsUntilApproved:                     existing.QuarantineRowsUntilApproved,
		RemoveOutliersWhenRecommendingNumericValidators: existing.RemoveOutliersWhenRecommendingNumericValidators,
		ColumnsForEntityResolution:                      existing.ColumnsForEntityResolution,
		BackupKeyFormat:                                 &existing.BackupKeyFormat,
		NotAnomaliesPerColumn:                           existing.NotAnomaliesPerColumn,
		DeletedLineSignatures:                           existing.DeletedLineSignatures,
		RedirectPostSubmitTo:                            existing.RedirectPostSubmitTo,
	}

	// Overlay manifest values — only override when explicitly set
	if manifest.Metadata.Name != "" {
		req.Name = &manifest.Metadata.Name
	}
	if manifest.Spec.MigrationPolicy != "" {
		mp := manifest.Spec.MigrationPolicy
		req.MigrationPolicy = &mp
	}
	if manifest.Spec.DataLoadingProcess != "" {
		dlp := manifest.Spec.DataLoadingProcess
		req.DataLoadingProcess = &dlp
	}
	if manifest.Spec.AnomalyThreshold != nil {
		req.AnomalyThreshold = manifest.Spec.AnomalyThreshold
	}
	if manifest.Spec.MaxRetryCount != nil {
		req.MaxRetryCount = manifest.Spec.MaxRetryCount
	}
	if manifest.Spec.MaxTriesToFixJSON != nil {
		req.MaxTriesToFixJSON = manifest.Spec.MaxTriesToFixJSON
	}
	if manifest.Spec.ShouldReprocess != nil {
		req.ShouldReprocess = manifest.Spec.ShouldReprocess
	}
	if manifest.Spec.DetectAnomalies != nil {
		req.DetectAnomalies = manifest.Spec.DetectAnomalies
	}
	if manifest.Spec.StrictlyOneDatetimeFormatInAColumn != nil {
		req.StrictlyOneDatetimeFormatInAColumn = manifest.Spec.StrictlyOneDatetimeFormatInAColumn
	}
	if manifest.Spec.GuessDatetimeFormatInIngestion != nil {
		req.GuessDatetimeFormatInIngestion = manifest.Spec.GuessDatetimeFormatInIngestion
	}
	if manifest.Spec.EnableRowLogs != nil {
		req.EnableRowLogs = manifest.Spec.EnableRowLogs
	}
	if manifest.Spec.EnableCellMoveSuggestions != nil {
		req.EnableCellMoveSuggestions = manifest.Spec.EnableCellMoveSuggestions
	}
	if manifest.Spec.EncryptRawDataDuringBackup != nil {
		req.EncryptRawDataDuringBackup = manifest.Spec.EncryptRawDataDuringBackup
	}
	if manifest.Spec.QuarantineRowsUntilApproved != nil {
		req.QuarantineRowsUntilApproved = manifest.Spec.QuarantineRowsUntilApproved
	}
	if manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators != nil {
		req.RemoveOutliersWhenRecommendingNumericValidators = manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators
	}
	if manifest.Spec.ColumnsForEntityResolution != nil {
		req.ColumnsForEntityResolution = manifest.Spec.ColumnsForEntityResolution
	}
	if manifest.Spec.BackupKeyFormat != "" {
		req.BackupKeyFormat = stringPtrIfNotEmpty(manifest.Spec.BackupKeyFormat)
	}
	if manifest.Spec.SettingsModel != nil {
		req.SettingsModel = settingsModelToSettingsSchemaOptional(manifest.Spec.SettingsModel)
	}

	return req
}

func intPointer(val int) *int {
	return &val
}

func stringPointer(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

func stringPtrIfNotEmpty(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

// settingsModelToNewSettingsSchema converts a manifest SettingsModel to api.NewSettingsSchema.
// Used for POST (create) operations.
func settingsModelToNewSettingsSchema(sm *SettingsModel) *api.NewSettingsSchema {
	if sm == nil {
		return nil
	}

	result := &api.NewSettingsSchema{
		ArrayDelimiters:                              sm.ArrayDelimiters,
		BooleanFalse:                                 sm.BooleanFalse,
		BooleanTrue:                                  sm.BooleanTrue,
		DatetimeAllowedCharacters:                    sm.DatetimeAllowedCharacters,
		DatetimeFormats:                              sm.DatetimeFormats,
		DecimalFieldPadding:                          sm.DecimalFieldPadding,
		DollarToCent:                                 sm.DollarToCent,
		DollarValueIfWordInFieldName:                 sm.DollarValueIfWordInFieldName,
		EnableInteger:                                sm.EnableInteger,
		EnableSmallInteger:                           sm.EnableSmallInteger,
		EncryptColumns:                               sm.EncryptColumns,
		FieldsToExpand:                               sm.FieldsToExpand,
		IgnoreColumnNames:                            sm.IgnoreColumnNames,
		IgnoreFieldsInSignatureCalculation:           sm.IgnoreFieldsInSignatureCalculation,
		IgnoreLinesThatIncludeOnlySubsetOfCharacters: sm.IgnoreLinesThatIncludeOnlySubsetOfCharacters,
		IgnoreLinesThatIncludeOnlySubsetOfWords:      sm.IgnoreLinesThatIncludeOnlySubsetOfWords,
		IgnoreNotSeenBeforeFieldsWhenImporting:       sm.IgnoreNotSeenBeforeFieldsWhenImporting,
		InferDatetime:                                sm.InferDatetime,
		InferDatetimeForColumns:                      sm.InferDatetimeForColumns,
		MaxBoundaryElementLength:                     sm.MaxBoundaryElementLength,
		NonStringFieldsAreAllNullable:                sm.NonStringFieldsAreAllNullable,
		NullValues:                                   sm.NullValues,
		PercentToDecimal:                             sm.PercentToDecimal,
		StringFieldPadding:                           sm.StringFieldPadding,
		StringFieldsCanBeNullable:                    sm.StringFieldsCanBeNullable,
		TrimStringInsteadOfRaisingErr:                sm.TrimStringInsteadOfRaisingErr,
		UseTextInsteadOfString:                       sm.UseTextInsteadOfString,
	}

	// Convert map types (need to use pointers)
	if sm.BooleanFalsePerColumn != nil {
		result.BooleanFalsePerColumn = &sm.BooleanFalsePerColumn
	}
	if sm.BooleanTruePerColumn != nil {
		result.BooleanTruePerColumn = &sm.BooleanTruePerColumn
	}
	if sm.DataMarkedAsValidForDBField != nil {
		result.DataMarkedAsValidForDBField = &sm.DataMarkedAsValidForDBField
	}
	if sm.DatetimeFormatsPerColumn != nil {
		result.DatetimeFormatsPerColumn = &sm.DatetimeFormatsPerColumn
	}
	if sm.DefaultValueForFieldWhenCastingError != nil {
		result.DefaultValueForFieldWhenCastingError = &sm.DefaultValueForFieldWhenCastingError
	}
	if sm.FieldNameFullConversion != nil {
		result.FieldNameFullConversion = &sm.FieldNameFullConversion
	}
	if sm.FieldNamePartConversion != nil {
		result.FieldNamePartConversion = &sm.FieldNamePartConversion
	}
	if sm.IgnoreMatchers != nil {
		result.IgnoreMatchers = &sm.IgnoreMatchers
	}
	if sm.MonetaryColumnsOverride != nil {
		result.MonetaryColumnsOverride = &sm.MonetaryColumnsOverride
	}
	if sm.NullValuesPerColumn != nil {
		result.NullValuesPerColumn = &sm.NullValuesPerColumn
	}
	if sm.ValueMapping != nil {
		result.ValueMapping = &sm.ValueMapping
	}
	if sm.ValueMappingPerColumn != nil {
		result.ValueMappingPerColumn = &sm.ValueMappingPerColumn
	}

	// Handle XlsDateMode enum conversion
	if sm.XlsDateMode != nil {
		xlsMode := api.XLSDateMode(*sm.XlsDateMode)
		result.XlsDateMode = &xlsMode
	}

	return result
}

// settingsModelToSettingsSchemaOptional converts a manifest SettingsModel to api.SettingsSchemaOptional.
// Used for PATCH (update) operations.
func settingsModelToSettingsSchemaOptional(sm *SettingsModel) *api.SettingsSchemaOptional {
	if sm == nil {
		return nil
	}

	result := &api.SettingsSchemaOptional{
		ArrayDelimiters:                              sm.ArrayDelimiters,
		BooleanFalse:                                 sm.BooleanFalse,
		BooleanTrue:                                  sm.BooleanTrue,
		DatetimeAllowedCharacters:                    sm.DatetimeAllowedCharacters,
		DatetimeFormats:                              sm.DatetimeFormats,
		DecimalFieldPadding:                          sm.DecimalFieldPadding,
		DollarToCent:                                 sm.DollarToCent,
		DollarValueIfWordInFieldName:                 sm.DollarValueIfWordInFieldName,
		EnableInteger:                                sm.EnableInteger,
		EnableSmallInteger:                           sm.EnableSmallInteger,
		EncryptColumns:                               sm.EncryptColumns,
		FieldsToExpand:                               sm.FieldsToExpand,
		IgnoreColumnNames:                            sm.IgnoreColumnNames,
		IgnoreFieldsInSignatureCalculation:           sm.IgnoreFieldsInSignatureCalculation,
		IgnoreLinesThatIncludeOnlySubsetOfCharacters: sm.IgnoreLinesThatIncludeOnlySubsetOfCharacters,
		IgnoreLinesThatIncludeOnlySubsetOfWords:      sm.IgnoreLinesThatIncludeOnlySubsetOfWords,
		IgnoreNotSeenBeforeFieldsWhenImporting:       sm.IgnoreNotSeenBeforeFieldsWhenImporting,
		InferDatetime:                                sm.InferDatetime,
		InferDatetimeForColumns:                      sm.InferDatetimeForColumns,
		MaxBoundaryElementLength:                     sm.MaxBoundaryElementLength,
		NonStringFieldsAreAllNullable:                sm.NonStringFieldsAreAllNullable,
		NullValues:                                   sm.NullValues,
		PercentToDecimal:                             sm.PercentToDecimal,
		StringFieldPadding:                           sm.StringFieldPadding,
		StringFieldsCanBeNullable:                    sm.StringFieldsCanBeNullable,
		TrimStringInsteadOfRaisingErr:                sm.TrimStringInsteadOfRaisingErr,
		UseTextInsteadOfString:                       sm.UseTextInsteadOfString,
	}

	// Convert map types (need to use pointers)
	if sm.BooleanFalsePerColumn != nil {
		result.BooleanFalsePerColumn = &sm.BooleanFalsePerColumn
	}
	if sm.BooleanTruePerColumn != nil {
		result.BooleanTruePerColumn = &sm.BooleanTruePerColumn
	}
	if sm.DataMarkedAsValidForDBField != nil {
		result.DataMarkedAsValidForDBField = &sm.DataMarkedAsValidForDBField
	}
	if sm.DatetimeFormatsPerColumn != nil {
		result.DatetimeFormatsPerColumn = &sm.DatetimeFormatsPerColumn
	}
	if sm.DefaultValueForFieldWhenCastingError != nil {
		result.DefaultValueForFieldWhenCastingError = &sm.DefaultValueForFieldWhenCastingError
	}
	if sm.FieldNameFullConversion != nil {
		result.FieldNameFullConversion = &sm.FieldNameFullConversion
	}
	if sm.FieldNamePartConversion != nil {
		result.FieldNamePartConversion = &sm.FieldNamePartConversion
	}
	if sm.IgnoreMatchers != nil {
		result.IgnoreMatchers = &sm.IgnoreMatchers
	}
	if sm.MonetaryColumnsOverride != nil {
		result.MonetaryColumnsOverride = &sm.MonetaryColumnsOverride
	}
	if sm.NullValuesPerColumn != nil {
		result.NullValuesPerColumn = &sm.NullValuesPerColumn
	}
	if sm.ValueMapping != nil {
		result.ValueMapping = &sm.ValueMapping
	}
	if sm.ValueMappingPerColumn != nil {
		result.ValueMappingPerColumn = &sm.ValueMappingPerColumn
	}

	// Handle XlsDateMode enum conversion
	if sm.XlsDateMode != nil {
		xlsMode := api.XLSDateMode(*sm.XlsDateMode)
		result.XlsDateMode = &xlsMode
	}

	return result
}
