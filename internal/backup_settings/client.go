package backup_settings //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

func NewClient(baseURL, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{baseURL: baseURL, organizationID: orgID, verbosity: verbosity}, nil
}

type ListParams struct {
	SearchQuery *string
	OrderBy     string
	Reverse     bool
	Page        int
	Limit       int
}

func (c *Client) apiClient(accessToken string) (*client.Client, error) {
	return client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: accessToken, Timeout: c.timeout, Verbosity: c.verbosity,
	})
}

func (c *Client) List(accessToken string, params ListParams) (*ListPage, error) {
	apiClient, err := c.apiClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	apiParams := &api.GetBackupSettingsParams{SearchQuery: params.SearchQuery}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.BackupSettingsOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}
	resp, err := apiClient.API.GetBackupSettingsWithResponse(context.Background(), c.organizationID, apiParams)
	if err != nil {
		return nil, apiClient.HandleError(err, "list backup settings failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "list backup settings failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

func (c *Client) Get(accessToken string, id int) (*BackupSettingsFull, error) {
	apiClient, err := c.apiClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	resp, err := apiClient.API.GetOneBackupSettingsModelWithResponse(
		context.Background(), c.organizationID, id, &api.GetOneBackupSettingsModelParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "get backup settings failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get backup settings failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetByName sweeps the paginated list because Atlas exposes name in the
// projection but offers no name filter and does not search that column.
func (c *Client) GetByName(accessToken, name string) (*BackupSettingsFull, error) {
	var total *int
	seen := 0
	for page := 1; ; page++ {
		listed, err := c.List(accessToken, ListParams{OrderBy: "id", Page: page, Limit: 100})
		if err != nil {
			return nil, err
		}
		if page == 1 {
			total = listed.TotalRows
		}
		for _, item := range listed.Results {
			if item.Name != nil && *item.Name == name {
				return c.Get(accessToken, item.ID)
			}
		}
		seen += len(listed.Results)
		if len(listed.Results) == 0 {
			return nil, nil
		}
		if total != nil {
			if seen >= *total {
				return nil, nil
			}
		} else if listed.Next == nil {
			return nil, nil
		}
	}
}

func (c *Client) Create(accessToken string, manifest *Manifest) (*BackupSettingsFull, error) {
	if err := manifest.Spec.validateCreateCredentials(); err != nil {
		return nil, err
	}
	body := specBody(manifest.Spec)
	body["name"] = manifest.Metadata.Name
	return c.write(accessToken, http.MethodPost, body)
}

func (c *Client) Update(accessToken string, existing *BackupSettingsFull, manifest *Manifest) (*BackupSettingsFull, error) {
	body := specBody(manifest.Spec)
	body["id"] = existing.ID
	body["version_id"] = existing.VersionID
	if manifest.Metadata.Name != "" {
		body["name"] = manifest.Metadata.Name
	}
	return c.write(accessToken, http.MethodPatch, body)
}

func (c *Client) write(accessToken, method string, body map[string]any) (*BackupSettingsFull, error) {
	apiClient, err := c.apiClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode backup settings request: %w", err)
	}
	var respBody []byte
	var status int
	switch method {
	case http.MethodPost:
		resp, err := apiClient.API.PostBackupSettingsModelWithBodyWithResponse(
			context.Background(), c.organizationID, &api.PostBackupSettingsModelParams{}, "application/json", bytes.NewReader(raw))
		if err != nil {
			return nil, apiClient.HandleError(err, "create backup settings failed")
		}
		status, respBody = resp.StatusCode(), resp.Body
		if status == http.StatusOK && resp.JSON200 != nil {
			return resp.JSON200, nil
		}
	case http.MethodPatch:
		resp, err := apiClient.API.PatchBackupSettingsModelWithBodyWithResponse(
			context.Background(), c.organizationID, &api.PatchBackupSettingsModelParams{}, "application/json", bytes.NewReader(raw))
		if err != nil {
			return nil, apiClient.HandleError(err, "update backup settings failed")
		}
		status, respBody = resp.StatusCode(), resp.Body
		if status == http.StatusOK && resp.JSON200 != nil {
			return resp.JSON200, nil
		}
	default:
		return nil, fmt.Errorf("unsupported backup settings method %s", method)
	}
	if status != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(status, respBody, "write backup settings failed")
	}
	return nil, fmt.Errorf("unexpected empty response")
}

func (c *Client) Apply(accessToken string, manifest *Manifest) (*ApplyResult, error) {
	existing, err := c.applyTarget(accessToken, manifest)
	if err != nil {
		return nil, fmt.Errorf("look up backup settings: %w", err)
	}
	action := "created"
	var result *BackupSettingsFull
	if existing == nil {
		result, err = c.Create(accessToken, manifest)
	} else {
		action = "updated"
		result, err = c.Update(accessToken, existing, manifest)
	}
	if err != nil {
		return nil, err
	}
	return &ApplyResult{Name: deref(result.Name), Action: action, ID: result.ID}, nil
}

func (c *Client) applyTarget(accessToken string, manifest *Manifest) (*BackupSettingsFull, error) {
	if manifest.Metadata.ID != "" {
		id, err := manifest.targetID()
		if err != nil {
			return nil, err
		}
		return c.Get(accessToken, id)
	}
	return c.GetByName(accessToken, manifest.Metadata.Name)
}

func specBody(spec Spec) map[string]any {
	body := map[string]any{}
	put(body, "backup_type", spec.BackupType)
	put(body, "backup_gpg_public_key", spec.BackupGPGPublicKey)
	put(body, "backup_gpg_recipient", spec.BackupGPGRecipient)
	put(body, "backup_gpg_private_key", spec.BackupGPGPrivateKey)
	put(body, "backup_gpg_passphrase", spec.BackupGPGPassphrase)
	put(body, "backup_s3_access_key", spec.BackupS3AccessKey)
	put(body, "backup_s3_secret_key", spec.BackupS3SecretKey)
	put(body, "backup_s3_endpoint_url", spec.BackupS3EndpointURL)
	put(body, "backup_s3_region_name", spec.BackupS3RegionName)
	put(body, "backup_s3_default_buffer_size", spec.BackupS3DefaultBufferSize)
	put(body, "backup_s3_bucket", spec.BackupS3Bucket)
	put(body, "backup_gs_service_account_key", spec.BackupGSServiceAccountKey)
	put(body, "backup_gs_bucket", spec.BackupGSBucket)
	return body
}

func put[T any](body map[string]any, key string, value *T) {
	if value != nil {
		body[key] = *value
	}
}
