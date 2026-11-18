package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ApplyResult represents the outcome of an apply operation.
type ApplyResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Action  string `json:"action,omitempty"` // "created", "updated" or "unchanged"
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
	if manifest == nil {
		return nil, qerrors.NewBadUsage("manifest is required")
	}
	if errs := manifest.Validate(); len(errs) > 0 {
		return nil, qerrors.NewBadUsagef("manifest validation failed: %s", errs[0].Error())
	}
	if !canUseQuickCreate(manifest) {
		if errs := manifest.validateFullCreate(); len(errs) > 0 {
			return nil, qerrors.NewBadUsagef("manifest validation failed: %s", errs[0].Error())
		}
	}
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
		return c.createDatasetQuick(apiClient, accessToken, manifestToQuickPostRequest(manifest))
	}

	body, err := c.prepareFullCreateBody(apiClient, manifest)
	if err != nil {
		return nil, err
	}
	return c.createDatasetFull(apiClient, body)
}

// createDatasetQuick creates a dataset using the /datasets/quick endpoint.
func (c *Client) createDatasetQuick(apiClient *client.Client, accessToken string, body api.DataSetCreateQuickPostRequest) (*DatasetFull, error) {
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

// prepareFullCreateBody fetches server defaults from GET /datasets/new, then
// overlays the manifest values on top to avoid overriding server defaults with
// Go zero values.
func (c *Client) prepareFullCreateBody(apiClient *client.Client, manifest *TableManifest) (api.DataSetPostRequestSchema, error) {
	var body api.DataSetPostRequestSchema

	defaults, err := c.fetchDefaults(apiClient)
	if err != nil {
		// Access failures are authoritative, not evidence that this server lacks
		// the defaults endpoint. Continuing would hide the 401/403/404, prompt
		// for a request qctl cannot make, and only rediscover the failure on POST.
		if code := qerrors.GetExitCode(err); code == qerrors.ExitUnauthorized || code == qerrors.ExitNotFound {
			return api.DataSetPostRequestSchema{}, err
		}
		// Fall back to direct manifest conversion if defaults fetch fails
		body, err = manifestToPostRequest(manifest)
		if err != nil {
			return api.DataSetPostRequestSchema{}, err
		}
	} else {
		body = defaultsToPostRequest(defaults)
		if err := applyManifestOverrides(&body, manifest); err != nil {
			return api.DataSetPostRequestSchema{}, err
		}
	}
	return body, nil
}

// createDatasetFull creates a dataset using the /datasets endpoint with an
// already-prepared body. Keeping preparation separate lets diff and an apply
// confirmation describe exactly the request that is later sent.
func (c *Client) createDatasetFull(apiClient *client.Client, body api.DataSetPostRequestSchema) (*DatasetFull, error) {
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
	// Quick create also carries append, append_skip_identical_rows, or mirror loading process.
	return !hasNonQuickCreateShapeFields(spec) &&
		!hasNonQuickCreateTuningFields(spec) &&
		!hasNonQuickCreateAdvancedFields(spec)
}

func hasNonQuickCreateShapeFields(spec TableSpec) bool {
	return spec.SchemaName != "" ||
		spec.MigrationPolicy != "" ||
		!quickCreateLoadingProcess(spec.DataLoadingProcess) ||
		spec.BackupKeyFormat != ""
}

func quickCreateLoadingProcess(process DataLoadingProcess) bool {
	switch process {
	case "",
		DataLoadingProcess(api.DataSetCreateQuickPostRequestDataLoadingProcessAppend),
		DataLoadingProcess(api.DataSetCreateQuickPostRequestDataLoadingProcessAppendSkipIdenticalRows),
		DataLoadingProcess(api.DataSetCreateQuickPostRequestDataLoadingProcessMirror):
		return true
	default:
		return false
	}
}

func hasNonQuickCreateTuningFields(spec TableSpec) bool {
	return spec.AnomalyThreshold != nil ||
		spec.MaxRetryCount != nil ||
		spec.MaxTriesToFixJSON != nil ||
		spec.DetectAnomalies != nil ||
		spec.StrictlyOneDatetimeFormatInAColumn != nil ||
		spec.GuessDatetimeFormatInIngestion != nil
}

func hasNonQuickCreateAdvancedFields(spec TableSpec) bool {
	return spec.EnableCellMoveSuggestions != nil ||
		spec.EncryptRawDataDuringBackup != nil ||
		spec.QuarantineRowsUntilApproved != nil ||
		spec.RemoveOutliersWhenRecommendingNumericValidators != nil ||
		spec.ColumnsForEntityResolution != nil ||
		spec.PiiColumns != nil ||
		spec.ModelLabels != nil ||
		spec.CompressionTypeOfBackupData != nil ||
		spec.SettingsModel != nil
}

// manifestToQuickPostRequest converts a TableManifest to DataSetCreateQuickPostRequest.
func manifestToQuickPostRequest(manifest *TableManifest) api.DataSetCreateQuickPostRequest {
	req := api.DataSetCreateQuickPostRequest{
		Name:             &manifest.Metadata.Name,
		DestinationID:    manifest.Spec.DestinationID,
		DatabaseName:     manifest.Spec.DatabaseName,
		TableName:        &manifest.Spec.TableName,
		BackupSettingsID: manifest.Spec.BackupSettingsID,
	}
	if manifest.Spec.DataLoadingProcess != "" {
		process := api.DataSetCreateQuickPostRequestDataLoadingProcess(manifest.Spec.DataLoadingProcess)
		req.DataLoadingProcess = &process
	}
	return req
}

// UpdateDataset patches an existing dataset using the manifest values.
func (c *Client) UpdateDataset(accessToken string, existing *DatasetFull, manifest *TableManifest) (*DatasetFull, error) {
	body, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		return nil, err
	}
	updated, err := c.updateDatasetWithBody(accessToken, body)
	if err != nil {
		return nil, explainPiiColumnsRemoval(err, existing, &body)
	}
	return updated, nil
}

// updateDatasetWithBody sends an already-prepared table PATCH. Callers that
// preview or confirm a change use this path so the displayed body and the wire
// body are the same value, not two independently rebuilt snapshots.
func (c *Client) updateDatasetWithBody(accessToken string, body api.DataSetPatchRequestSchema) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	raw, err := json.Marshal(datasetPatchWireBody{body: &body})
	if err != nil {
		return nil, fmt.Errorf("failed to encode table update: %w", err)
	}
	resp, err := apiClient.API.PatchDatasetWithBodyWithResponse(
		context.Background(),
		c.organizationID,
		&api.PatchDatasetParams{},
		"application/json",
		bytes.NewReader(raw),
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

// datasetPatchWireBody is the exact JSON representation used for a table
// PATCH. Atlas's latest SettingsSchemaOptional correctly makes
// schema_overrides nullable, but oapi-codegen consequently dropped omitempty
// from that field. A nil generated field would now serialize as an explicit
// null and reopen the unguarded schema-write path that kind: TableSchema exists
// to prevent. Keep the generated file untouched and remove the nested key at
// the request boundary instead.
type datasetPatchWireBody struct {
	body *api.DataSetPatchRequestSchema
}

func (w datasetPatchWireBody) MarshalJSON() ([]byte, error) {
	if w.body == nil {
		return []byte("null"), nil
	}
	raw, err := json.Marshal(w.body)
	if err != nil {
		return nil, err
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, err
	}
	settingsRaw, ok := outer["settings_model"]
	if !ok || string(settingsRaw) == "null" { //nolint:goconst // Keep the JSON literal beside the raw-settings check.
		return raw, nil
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		return nil, err
	}
	delete(settings, "schema_overrides")
	settingsRaw, err = json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	outer["settings_model"] = settingsRaw
	return json.Marshal(outer)
}

// Apply performs idempotent create/update semantics.
func (c *Client) Apply(accessToken string, manifest *TableManifest) (*ApplyResult, error) {
	plan, err := c.PrepareApply(accessToken, manifest)
	if err != nil {
		return nil, err
	}
	return c.ApplyPrepared(accessToken, plan)
}

// fetchDefaults fetches server-side defaults from GET /datasets/new.
func (c *Client) fetchDefaults(apiClient *client.Client) (*api.DataSetSchemaFullDraft, error) {
	resp, err := apiClient.API.GetNewDatasetWithResponse(
		context.Background(),
		c.organizationID,
		&api.GetNewDatasetParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to fetch defaults")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to fetch defaults")
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
		AnomalyThreshold:                                intPointer(defaults.AnomalyThreshold),
		MaxRetryCount:                                   intPointer(defaults.MaxRetryCount),
		MaxTriesToFixJSON:                               intPointer(defaults.MaxTriesToFixJSON),
		BackupKeyFormat:                                 stringPtrIfNotEmpty(defaults.BackupKeyFormat),
		DetectAnomalies:                                 defaults.DetectAnomalies,
		StrictlyOneDatetimeFormatInAColumn:              defaults.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:                  defaults.GuessDatetimeFormatInIngestion,
		EnableCellMoveSuggestions:                       defaults.EnableCellMoveSuggestions,
		EncryptRawDataDuringBackup:                      defaults.EncryptRawDataDuringBackup,
		QuarantineRowsUntilApproved:                     defaults.QuarantineRowsUntilApproved,
		RemoveOutliersWhenRecommendingNumericValidators: defaults.RemoveOutliersWhenRecommendingNumericValidators,
		ColumnsForEntityResolution:                      defaults.ColumnsForEntityResolution,
		MigrationPolicy:                                 &defaults.MigrationPolicy,
		DataLoadingProcess:                              &defaults.DataLoadingProcess,
	}

	return req
}

// applyManifestOverrides overlays manifest values on top of a base POST request.
// Required fields are always set. Optional fields are only set if non-nil/non-empty.
func applyManifestOverrides(req *api.DataSetPostRequestSchema, manifest *TableManifest) error {
	// Required fields — always set from manifest
	req.Name = manifest.Metadata.Name
	req.DestinationID = manifest.Spec.DestinationID
	req.DatabaseName = manifest.Spec.DatabaseName
	req.SchemaName = manifest.Spec.SchemaName
	req.TableName = manifest.Spec.TableName
	req.BackupSettingsID = manifest.Spec.BackupSettingsID

	applyManifestNumericOverrides(req, manifest.Spec)
	applyManifestBooleanOverrides(req, manifest.Spec)
	return applyManifestConfigurationOverrides(req, manifest.Spec)
}

func applyManifestNumericOverrides(req *api.DataSetPostRequestSchema, spec TableSpec) {
	// *int fields — only override if manifest explicitly set them
	if spec.AnomalyThreshold != nil {
		req.AnomalyThreshold = spec.AnomalyThreshold
	}
	if spec.MaxRetryCount != nil {
		req.MaxRetryCount = spec.MaxRetryCount
	}
	if spec.MaxTriesToFixJSON != nil {
		req.MaxTriesToFixJSON = spec.MaxTriesToFixJSON
	}
}

func applyManifestBooleanOverrides(req *api.DataSetPostRequestSchema, spec TableSpec) {
	// *bool fields — only override if manifest explicitly set them
	if spec.DetectAnomalies != nil {
		req.DetectAnomalies = spec.DetectAnomalies
	}
	if spec.StrictlyOneDatetimeFormatInAColumn != nil {
		req.StrictlyOneDatetimeFormatInAColumn = spec.StrictlyOneDatetimeFormatInAColumn
	}
	if spec.GuessDatetimeFormatInIngestion != nil {
		req.GuessDatetimeFormatInIngestion = spec.GuessDatetimeFormatInIngestion
	}
	if spec.EnableCellMoveSuggestions != nil {
		req.EnableCellMoveSuggestions = spec.EnableCellMoveSuggestions
	}
	if spec.EncryptRawDataDuringBackup != nil {
		req.EncryptRawDataDuringBackup = spec.EncryptRawDataDuringBackup
	}
	if spec.QuarantineRowsUntilApproved != nil {
		req.QuarantineRowsUntilApproved = spec.QuarantineRowsUntilApproved
	}
	if spec.RemoveOutliersWhenRecommendingNumericValidators != nil {
		req.RemoveOutliersWhenRecommendingNumericValidators = spec.RemoveOutliersWhenRecommendingNumericValidators
	}
}

func applyManifestConfigurationOverrides(req *api.DataSetPostRequestSchema, spec TableSpec) error {
	// String enums — only override if manifest explicitly set them
	if spec.MigrationPolicy != "" {
		mp := spec.MigrationPolicy
		req.MigrationPolicy = &mp
	}
	if spec.DataLoadingProcess != "" {
		dlp := spec.DataLoadingProcess
		req.DataLoadingProcess = &dlp
	}

	// String fields — only override if non-empty
	if spec.BackupKeyFormat != "" {
		req.BackupKeyFormat = stringPtrIfNotEmpty(spec.BackupKeyFormat)
	}

	// Enum pointers — a nil one leaves the key off the wire entirely, since the
	// POST schema declares compression_type_of_backup_data with omitempty.
	if spec.CompressionTypeOfBackupData != nil {
		req.CompressionTypeOfBackupData = spec.CompressionTypeOfBackupData
	}

	// Slice and map fields
	if spec.ColumnsForEntityResolution != nil {
		req.ColumnsForEntityResolution = spec.ColumnsForEntityResolution
	}
	if spec.PiiColumns != nil {
		req.PiiColumns = spec.PiiColumns
	}
	if spec.ModelLabels != nil {
		req.ModelLabels = spec.ModelLabels
	}

	// SettingsModel — only override if set
	if spec.SettingsModel != nil {
		settings, err := settingsModelToNewSettingsSchema(spec.SettingsModel)
		if err != nil {
			return err
		}
		req.SettingsModel = settings
	}

	return nil
}

func manifestToPostRequest(manifest *TableManifest) (api.DataSetPostRequestSchema, error) {
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
		DetectAnomalies:                    manifest.Spec.DetectAnomalies,
		StrictlyOneDatetimeFormatInAColumn: manifest.Spec.StrictlyOneDatetimeFormatInAColumn,
		GuessDatetimeFormatInIngestion:     manifest.Spec.GuessDatetimeFormatInIngestion,
		EnableCellMoveSuggestions:          manifest.Spec.EnableCellMoveSuggestions,
		EncryptRawDataDuringBackup:         manifest.Spec.EncryptRawDataDuringBackup,
		QuarantineRowsUntilApproved:        manifest.Spec.QuarantineRowsUntilApproved,
		RemoveOutliersWhenRecommendingNumericValidators: manifest.Spec.RemoveOutliersWhenRecommendingNumericValidators,
		ColumnsForEntityResolution:                      manifest.Spec.ColumnsForEntityResolution,
		PiiColumns:                                      manifest.Spec.PiiColumns,
		CompressionTypeOfBackupData:                     manifest.Spec.CompressionTypeOfBackupData,
		ModelLabels:                                     manifest.Spec.ModelLabels,
	}

	if manifest.Spec.SettingsModel != nil {
		settings, err := settingsModelToNewSettingsSchema(manifest.Spec.SettingsModel)
		if err != nil {
			return api.DataSetPostRequestSchema{}, err
		}
		req.SettingsModel = settings
	}

	return req, nil
}

func manifestToPatchRequest(existing *DatasetFull, manifest *TableManifest) (api.DataSetPatchRequestSchema, error) {
	// Start with existing values as the base so unspecified fields are preserved,
	// then overlay only fields explicitly set in the manifest.
	req, err := patchRequestFromExisting(existing)
	if err != nil {
		return api.DataSetPatchRequestSchema{}, err
	}
	applyPatchIdentityAndPolicyOverrides(&req, manifest)
	applyPatchNumericOverrides(&req, manifest.Spec)
	applyPatchBooleanOverrides(&req, manifest.Spec)
	if err := applyPatchStructuredOverrides(&req, existing, manifest.Spec, manifest.clearKeyColumns); err != nil {
		return api.DataSetPatchRequestSchema{}, err
	}

	return req, nil
}

// patchRequestFromExisting seeds the dataset PATCH from the table the read
// returned. It goes through JSON rather than a field-by-field copy for the same
// reason settingsPatchBase does one level down: 23 of the 27 keys on
// api.DataSetPatchRequestSchema carry no omitempty, so a key the copy forgets is
// serialised as null, and the backend applies every key the RAW body carries
// (cettings/cettings/db.py:update_from_body). Such a null was skipped only
// because set_field skips None on a non-nullable column and update_from_body
// skips it on a key that is not a mapped column at all -- so the hand copy's
// four null-valued keys (model_labels, organization_id, destinations_list,
// whodunit_id) survived on properties of the current backend rather than on
// anything qctl controls, and the first nullable column a spec adds would be
// wiped on every apply with no test failure. TestPatchBaseCarriesEveryField is
// the tripwire.
//
// api.DataSetSchemaFull carries every one of the patch schema's JSON keys plus
// read-only keys of its own (state, table_name, disabled_fields, the row
// counts, pii_visibility, ...), and the decode drops those -- which is why the fields apply validates as immutable
// (destination_id, database_name, schema_name, table_name, backup_settings_id)
// cannot be echoed by this seed even in principle. The backend ignores them on a
// PATCH anyway (settings_view_core.py patch_dataset_core's ignore_keys).
func patchRequestFromExisting(existing *DatasetFull) (api.DataSetPatchRequestSchema, error) {
	base := api.DataSetPatchRequestSchema{}

	// A nil read cannot seed anything, and a zero-value patch would go to the
	// server as id 0. Unreachable through Apply, which creates the table when the
	// lookup finds none, but the hand copy this replaced dereferenced `existing`
	// on its first line and this one does not, so say so rather than send it.
	if existing == nil {
		return base, fmt.Errorf("cannot build a table update without the table's current values")
	}

	raw, err := json.Marshal(existing)
	if err != nil {
		return base, fmt.Errorf("failed to encode the table qctl just read: %w", err)
	}
	// UseNumber, because not_anomalies_per_column is a map[string]any: without it
	// every JSON number decodes to float64, and re-encoding a stored value past
	// 2^53 hands the server back a DIFFERENT number (12345678901234567 ->
	// 12345678901234568). That keeps only what existing already holds exactly,
	// so the read must not round it either: GetDataset decodes with jsonnum.
	// Same trap as settingsPatchBase's, one level up.
	// TestPatchBasePreservesLargeNumbers guards this decode, and
	// TestApplyDatasetUpdateResendsStoredNumbersExactly the read and the PATCH.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&base); err != nil {
		return api.DataSetPatchRequestSchema{}, fmt.Errorf("failed to decode the table qctl just read: %w", err)
	}

	// The settings model is the one key the read must not seed. Sending the block
	// submits the nested optimistic-concurrency check and rewrites every settings
	// column, so it goes on the wire only when the manifest asked for a settings
	// edit -- applyPatchStructuredOverrides puts it back through
	// settingsPatchFromExisting, which seeds it from this same read.
	//
	// Unlike settingsPatchBase's schema_overrides line, this one does not hang on
	// omitempty. settings_model has it (one of four keys here that do, with
	// migration_policy, data_loading_process and compression_type_of_backup_data),
	// so nil omits the key -- but a regen that dropped it would send
	// "settings_model": null, and both backend arms guard on truthiness: the
	// version check is `body.settings_model and ...` over the parsed body, where
	// the field is SettingsSchemaOptional | None = None, and the nested update is
	// `original_body.get("settings_model") and ...` over the raw one. A null is
	// inert on both. TestPatchOmitsSettingsModelWhenTheManifestHasNone still
	// asserts absence from the wire rather than just a nil field.
	base.SettingsModel = nil

	return base, nil
}

func applyPatchIdentityAndPolicyOverrides(req *api.DataSetPatchRequestSchema, manifest *TableManifest) {
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
}

func applyPatchNumericOverrides(req *api.DataSetPatchRequestSchema, spec TableSpec) {
	if spec.AnomalyThreshold != nil {
		req.AnomalyThreshold = spec.AnomalyThreshold
	}
	if spec.MaxRetryCount != nil {
		req.MaxRetryCount = spec.MaxRetryCount
	}
	if spec.MaxTriesToFixJSON != nil {
		req.MaxTriesToFixJSON = spec.MaxTriesToFixJSON
	}
}

func applyPatchBooleanOverrides(req *api.DataSetPatchRequestSchema, spec TableSpec) {
	if spec.DetectAnomalies != nil {
		req.DetectAnomalies = spec.DetectAnomalies
	}
	if spec.StrictlyOneDatetimeFormatInAColumn != nil {
		req.StrictlyOneDatetimeFormatInAColumn = spec.StrictlyOneDatetimeFormatInAColumn
	}
	if spec.GuessDatetimeFormatInIngestion != nil {
		req.GuessDatetimeFormatInIngestion = spec.GuessDatetimeFormatInIngestion
	}
	if spec.EnableCellMoveSuggestions != nil {
		req.EnableCellMoveSuggestions = spec.EnableCellMoveSuggestions
	}
	if spec.EncryptRawDataDuringBackup != nil {
		req.EncryptRawDataDuringBackup = spec.EncryptRawDataDuringBackup
	}
	if spec.QuarantineRowsUntilApproved != nil {
		req.QuarantineRowsUntilApproved = spec.QuarantineRowsUntilApproved
	}
	if spec.RemoveOutliersWhenRecommendingNumericValidators != nil {
		req.RemoveOutliersWhenRecommendingNumericValidators = spec.RemoveOutliersWhenRecommendingNumericValidators
	}
}

func applyPatchStructuredOverrides(req *api.DataSetPatchRequestSchema, existing *DatasetFull, spec TableSpec,
	clearKeyColumns bool) error {
	if spec.ColumnsForEntityResolution != nil {
		req.ColumnsForEntityResolution = spec.ColumnsForEntityResolution
	}
	if spec.PiiColumns != nil {
		req.PiiColumns = spec.PiiColumns
	}
	if spec.ModelLabels != nil {
		req.ModelLabels = spec.ModelLabels
	}
	if spec.CompressionTypeOfBackupData != nil {
		req.CompressionTypeOfBackupData = spec.CompressionTypeOfBackupData
	}
	if spec.BackupKeyFormat != "" {
		req.BackupKeyFormat = stringPtrIfNotEmpty(spec.BackupKeyFormat)
	}
	if spec.SettingsModel == nil {
		// No settings_model block on the wire at all, so the backend's nested
		// version check does not run and no settings column is rewritten.
		return nil
	}
	// A settings edit is only meaningful on top of the settings model the read
	// returned, and both of the backend's arms key off the STORED one: the
	// optimistic-concurrency check compares against body.settings_model.version_id
	// only when the dataset has a settings model, and so does the nested update
	// (settings_view_core.py, patch_dataset_core and
	// _update_dataset_from_body). So a read that carried none splits two ways
	// -- if the dataset has one, the id/version_id 0 this would send earns a
	// 409 whose message says "DataSet version mismatch" and names nothing about
	// the settings model; if it does not, the server skips both, answers 200,
	// and apply reports "table updated successfully" over an edit that was
	// dropped. Neither leaves the user anything to act on, so refuse here
	// instead.
	//
	// Unreachable through today's contract -- api.DataSetSchemaFull declares
	// SettingsModel as a value, not a pointer, and the detail GET's response model
	// requires it -- which is why this checks the id and not version_id: a settings
	// model's version_id is legitimately 0, its primary key never is.
	if existing.SettingsModel.ID == 0 {
		return fmt.Errorf("the read of table %q returned no settings model, so spec.settings_model cannot be applied: the server would either refuse the change as a version mismatch or accept the request and drop the settings edit silently", existing.Name)
	}
	settings, err := settingsPatchFromExisting(&existing.SettingsModel, spec.SettingsModel)
	if err != nil {
		return err
	}
	if clearKeyColumns {
		// A nil KeyColumns goes out as "key_columns": null, which the PATCH
		// writes as no key columns.
		settings.KeyColumns = nil
	}
	req.SettingsModel = settings
	return nil
}

func intPointer(val int) *int {
	return &val
}

func stringPtrIfNotEmpty(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

// settingsModelToNewSettingsSchema converts a manifest SettingsModel into the
// settings block of a POST (create) body.
//
// A create body is NOT an overlay: there is nothing stored to overlay onto, so
// every key the manifest omits goes out as null (api.NewSettingsSchema declares
// omitempty only on xls_date_mode) and the server's own default lands. That is
// the intended behaviour for a create, and it is why the read had to stop
// dropping emptied values -- see settingsSchemaToSettingsModel.
//
// TestEverySettingsManifestFieldReachesTheCreateBody is the tripwire.
func settingsModelToNewSettingsSchema(sm *SettingsModel) (*api.NewSettingsSchema, error) {
	if sm == nil {
		return nil, nil
	}

	result := &api.NewSettingsSchema{}
	if err := transcodeJSONToWire(sm, result); err != nil {
		return nil, fmt.Errorf("failed to encode the manifest's settings model: %w", err)
	}
	return result, nil
}

// settingsPatchFromExisting builds the settings_model block of a dataset PATCH:
// the settings model the read returned, with the manifest overlaid on top.
//
// The block is a whole object, not a sparse patch. PATCH /orgs/{org}/datasets
// applies every key the RAW request body carries (atlas
// cettings/cettings/db.py:update_from_body walks request.json(), not the parsed
// model) and api.SettingsSchemaOptional declares omitempty only on xls_date_mode,
// so a field left nil here goes out as null: set_field skips a null on a NOT
// NULL column, but on a nullable one it writes SQL NULL and the stored value is
// gone. Building on top of the read is also what carries the settings model's
// own id and version_id -- the handler answers 409 when
// body.settings_model.version_id does not match the stored one, and a zero
// never does.
//
// This mirrors patchRequestFromExisting, which treats the dataset's own fields
// the same way, and the web UI, which spreads the loaded settings model and
// stamps id/version_id onto it (cosmosview
// src/container/tablesettings/util.ts:updateTableSettingData).
func settingsPatchFromExisting(existing *SettingsSchema, sm *SettingsModel) (*api.SettingsSchemaOptional, error) {
	if sm == nil {
		return nil, nil
	}

	base, err := settingsPatchBase(existing)
	if err != nil {
		return nil, err
	}

	return overlaySettings(base, sm)
}

// overlaySettings writes the manifest's settings over the ones the read
// returned, one JSON key at a time: a key the manifest carries replaces the
// seeded entry wholesale, a key it omits keeps it.
//
// It merges RAW KEYS rather than unmarshalling the manifest onto the seeded
// struct, and the difference is the whole clear semantics. encoding/json REUSES
// an existing map when it decodes an object into one, keeping the entries
// already there, so `value_mapping: {}` written over a base seeded from the read
// would leave the stored pairs in place and the clear would be silently dropped
// -- a 200 and "table updated successfully" over an edit that never happened.
// Slices are reset rather than merged, so a `[]` clear works either way, which
// is what would have made this an easy bug to ship half-tested.
// TestPatchClearsSettingsFieldsWrittenEmpty covers both shapes.
//
// json.RawMessage also carries the manifest's bytes through untouched, so a
// number past 2^53 needs no UseNumber handling on this path at all.
func overlaySettings(base *api.SettingsSchemaOptional, sm *SettingsModel) (*api.SettingsSchemaOptional, error) {
	merged, err := jsonKeyMap(base)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the table's current settings model: %w", err)
	}
	edits, err := jsonKeyMap(sm)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the manifest's settings model: %w", err)
	}

	for key, val := range edits {
		merged[key] = val
	}

	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the settings model patch: %w", err)
	}
	// UseNumber for the same reason settingsPatchBase decodes that way: this
	// object is bound for the server, and default_value_for_field_when_casting_error
	// is a map[string]any whose values would otherwise round-trip through float64.
	req := &api.SettingsSchemaOptional{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(req); err != nil {
		return nil, fmt.Errorf("failed to decode the settings model patch: %w", err)
	}
	return req, nil
}

// jsonKeyMap renders v as its top-level JSON keys with the bytes of each value
// left undecoded, which is what lets overlaySettings replace a value rather than
// merge into it.
func jsonKeyMap(v any) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	keys := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// settingsPatchBase seeds a PATCH settings_model block from the settings model
// the read returned. It goes through JSON rather than a field-by-field copy:
// api.SettingsSchema and api.SettingsSchemaOptional carry the same 47 JSON keys
// and differ only in pointer-ness, so the round trip carries a column a later
// spec adds instead of nulling it. TestSettingsPatchBaseCarriesEveryField is the
// tripwire.
func settingsPatchBase(existing *SettingsSchema) (*api.SettingsSchemaOptional, error) {
	base := &api.SettingsSchemaOptional{}
	if existing == nil {
		return base, nil
	}

	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the table's current settings model: %w", err)
	}
	// UseNumber, because default_value_for_field_when_casting_error is a
	// map[string]any: without it every JSON number decodes to float64, and
	// re-encoding a stored default past 2^53 hands the server back a DIFFERENT
	// number (12345678901234567 -> 12345678901234568). The column is NOT NULL,
	// so before this block was seeded from the read it went out as null and the
	// server skipped it -- echoing it back is what makes the fidelity matter,
	// and why the read decodes with jsonnum too.
	// TestSettingsPatchBasePreservesLargeNumbers guards this decode, and
	// TestApplyDatasetUpdateResendsStoredNumbersExactly the read and the PATCH.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(base); err != nil {
		return nil, fmt.Errorf("failed to decode the table's current settings model: %w", err)
	}

	// schema_overrides is deliberately absent from the manifest (see the
	// SettingsModel doc comment) and must not be echoed back either: PATCH
	// writes that column with no migration version check, no draft and no
	// needs_review_migration alert, which would make apply a second, unguarded
	// way to change a schema. `kind: TableSchema` is the guarded one.
	//
	// The latest generated map type no longer carries omitempty when nullable;
	// datasetPatchWireBody removes the key at the outer request boundary.
	base.SchemaOverrides = nil

	return base, nil
}
