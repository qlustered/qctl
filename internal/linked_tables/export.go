package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// LiveTable is the linked table's own settings an export reads.
type LiveTable struct {
	ID               int
	Name             string
	Database         string
	Schema           string
	TableName        string
	Destination      string
	BackupSettingsID int
	// BackupSettings is the backup settings' name; nil leaves
	// spec.backup_settings out.
	BackupSettings *string
}

// SourceDatasetIDs returns the distinct dataset ids detail's sources read,
// in ascending order.
func SourceDatasetIDs(detail *RevisionDetail) []int {
	ids := map[int]bool{}
	for _, src := range detail.Revision.Sources {
		ids[src.DatasetID] = true
	}
	return slices.Sorted(maps.Keys(ids))
}

// ExportManifest returns the manifest of table as detail specifies it, with a
// status block. datasetNames names each id SourceDatasetIDs returns. The spec
// has no release. pendingAutoEnable means a dry run is waiting to enable this
// revision.
func ExportManifest(table LiveTable, detail *RevisionDetail, datasetNames map[int]string, pendingAutoEnable bool) (*LinkedTableManifestWithStatus, error) {
	revision, group := detail.Revision, detail.Group

	spine, err := spineAlias(revision.Sources, group.SpineDatasetID)
	if err != nil {
		return nil, err
	}

	sources := make(map[string]SourceSpec, len(revision.Sources))
	for alias, src := range revision.Sources {
		name, ok := datasetNames[src.DatasetID]
		if !ok {
			return nil, fmt.Errorf("no name for source table %d (spec.sources.%s)", src.DatasetID, alias)
		}
		spec := SourceSpec{Dataset: name, Arity: src.Arity}
		if len(src.JoinFields) > 0 {
			spec.JoinFields = make(map[string]JoinField, len(src.JoinFields))
			for column, raw := range src.JoinFields {
				field, err := joinFieldFromJSON(raw)
				if err != nil {
					return nil, qerrors.NewAPIIncompatible(fmt.Sprintf(
						"spec revision %s: sources.%s.join_fields.%s cannot be written as a manifest: %v",
						detail.ID, alias, column, err))
				}
				spec.JoinFields[column] = field
			}
		}
		sources[alias] = spec
	}

	desired := "draft" //nolint:goconst // Desired state is a manifest value.
	if detail.State == stateEnabled || pendingAutoEnable {
		desired = stateEnabled
	}
	identityHashVersion := group.IdentityHashVersion
	schema := table.Schema
	release := ""
	if revision.Release != nil {
		release = *revision.Release
	}

	return &LinkedTableManifestWithStatus{
		LinkedTableManifest: LinkedTableManifest{
			APIVersion: pkgmanifest.APIVersionV1,
			Kind:       kindLinkedTable,
			Metadata:   pkgmanifest.Metadata{Name: group.Name},
			Spec: LinkedTableSpec{
				Destination:    table.Destination,
				BackupSettings: table.BackupSettings,
				Table:          TableSpec{Database: table.Database, Schema: &schema, Name: table.TableName},
				Spine: SpineSpec{
					Source:                  spine,
					NaturalKeyColumns:       group.NaturalKeyColumns,
					NaturalKeyNormalization: group.NaturalKeyNormalization,
					IdentityHashVersion:     &identityHashVersion,
				},
				Sources:      sources,
				Output:       revision.Output,
				DesiredState: &desired,
				Description:  revision.Description,
			},
		},
		Status: &LinkedTableStatus{
			TableID:              table.ID,
			CompositeSpecGroupID: detail.GroupID,
			Slug:                 group.Slug,
			RevisionID:           detail.ID,
			Release:              release,
			State:                detail.State,
			BackupSettingsID:     table.BackupSettingsID,
		},
	}, nil
}

// Summary is the one-row view of an exported linked table.
type Summary struct {
	Name          string `json:"name"`
	TableID       int    `json:"table_id"`
	Release       string `json:"release"`
	State         string `json:"state"`
	Spine         string `json:"spine"`
	Sources       int    `json:"sources"`
	OutputColumns int    `json:"output_columns"`
	Destination   string `json:"destination"`
}

// SummaryOf returns the one-row view of m, a manifest ExportManifest built.
func SummaryOf(m *LinkedTableManifestWithStatus) *Summary {
	return &Summary{
		Name:          m.Metadata.Name,
		TableID:       m.Status.TableID,
		Release:       m.Status.Release,
		State:         m.Status.State,
		Spine:         m.Spec.Spine.Source,
		Sources:       len(m.Spec.Sources),
		OutputColumns: len(m.Spec.Output),
		Destination:   m.Spec.Destination,
	}
}

// spineAlias returns the one alias in sources that reads spineDatasetID.
func spineAlias(sources map[string]RevisionSource, spineDatasetID int) (string, error) {
	var aliases []string
	for alias, src := range sources {
		if src.DatasetID == spineDatasetID {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) != 1 {
		slices.Sort(aliases)
		return "", qerrors.NewAPIIncompatible(fmt.Sprintf(
			"the spec revision should have exactly one source reading the spine table %d, but has %d [%s]",
			spineDatasetID, len(aliases), strings.Join(aliases, ", ")))
	}
	return aliases[0], nil
}

// joinFieldFromJSON reads a join_fields value as the server sends it: a JSON
// string is a shorthand, and an object is a clause whose constant keeps each
// number as a json.Number. A clause key JoinClause lacks is an error.
func joinFieldFromJSON(raw json.RawMessage) (JoinField, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var shorthand string
		if err := json.Unmarshal(trimmed, &shorthand); err != nil {
			return JoinField{}, err
		}
		return JoinField{Shorthand: shorthand}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var clause JoinClause
	if err := decoder.Decode(&clause); err != nil {
		return JoinField{}, err
	}
	return JoinField{Clause: &clause}, nil
}
