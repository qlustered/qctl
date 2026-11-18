package datasets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// TableFieldChange is one manifest-authored value that differs from the live
// table. Paths are manifest paths: top-level table settings use spec.<key> and
// settings-model values use spec.settings_model.<key>.
type TableFieldChange struct {
	Path      string
	OldValue  json.RawMessage
	NewValue  json.RawMessage
	OldExists bool
	NewExists bool
}

// TableApplyPlan is the request qctl will send for one Table manifest. A plan
// owns the already-built body so a diff or confirmation cannot describe one
// snapshot and then rebuild a different request after the user approves it.
type TableApplyPlan struct {
	manifest    *TableManifest
	existing    *DatasetFull
	quickCreate *api.DataSetCreateQuickPostRequest
	fullCreate  *api.DataSetPostRequestSchema
	patch       *api.DataSetPatchRequestSchema
	patchNeeded bool
	tableKinds  *api.DatasetProblemDomainsPutRequestSchema
	concepts    *api.DatasetColumnConceptsPutRequestSchema
	changes     []TableFieldChange
}

// IsCreate reports whether applying the plan creates a new table.
func (p *TableApplyPlan) IsCreate() bool { return p != nil && p.existing == nil }

// Existing returns the table snapshot the update plan was built from. It is nil
// for a create plan.
func (p *TableApplyPlan) Existing() *DatasetFull {
	if p == nil {
		return nil
	}
	return p.existing
}

// Changes returns a copy of the manifest-scoped field changes in stable path
// order.
func (p *TableApplyPlan) Changes() []TableFieldChange {
	if p == nil {
		return nil
	}
	return append([]TableFieldChange(nil), p.changes...)
}

// RequestBody returns the exact body ApplyPrepared will send.
func (p *TableApplyPlan) RequestBody() any {
	if p == nil {
		return nil
	}
	switch {
	case p.patch != nil:
		if !p.patchNeeded {
			return nil
		}
		return datasetPatchWireBody{body: p.patch}
	case p.quickCreate != nil:
		return p.quickCreate
	default:
		return p.fullCreate
	}
}

// TableKindsBody returns the planned replacement request, if any.
func (p *TableApplyPlan) TableKindsBody() any {
	if p == nil || p.tableKinds == nil {
		return nil
	}
	return p.tableKinds
}

// ColumnConceptsBody returns the planned merge request, if any.
func (p *TableApplyPlan) ColumnConceptsBody() any {
	if p == nil || p.concepts == nil {
		return nil
	}
	return p.concepts
}

// PrepareApply resolves the live table and builds the exact create or update
// request without mutating anything.
func (c *Client) PrepareApply(accessToken string, manifest *TableManifest) (*TableApplyPlan, error) {
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
	if existing != nil {
		return c.prepareTableUpdate(accessToken, existing, manifest)
	}
	if err := c.refuseCreate(accessToken, manifest); err != nil {
		return nil, err
	}

	if canUseQuickCreate(manifest) {
		body := manifestToQuickPostRequest(manifest)
		changes, err := tableManifestChanges(nil, manifest, &body)
		if err != nil {
			return nil, err
		}
		kinds, concepts, assignmentChanges, err := c.prepareConceptAssignments(accessToken, nil, manifest.Spec)
		if err != nil {
			return nil, err
		}
		return &TableApplyPlan{manifest: manifest, quickCreate: &body, tableKinds: kinds, concepts: concepts, changes: sortedTableChanges(changes, assignmentChanges)}, nil
	}
	if errs := manifest.validateFullCreate(); len(errs) > 0 {
		return nil, qerrors.NewBadUsagef("manifest validation failed: %s", errs[0].Error())
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
	body, err := c.prepareFullCreateBody(apiClient, manifest)
	if err != nil {
		return nil, err
	}
	changes, err := tableManifestChanges(nil, manifest, &body)
	if err != nil {
		return nil, err
	}
	kinds, concepts, assignmentChanges, err := c.prepareConceptAssignments(accessToken, nil, manifest.Spec)
	if err != nil {
		return nil, err
	}
	return &TableApplyPlan{manifest: manifest, fullCreate: &body, tableKinds: kinds, concepts: concepts, changes: sortedTableChanges(changes, assignmentChanges)}, nil
}

// refuseCreate refuses, with exit 2, a create from a manifest that sets spec.column_standard_fields or whose
// status.id names a renamed table (refuseRenamedTable).
func (c *Client) refuseCreate(token string, manifest *TableManifest) error {
	if manifest.Spec.ColumnStandardFields != nil && len(*manifest.Spec.ColumnStandardFields) > 0 {
		return qerrors.NewBadUsage("spec.column_standard_fields requires an existing table schema; create the table and apply its schema first")
	}
	return c.refuseRenamedTable(token, manifest)
}

// refuseRenamedTable refuses, with exit 2, a create from a manifest whose status.id names a table of this
// organization under a name other than metadata.name. A status block of another organization, or a status.id the
// server answers with 403 or 404, refuses nothing.
func (c *Client) refuseRenamedTable(token string, manifest *TableManifest) error {
	status := manifest.status
	if status == nil || status.ID <= 0 {
		return nil
	}
	if org, err := uuid.Parse(status.OrganizationID); err == nil && org != c.organizationID {
		return nil
	}
	live, err := c.GetDataset(token, status.ID)
	if err != nil {
		switch qerrors.GetExitCode(err) {
		case qerrors.ExitNotFound, qerrors.ExitUnauthorized:
			return nil
		}
		return fmt.Errorf("look up status.id %d: %w", status.ID, err)
	}
	if live.Name == manifest.Metadata.Name {
		return nil
	}
	renamed := ""
	if live.Period != nil {
		renamed = " (its table series renamed it)"
	}
	return qerrors.NewBadUsagef("status.id %d names table %q, not %q%s; apply finds a table by metadata.name, so this file "+
		"would create a new table. Export table %d again with 'qctl get table %d -o yaml' to update it, or delete the "+
		"status block to create a new table", status.ID, live.Name, manifest.Metadata.Name, renamed, status.ID, status.ID)
}

func (c *Client) prepareTableUpdate(token string, existing *DatasetFull, manifest *TableManifest) (*TableApplyPlan, error) {
	if err := validateTableUpdate(existing, manifest); err != nil {
		return nil, err
	}
	body, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		return nil, err
	}
	changes, err := tableManifestChanges(existing, manifest, datasetPatchWireBody{body: &body})
	if err != nil {
		return nil, err
	}
	kinds, concepts, assignmentChanges, err := c.prepareConceptAssignments(token, existing, manifest.Spec)
	if err != nil {
		return nil, err
	}
	return &TableApplyPlan{manifest: manifest, existing: existing, patch: &body, patchNeeded: len(changes) > 0, tableKinds: kinds, concepts: concepts, changes: sortedTableChanges(changes, assignmentChanges)}, nil
}

func sortedTableChanges(base, assignments []TableFieldChange) []TableFieldChange {
	all := slices.Clone(base)
	all = append(all, assignments...)
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	return all
}

// ApplyPrepared executes a plan without resolving the table or rebuilding its
// body. The optimistic-concurrency ids in an update body protect the snapshot
// between preview and PATCH.
func (c *Client) ApplyPrepared(accessToken string, plan *TableApplyPlan) (*ApplyResult, error) {
	if plan == nil || plan.manifest == nil {
		return nil, fmt.Errorf("table apply plan is required")
	}

	if plan.IsCreate() {
		apiClient, err := client.New(client.Config{
			BaseURL:     c.baseURL,
			AccessToken: accessToken,
			Timeout:     c.timeout,
			Verbosity:   c.verbosity,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create API client: %w", err)
		}

		var created *DatasetFull
		switch {
		case plan.quickCreate != nil:
			created, err = c.createDatasetQuick(apiClient, accessToken, *plan.quickCreate)
		case plan.fullCreate != nil:
			created, err = c.createDatasetFull(apiClient, *plan.fullCreate)
		default:
			return nil, fmt.Errorf("table create plan has no request body")
		}
		if err != nil {
			return nil, err
		}
		if err := c.applyTableAssignments(accessToken, created.ID, plan); err != nil {
			return nil, fmt.Errorf("table created, but %w", err)
		}
		return &ApplyResult{
			Status:  "applied",
			Name:    created.Name,
			ID:      created.ID,
			Action:  "created",
			Message: "table created successfully",
		}, nil
	}

	if plan.patch == nil {
		return nil, fmt.Errorf("table update plan has no request body")
	}
	updated := plan.existing
	if plan.patchNeeded {
		var err error
		updated, err = c.updateDatasetWithBody(accessToken, *plan.patch)
		if err != nil {
			return nil, fmt.Errorf("table PATCH failed: %w", explainPiiColumnsRemoval(err, plan.existing, plan.patch))
		}
	}
	if err := c.applyTableAssignments(accessToken, updated.ID, plan); err != nil {
		if plan.patchNeeded {
			return nil, fmt.Errorf("table PATCH completed, but %w", err)
		}
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

func (c *Client) applyTableAssignments(token string, id int, plan *TableApplyPlan) error {
	if plan.tableKinds != nil {
		if err := c.putTableKinds(token, id, *plan.tableKinds); err != nil {
			return fmt.Errorf("table kinds PUT failed: %w", err)
		}
	}
	if plan.concepts != nil {
		if err := c.putColumnConcepts(token, id, *plan.concepts); err != nil {
			return fmt.Errorf("column standard fields PUT failed: %w", err)
		}
	}
	return nil
}

func validateTableUpdate(existing *DatasetFull, manifest *TableManifest) error {
	if manifest.Spec.DestinationID != existing.DestinationID {
		return qerrors.NewBadUsagef("table already exists with destination_id %d (cannot change destination)", existing.DestinationID)
	}
	if manifest.Spec.DatabaseName != existing.DatabaseName {
		return qerrors.NewBadUsagef("table already exists with database_name %s (cannot change database)", existing.DatabaseName)
	}
	if manifest.Spec.SchemaName != "" && manifest.Spec.SchemaName != existing.SchemaName {
		return qerrors.NewBadUsagef("table already exists with schema_name %s (cannot change schema)", existing.SchemaName)
	}
	if manifest.Spec.TableName != existing.TableName {
		return qerrors.NewBadUsagef("table already exists with table_name %s (cannot change table name)", existing.TableName)
	}
	if manifest.Spec.BackupSettingsID != existing.BackupSettingsID {
		return qerrors.NewBadUsagef("table already exists with backup_settings_id %d (cannot change backup settings)", existing.BackupSettingsID)
	}
	return nil
}

var immutableTableSpecKeys = map[string]struct{}{
	"backup_settings_id": {},
	"database_name":      {},
	"destination_id":     {},
	"schema_name":        {},
	"table_name":         {},
}

func tableManifestChanges(existing *DatasetFull, manifest *TableManifest, requestBody any) ([]TableFieldChange, error) {
	authored, err := jsonKeyMap(manifest.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the table manifest: %w", err)
	}
	request, err := jsonKeyMap(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the table request: %w", err)
	}
	live := map[string]json.RawMessage{}
	if existing != nil {
		live, err = jsonKeyMap(existing)
		if err != nil {
			return nil, fmt.Errorf("failed to encode the live table: %w", err)
		}
	}

	changes, err := topLevelTableChanges(existing, manifest, authored, live, request)
	if err != nil {
		return nil, err
	}
	settingsChanges, err := settingsModelChanges(existing, manifest.Spec.SettingsModel, manifest.clearKeyColumns, request)
	if err != nil {
		return nil, err
	}
	return append(changes, settingsChanges...), nil
}

func topLevelTableChanges(existing *DatasetFull, manifest *TableManifest, authored, live,
	request map[string]json.RawMessage) ([]TableFieldChange, error) {
	paths := make([]string, 0, len(authored))
	for key := range authored {
		if key == "settings_model" || key == "table_kinds" || key == "column_standard_fields" {
			continue
		}
		if key == "data_loading_process" && manifest.Spec.DataLoadingProcess == "" {
			continue
		}
		if _, immutable := immutableTableSpecKeys[key]; existing != nil && immutable {
			continue
		}
		paths = append(paths, key)
	}
	sort.Strings(paths)

	changes := make([]TableFieldChange, 0, len(paths)+1)
	if existing == nil {
		name, err := json.Marshal(manifest.Metadata.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to encode the table name: %w", err)
		}
		changes = append(changes, newTableFieldChange("metadata.name", nil, false, name, true))
	}
	for _, key := range paths {
		old, oldOK := live[key]
		newValue, newOK := request[key]
		if rawJSONEqual(old, oldOK, newValue, newOK) {
			continue
		}
		changes = append(changes, newTableFieldChange("spec."+key, old, oldOK, newValue, newOK))
	}
	return changes, nil
}

func settingsModelChanges(existing *DatasetFull, settingsModel *SettingsModel, clearKeyColumns bool,
	request map[string]json.RawMessage) ([]TableFieldChange, error) {
	if settingsModel == nil {
		return nil, nil
	}
	settingsAuthored, err := jsonKeyMap(settingsModel)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the manifest's settings model: %w", err)
	}
	if clearKeyColumns {
		settingsAuthored["key_columns"] = json.RawMessage("null")
	}
	liveSettings := map[string]json.RawMessage{}
	if existing != nil {
		liveSettings, err = jsonKeyMap(&existing.SettingsModel)
		if err != nil {
			return nil, fmt.Errorf("failed to encode the live settings model: %w", err)
		}
	}
	requestSettings := map[string]json.RawMessage{}
	if raw, ok := request["settings_model"]; ok && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &requestSettings); err != nil {
			return nil, fmt.Errorf("failed to decode the settings model request: %w", err)
		}
	}
	settingsKeys := make([]string, 0, len(settingsAuthored))
	for key := range settingsAuthored {
		settingsKeys = append(settingsKeys, key)
	}
	sort.Strings(settingsKeys)
	changes := make([]TableFieldChange, 0, len(settingsKeys))
	for _, key := range settingsKeys {
		old, oldOK := liveSettings[key]
		newValue, newOK := requestSettings[key]
		if rawJSONEqual(old, oldOK, newValue, newOK) {
			continue
		}
		changes = append(changes, newTableFieldChange("spec.settings_model."+key, old, oldOK, newValue, newOK))
	}
	return changes, nil
}

func rawJSONEqual(a json.RawMessage, aOK bool, b json.RawMessage, bOK bool) bool {
	return aOK == bOK && (!aOK || bytes.Equal(a, b))
}

func newTableFieldChange(path string, old json.RawMessage, oldOK bool, newValue json.RawMessage, newOK bool) TableFieldChange {
	return TableFieldChange{
		Path:      path,
		OldValue:  append(json.RawMessage(nil), old...),
		NewValue:  append(json.RawMessage(nil), newValue...),
		OldExists: oldOK,
		NewExists: newOK,
	}
}

// FormatTableFieldChanges renders one stable old-to-new line per changed
// manifest path. Both callers write the result straight to a terminal
// (`apply table`'s confirmation block and `diff table`), and the live side of
// every line is server-supplied JSON, so the whole block is sanitized once
// here. json.Marshal escapes C0 but not C1, so a U+009B in a live value would
// otherwise reach the terminal as a raw CSI introducer.
func FormatTableFieldChanges(changes []TableFieldChange, indent string) string {
	var b strings.Builder
	for _, change := range changes {
		fmt.Fprintf(&b, "%s%s: %s → %s\n", indent, change.Path,
			formatTableFieldValue(change.OldValue, change.OldExists),
			formatTableFieldValue(change.NewValue, change.NewExists))
	}
	return terminal.Sanitize(b.String())
}

func formatTableFieldValue(value json.RawMessage, exists bool) string {
	if !exists {
		return "<absent>"
	}
	return string(value)
}
