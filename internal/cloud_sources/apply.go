package cloud_sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// ApplyResult represents the result of an apply operation.
type ApplyResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Action  string `json:"action,omitempty"` // "created", "updated" or "unchanged"
	ID      int    `json:"id,omitempty"`
	// State is the source's state as the create response or the update's read
	// reported it.
	State string `json:"state,omitempty"`
}

// csvDialectKeys are the wire keys of the CSV dialect fields.
var csvDialectKeys = map[string]bool{
	"csv_delimiter":  true,
	"csv_quotechar":  true,
	"csv_escapechar": true,
}

// boundaryKeys are the wire keys of DataSourceModel.FIELDS_THAT_INVALIDATE_BOUNDARY_DATA.
var boundaryKeys = map[string]bool{
	"fields_to_expand_override":           true,
	"infer_datetime_override":             true,
	"infer_datetime_for_columns_override": true,
	"ignore_column_ids":                   true,
	"ignore_column_names_override":        true,
}

// FieldChange is one field an update changes.
type FieldChange struct {
	// Path is the manifest path: spec.<key> or spec.settings_overrides.<name>.
	Path string
	// Key is the wire key.
	Key string
	// Old is the live value, or nil when it is null.
	Old json.RawMessage
	// New is the value sent, or nil when it is null.
	New json.RawMessage
	// Secret marks a credential.
	Secret bool
}

// Verb is CLEAR when the new value is null, SET when the live value is null or
// "", and REPLACE otherwise.
func (c FieldChange) Verb() string {
	switch {
	case c.New == nil:
		return "CLEAR"
	case c.Old == nil || bytes.Equal(c.Old, []byte(`""`)):
		return "SET"
	default:
		return "REPLACE"
	}
}

// ApplyPlan is the create or update apply performs for a CloudSource manifest.
type ApplyPlan struct {
	manifest *CloudSourceManifest
	existing *CloudSourceFull
	body     []byte
	changes  []FieldChange
	warnings []string
}

// Warnings returns warnings about the planned update.
func (p *ApplyPlan) Warnings() []string { return append([]string(nil), p.warnings...) }

// IsCreate reports whether the plan creates a cloud source.
func (p *ApplyPlan) IsCreate() bool { return p.existing == nil }

// CreatesDraft reports whether the plan creates a cloud source as a draft.
func (p *ApplyPlan) CreatesDraft() bool { return p.IsCreate() && p.manifest.Spec.CreatesDraft() }

// Existing returns the cloud source an update was planned against, or nil.
func (p *ApplyPlan) Existing() *CloudSourceFull { return p.existing }

// Changes returns the fields an update changes, CloudSourceFields keys first,
// then overrides, each in key order.
func (p *ApplyPlan) Changes() []FieldChange {
	return append([]FieldChange(nil), p.changes...)
}

// StaleExport returns the manifest's status.version_id and the live version
// when status names this cloud source at an older version than the live one.
func (p *ApplyPlan) StaleExport() (exported, live int, ok bool) {
	if p.existing == nil || p.manifest.Status == nil {
		return 0, 0, false
	}
	status := p.manifest.Status
	live = derefInt(p.existing.VersionID)
	if status.ID != derefInt(p.existing.ID) || status.VersionID <= 0 || status.VersionID >= live {
		return 0, 0, false
	}
	return status.VersionID, live, true
}

// SideEffects describes the side effects the planned update may trigger.
func (p *ApplyPlan) SideEffects() []string {
	var boundary, dialect bool
	for _, c := range p.changes {
		boundary = boundary || boundaryKeys[c.Key]
		dialect = dialect || (csvDialectKeys[c.Key] && (!blankJSON(c.Old) || !blankJSON(c.New)))
	}
	var effects []string
	if boundary {
		effects = append(effects, "The server may queue the table to learn its schema again if the caller is not external, a file of this source is enabled for training, and the table has a draft migration or has files but no migration yet.")
	}
	if dialect {
		effects = append(effects, "When a CSV dialect field changes, the server resolves the unresolved cant_sniff_header alerts on this source that you may resolve and requeues their unfinished ingestion jobs.")
	}
	return effects
}

// blankJSON reports whether raw is null, a nil one meaning null, or "".
func blankJSON(raw json.RawMessage) bool {
	return raw == nil || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`))
}

// PrepareApply reads the cloud source manifest names and builds the exact
// create or update request, without changing anything.
//
// An update's desired state is the whole manifest: each field setFields
// returns and each override the manifest sets is the manifest's value; any
// other override, and any other field ClearedWhenOmittedKeys lists, is null;
// every other field is the stored value. The request carries only the fields
// whose desired value differs from the stored one, plus supplied secrets on a
// repointing update, with the id and version_id read.
func (c *Client) PrepareApply(accessToken string, manifest *CloudSourceManifest) (*ApplyPlan, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is required")
	}
	if errs := manifest.Validate(); len(errs) > 0 {
		return nil, qerrors.NewBadUsagef("manifest validation failed: %s", errs[0].Error())
	}
	// createBody dereferences spec.settings_model_id.
	if manifest.Spec.SettingsModelID == nil {
		return nil, fmt.Errorf("internal error: spec.settings_model_id was not resolved before apply")
	}

	existing, live, err := c.getCloudSourceByName(accessToken, manifest.Spec.DatasetID, manifest.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up cloud source: %w", err)
	}
	if existing == nil {
		if errs := validateCreateCredentials(manifest.Spec, manifest.DataSourceTypeValue()); len(errs) > 0 {
			return nil, qerrors.NewBadUsagef("manifest validation failed: %s", errs[0].Error())
		}
		body, err := createBody(manifest)
		if err != nil {
			return nil, err
		}
		return &ApplyPlan{manifest: manifest, body: body}, nil
	}
	return planUpdate(manifest, existing, live)
}

// planUpdate builds the update that brings existing, read as the JSON body
// live, to manifest's desired state.
func planUpdate(manifest *CloudSourceManifest, existing *CloudSourceFull, live []byte) (*ApplyPlan, error) {
	if existing.DatasetID != nil && *existing.DatasetID != manifest.Spec.DatasetID {
		return nil, qerrors.NewBadUsagef("cloud source already exists with dataset_id %d (cannot change dataset_id)", *existing.DatasetID)
	}
	if existing.DataSourceType != nil && manifest.Spec.DataSourceType != nil && *existing.DataSourceType != *manifest.Spec.DataSourceType {
		return nil, qerrors.NewBadUsagef("cloud source already exists with data_source_type %s (cannot change type)", *existing.DataSourceType)
	}
	if existing.ID == nil || existing.VersionID == nil {
		return nil, fmt.Errorf("the read of cloud source %q carried no id or version_id; cannot apply", manifest.Metadata.Name)
	}
	if manifest.Spec.State != nil {
		return nil, qerrors.NewBadUsagef("cloud source %q already exists (id %d), and spec.state is accepted only when apply creates the source: "+
			"remove spec.state from the manifest, and change its state with 'qctl enable cloud-source %d' or 'qctl disable cloud-source %d'",
			manifest.Metadata.Name, *existing.ID, *existing.ID, *existing.ID)
	}

	liveMembers, err := nonNullMembers(live)
	if err != nil {
		return nil, fmt.Errorf("failed to decode the cloud source: %w", err)
	}
	changes, err := updateChanges(&manifest.Spec, liveMembers)
	if err != nil {
		return nil, err
	}
	if err := refuseSeriesChange(manifest.Metadata.Name, *existing.ID, changes); err != nil {
		return nil, err
	}
	patch := map[string]json.RawMessage{}
	for _, change := range changes {
		patch[change.Key] = orNull(change.New)
	}
	warnings, err := prepareRepointSecrets(manifest, existing, liveMembers, changes, patch)
	if err != nil {
		return nil, err
	}
	patch["id"] = json.RawMessage(fmt.Sprint(*existing.ID))
	patch["version_id"] = json.RawMessage(fmt.Sprint(*existing.VersionID))
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to encode cloud source update: %w", err)
	}
	return &ApplyPlan{manifest: manifest, existing: existing, body: body, changes: changes, warnings: warnings}, nil
}

// refuseSeriesChange returns a bad-usage error when changes include
// table_series_id on the cloud source name with id.
func refuseSeriesChange(name string, id int, changes []FieldChange) error {
	for _, change := range changes {
		if change.Key != "table_series_id" {
			continue
		}
		liveSeries := "none"
		var series string
		if json.Unmarshal(change.Old, &series) == nil && series != "" {
			liveSeries = series
		}
		return qerrors.NewBadUsagef("cloud source %q already exists (id %d) with table series %s, and spec.table_series_id is accepted only when apply creates the source: "+
			"remove spec.table_series_id from the manifest or set it back to the live value; a source for another series is a new cloud source",
			name, id, liveSeries)
	}
	return nil
}

// ApplyPrepared sends the plan's request, or nothing for an update with no
// changes. A 409 on an update means the cloud source changed after the plan
// read it.
func (c *Client) ApplyPrepared(accessToken string, plan *ApplyPlan) (*ApplyResult, error) {
	if !plan.IsCreate() && len(plan.changes) == 0 {
		return &ApplyResult{
			Status:  "unchanged",
			Name:    plan.manifest.Metadata.Name,
			ID:      derefInt(plan.existing.ID),
			Action:  "unchanged",
			Message: "cloud source already matches the manifest",
			State:   string(derefDataSourceState(plan.existing.State)),
		}, nil
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

	if plan.IsCreate() {
		resp, err := apiClient.API.PostDataSourceModelWithBodyWithResponse(
			context.Background(), c.organizationID, &api.PostDataSourceModelParams{},
			"application/json", bytes.NewReader(plan.body))
		if err != nil {
			return nil, apiClient.HandleError(err, "create cloud source failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "create cloud source failed")
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		return &ApplyResult{
			Status:  "applied",
			Name:    resp.JSON200.Name,
			ID:      resp.JSON200.ID,
			Action:  "created",
			Message: "cloud source created successfully",
			State:   string(resp.JSON200.State),
		}, nil
	}

	id := derefInt(plan.existing.ID)
	resp, err := apiClient.API.PatchDataSourceModelWithBodyWithResponse(
		context.Background(), c.organizationID, id, &api.PatchDataSourceModelParams{},
		"application/json", bytes.NewReader(plan.body))
	if err != nil {
		return nil, apiClient.HandleError(err, "update cloud source failed")
	}
	if resp.StatusCode() == http.StatusConflict {
		err := apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "update cloud source failed")
		return nil, fmt.Errorf("%w\n\ncloud source %q changed after apply read it at version %d; re-run apply to read it again",
			err, terminal.Sanitize(plan.manifest.Metadata.Name), derefInt(plan.existing.VersionID))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "update cloud source failed")
	}
	return &ApplyResult{
		Status:  "applied",
		Name:    plan.manifest.Metadata.Name,
		ID:      id,
		Action:  "updated",
		Message: "cloud source updated successfully",
		State:   string(derefDataSourceState(plan.existing.State)),
	}, nil
}

// getCloudSourceByName returns the cloud source named name in the dataset,
// decoded and as the JSON body it was decoded from, or nil when there is none.
func (c *Client) getCloudSourceByName(accessToken string, datasetID int, name string) (*CloudSourceFull, []byte, error) {
	results, _, err := c.GetAllCloudSources(accessToken, GetCloudSourcesParams{
		DatasetID:   &datasetID,
		SearchQuery: &name,
	}, 0)
	if err != nil {
		return nil, nil, err
	}
	var matches []CloudSourceTiny
	for _, cs := range results {
		if cs.Name == name {
			matches = append(matches, cs)
		}
	}
	if len(matches) == 0 {
		return nil, nil, nil
	}
	if len(matches) > 1 {
		return nil, nil, fmt.Errorf("multiple cloud sources named %q found for dataset %d", name, datasetID)
	}
	return c.GetCloudSourceBody(accessToken, matches[0].ID)
}

// createBody is the POST body for manifest: its name, identity, spec.state
// when set, every field setFields returns and every override it sets.
func createBody(manifest *CloudSourceManifest) ([]byte, error) {
	spec := &manifest.Spec
	members, err := setFields(spec)
	if err != nil {
		return nil, err
	}
	overrides, err := encodeMembers(spec.SettingsOverrides)
	if err != nil {
		return nil, fmt.Errorf("failed to encode spec.settings_overrides: %w", err)
	}
	identity, err := encodeMembers(map[string]any{
		"name":              manifest.Metadata.Name,
		"state":             spec.State,
		"dataset_id":        spec.DatasetID,
		"data_source_type":  spec.DataSourceType,
		"settings_model_id": *spec.SettingsModelID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode cloud source identity: %w", err)
	}
	return createBodyFromMembers(members, overrides, identity)
}

// setFields returns the CloudSourceFields members spec sets, leaving out a
// secret whose value is obfuscated.
func setFields(spec *CloudSourceSpec) (map[string]json.RawMessage, error) {
	members, err := encodeMembers(spec.CloudSourceFields)
	if err != nil {
		return nil, fmt.Errorf("failed to encode spec: %w", err)
	}
	for key, value := range members {
		if isCopyCredential(key) && obfuscated(value) {
			delete(members, key)
		}
	}
	return members, nil
}

// updateChanges compares spec's desired state with live, the non-null members
// of the stored cloud source.
func updateChanges(spec *CloudSourceSpec, live map[string]json.RawMessage) ([]FieldChange, error) {
	set, err := setFields(spec)
	if err != nil {
		return nil, err
	}
	overrides, err := encodeMembers(spec.SettingsOverrides)
	if err != nil {
		return nil, fmt.Errorf("failed to encode spec.settings_overrides: %w", err)
	}

	var changes []FieldChange
	add := func(path, key string, desired json.RawMessage, secret bool) error {
		old := live[key]
		same, err := sameJSON(old, desired)
		if err != nil {
			return fmt.Errorf("failed to compare %s: %w", path, err)
		}
		if !same {
			changes = append(changes, FieldChange{Path: path, Key: key, Old: old, New: desired, Secret: secret})
		}
		return nil
	}
	for _, key := range fieldKeys {
		desired, ok := set[key]
		if !ok && !clearedWhenOmitted(key) {
			continue
		}
		if err := add("spec."+key, key, desired, isCopyCredential(key)); err != nil {
			return nil, err
		}
	}
	for _, name := range overrideNames {
		if err := add("spec.settings_overrides."+name, name+overrideSuffix, overrides[name], false); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

// sameJSON reports whether a and b are the same JSON value, a nil one meaning
// null.
func sameJSON(a, b json.RawMessage) (bool, error) {
	ca, err := canonicalJSON(a)
	if err != nil {
		return false, err
	}
	cb, err := canonicalJSON(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ca, cb), nil
}

// canonicalJSON re-encodes raw, a nil one meaning null, compactly with sorted
// object keys and without HTML escaping.
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(orNull(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func orNull(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return json.RawMessage("null")
	}
	return raw
}

// FormatChanges renders one line per change: its verb, its path, and its old
// and new values as displayJSON, or "(secret, not shown)" for a credential.
func FormatChanges(changes []FieldChange, indent string) string {
	var b strings.Builder
	for _, change := range changes {
		fmt.Fprintf(&b, "%s%-7s  %s: ", indent, change.Verb(), change.Path)
		if change.Secret {
			b.WriteString("(secret, not shown)\n")
			continue
		}
		fmt.Fprintf(&b, "%s → %s\n", displayJSON(change.Old), displayJSON(change.New))
	}
	return terminal.Sanitize(b.String())
}

// displayJSON is raw as canonicalJSON, or as it is when it does not decode.
func displayJSON(raw json.RawMessage) []byte {
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return orNull(raw)
	}
	return canonical
}

// nonNullMembers decodes raw as a JSON object and returns its members whose
// value is not null.
func nonNullMembers(raw []byte) (map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	for key, value := range members {
		if isNull(value) {
			delete(members, key)
		}
	}
	return members, nil
}
