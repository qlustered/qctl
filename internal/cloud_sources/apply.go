package cloud_sources

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// ApplyResult represents the result of an apply operation.
type ApplyResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Action  string `json:"action,omitempty"` // "created" or "updated"
	ID      int    `json:"id,omitempty"`
}

// GetCloudSourceByName retrieves a cloud source by exact name within a dataset.
func (c *Client) GetCloudSourceByName(accessToken string, datasetID int, name string) (*CloudSourceFull, error) {
	params := GetCloudSourcesParams{
		DatasetID:   &datasetID,
		SearchQuery: &name,
	}

	resp, err := c.GetCloudSources(accessToken, params)
	if err != nil {
		return nil, err
	}

	var matches []CloudSourceTiny
	for _, cs := range resp.Results {
		if cs.Name == name {
			matches = append(matches, cs)
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple cloud sources named %q found for dataset %d", name, datasetID)
	}

	return c.GetCloudSource(accessToken, matches[0].ID)
}

// CreateCloudSource creates a new cloud source from a manifest.
func (c *Client) CreateCloudSource(accessToken string, manifest *CloudSourceManifest) (*api.DataSourceModelFullSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	body := mapToPostBody(manifest)
	resp, err := apiClient.API.PostDataSourceModelWithResponse(
		context.Background(),
		c.organizationID,
		&api.PostDataSourceModelParams{},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "create cloud source failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "create cloud source failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// UpdateCloudSource updates an existing cloud source using patch semantics.
func (c *Client) UpdateCloudSource(accessToken string, existing *CloudSourceFull, manifest *CloudSourceManifest) (*api.DataSourceModelFullSchema, error) {
	if existing.VersionID == nil {
		return nil, fmt.Errorf("cloud source %d is missing version_id; cannot apply", derefInt(existing.ID))
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

	body := mapToPatchBody(existing, manifest)
	resp, err := apiClient.API.PatchDataSourceModelWithResponse(
		context.Background(),
		c.organizationID,
		derefInt(existing.ID),
		&api.PatchDataSourceModelParams{},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "update cloud source failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "update cloud source failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// Apply performs an idempotent create or update.
func (c *Client) Apply(accessToken string, manifest *CloudSourceManifest) (*ApplyResult, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is required")
	}

	if errs := manifest.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("manifest validation failed: %s", errs[0].Error())
	}

	existing, err := c.GetCloudSourceByName(accessToken, manifest.Spec.DatasetID, manifest.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up cloud source: %w", err)
	}

	if existing == nil {
		result, err := c.CreateCloudSource(accessToken, manifest)
		if err != nil {
			return nil, err
		}
		return &ApplyResult{
			Status:  "applied",
			Name:    result.Name,
			ID:      result.ID,
			Action:  "created",
			Message: "cloud source created successfully",
		}, nil
	}

	// Prevent changes to immutable identity fields
	if existing.DatasetID != nil && *existing.DatasetID != manifest.Spec.DatasetID {
		return nil, fmt.Errorf("cloud source already exists with dataset_id %d (cannot change dataset_id)", *existing.DatasetID)
	}
	if existing.DataSourceType != nil && manifest.Spec.DataSourceType != nil && *existing.DataSourceType != *manifest.Spec.DataSourceType {
		return nil, fmt.Errorf("cloud source already exists with data_source_type %s (cannot change type)", *existing.DataSourceType)
	}

	if _, err := c.UpdateCloudSource(accessToken, existing, manifest); err != nil {
		return nil, err
	}

	return &ApplyResult{
		Status:  "applied",
		Name:    manifest.Metadata.Name,
		ID:      derefInt(existing.ID),
		Action:  "updated",
		Message: "cloud source updated successfully",
	}, nil
}

func mapToPostBody(manifest *CloudSourceManifest) api.DataSourceModelPostRequestSchema {
	spec := manifest.Spec

	body := api.DataSourceModelPostRequestSchema{
		Name:            manifest.Metadata.Name,
		DataSourceType:  spec.DataSourceType,
		DatasetID:       &spec.DatasetID,
		DatasetName:     spec.DatasetName,
		SettingsModelID: spec.SettingsModelID,
		Schedule:        spec.Schedule,
		Pattern:         spec.Pattern,
		SimpleURL:       spec.SimpleURL,
		ArchivePattern:  spec.ArchivePattern,

		ConnectionTimeout:           spec.ConnectionTimeout,
		DeleteSourceFileAfterBackup: spec.DeleteSourceFileAfterBackup,
		S3Bucket:                    spec.S3Bucket,
		S3RegionName:                spec.S3RegionName,
		S3Prefix:                    spec.S3Prefix,
		S3EndpointURL:               spec.S3EndpointURL,
		S3AccessKey:                 spec.S3AccessKey,
		S3SecretKey:                 spec.S3SecretKey,
		GsBucket:                    spec.GsBucket,
		GsPrefix:                    spec.GsPrefix,
		GsServiceAccountKey:         spec.GsServiceAccountKey,
		DropboxAccessToken:          spec.DropboxAccessToken,
		DropboxFolder:               spec.DropboxFolder,
		SftpHost:                    spec.SftpHost,
		SftpPort:                    spec.SftpPort,
		SftpUser:                    spec.SftpUser,
		SftpPassword:                spec.SftpPassword,
		SftpFolder:                  spec.SftpFolder,
		SftpSSHKey:                  spec.SftpSSHKey,
		SftpSSHKeyPassphrase:        spec.SftpSSHKeyPassphrase,
		FilePassword:                spec.FilePassword,
		GpgPrivateKey:               spec.GpgPrivateKey,
		GpgPassphrase:               spec.GpgPassphrase,
	}

	return body
}

func mapToPatchBody(existing *CloudSourceFull, manifest *CloudSourceManifest) api.DataSourceModelPatchRequestSchema {
	spec := manifest.Spec

	body := api.DataSourceModelPatchRequestSchema{
		ID:                          derefInt(existing.ID),
		VersionID:                   derefInt(existing.VersionID),
		Name:                        stringPtr(manifest.Metadata.Name),
		DataSourceType:              spec.DataSourceType,
		DatasetID:                   &spec.DatasetID,
		SettingsModelID:             &spec.SettingsModelID,
		Schedule:                    spec.Schedule,
		Pattern:                     spec.Pattern,
		SimpleURL:                   spec.SimpleURL,
		ArchivePattern:              spec.ArchivePattern,
		ConnectionTimeout:           spec.ConnectionTimeout,
		DeleteSourceFileAfterBackup: spec.DeleteSourceFileAfterBackup,
		S3Bucket:                    spec.S3Bucket,
		S3RegionName:                spec.S3RegionName,
		S3Prefix:                    spec.S3Prefix,
		S3EndpointURL:               spec.S3EndpointURL,
		S3AccessKey:                 spec.S3AccessKey,
		S3SecretKey:                 spec.S3SecretKey,
		GsBucket:                    spec.GsBucket,
		GsPrefix:                    spec.GsPrefix,
		GsServiceAccountKey:         spec.GsServiceAccountKey,
		DropboxAccessToken:          spec.DropboxAccessToken,
		DropboxFolder:               spec.DropboxFolder,
		SftpHost:                    spec.SftpHost,
		SftpPort:                    spec.SftpPort,
		SftpUser:                    spec.SftpUser,
		SftpPassword:                spec.SftpPassword,
		SftpFolder:                  spec.SftpFolder,
		SftpSSHKey:                  spec.SftpSSHKey,
		SftpSSHKeyPassphrase:        spec.SftpSSHKeyPassphrase,
		FilePassword:                spec.FilePassword,
		GpgPrivateKey:               spec.GpgPrivateKey,
		GpgPassphrase:               spec.GpgPassphrase,
		State:                       existing.State,
	}

	return body
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
