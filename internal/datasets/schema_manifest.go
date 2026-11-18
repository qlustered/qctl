package datasets

import (
	"fmt"
	"sort"
	"strconv"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// TableSchemaKind is the manifest kind `apply -f` and `diff -f` dispatch on.
const TableSchemaKind = "TableSchema"

// FieldOverride is one column's desired definition.
//
// Every property but Type is a pointer, and that is load-bearing rather than
// stylistic: an omitted property means "keep what is there", and only the
// properties a block actually states take part in the comparison
// BuildSchemaOverrides makes. A value type could not tell `indexed: false`
// from an omitted `indexed`.
//
// display_decimals is the one property with three states rather than two,
// because unset is a value the backend gives its own meaning — the default
// places for the column's flags — so a block has to be able to ask for it.
// It is carried by two fields: DisplayDecimals for a stated number, and
// ResetDisplayDecimals for a stated null.
type FieldOverride struct {
	Type            string    `yaml:"type" json:"type"`
	Args            *[]int    `yaml:"args,omitempty" json:"args,omitempty"`
	IsNullable      *bool     `yaml:"is_nullable,omitempty" json:"is_nullable,omitempty"`
	Indexed         *bool     `yaml:"indexed,omitempty" json:"indexed,omitempty"`
	TrgmIndexed     *bool     `yaml:"trgm_indexed,omitempty" json:"trgm_indexed,omitempty"`
	IsDeleted       *bool     `yaml:"is_deleted,omitempty" json:"is_deleted,omitempty"`
	IsDollar        *bool     `yaml:"is_dollar,omitempty" json:"is_dollar,omitempty"`
	IsPercent       *bool     `yaml:"is_percent,omitempty" json:"is_percent,omitempty"`
	DisplayDecimals *int      `yaml:"display_decimals,omitempty" json:"display_decimals,omitempty"`
	DatetimeFormats *[]string `yaml:"datetime_formats,omitempty" json:"datetime_formats,omitempty"`

	// ResetDisplayDecimals records `display_decimals: null` — put the column
	// back to the backend's default places. markResetDisplayDecimals sets it
	// from a second read of the document, and only where the block writes that
	// null, so DisplayDecimals is nil wherever it is true.
	//
	// It is not decoded, because no type in this struct could be: yaml.v3
	// resolves a null scalar to the destination's ZERO VALUE without consulting
	// a custom unmarshaler (prepare() in gopkg.in/yaml.v3/decode.go returns
	// before the Unmarshaler check when the node carries the null tag), which
	// is the same nil an omitted line produces.
	//
	// It is not emitted either: `get table-schema -o yaml` omits
	// display_decimals for a column that has none, so an unedited file asks for
	// no reset.
	ResetDisplayDecimals bool `yaml:"-" json:"-"`

	// OriginalName is declared only so it can be rejected with a message that
	// says why. Without the field, strict parsing rejects it as an anonymous
	// unknown key. It is never emitted: it records what the column was called
	// in the source data, and the route cannot rename anything
	// (cosmosview useFieldTypeModificationMutation.ts, the
	// FieldTypeModificationParams type, has the frontend's rename feature
	// commented out for the same reason).
	OriginalName *string `yaml:"original_name,omitempty" json:"original_name,omitempty"`
}

// TableSchemaSpec is the manifest's spec block.
//
// Fields is a map to POINTERS so that `field: null` — the one explicit way to
// clear a field's override and go back to the inferred definition — is
// distinguishable from an omitted key and from an empty block.
type TableSchemaSpec struct {
	Fields map[string]*FieldOverride `yaml:"fields" json:"fields"`
}

// TableSchemaStatus is emitted by `get` and ignored by `apply`, exactly as
// TableStatus is on the Table manifest.
type TableSchemaStatus struct {
	SchemaState             string `yaml:"schema_state" json:"schema_state"`
	State                   string `yaml:"state" json:"state"`
	Confirmed               bool   `yaml:"confirmed" json:"confirmed"`
	MigrationID             int    `yaml:"migration_id" json:"migration_id"`
	SettingsModelVersionID  int    `yaml:"settings_model_version_id" json:"settings_model_version_id"`
	MigrationModelVersionID int    `yaml:"migration_model_version_id" json:"migration_model_version_id"`
}

// TableSchemaManifest is the declarative representation of a table's schema.
type TableSchemaManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       TableSchemaSpec      `yaml:"spec" json:"spec"`
}

// TableSchemaManifestWithStatus extends the manifest with runtime status.
// apply unmarshals into THIS type so strict parsing tolerates the status block
// `get` emits — the same trick ParseTableManifest (internal/datasets/manifest.go)
// plays with tableManifestInput.
type TableSchemaManifestWithStatus struct {
	TableSchemaManifest `yaml:",inline" json:",inline"`
	Status              *TableSchemaStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// SchemaDetailToManifest renders the live schema as the manifest
// `get table-schema -o yaml|json` emits.
//
// It reads each field through displayDefinition, the same picker the grid uses,
// so the manifest and the grid can never disagree: the draft's definition when
// a draft exists, falling back to current for a field the draft does not carry.
// A field the draft removes is emitted either way — with `is_deleted: true`
// when the draft deletes it, and with its current definition when the draft
// has dropped it, which an edit then overrides.
func SchemaDetailToManifest(detail *TableSchemaDetail) *TableSchemaManifestWithStatus {
	fields := make(map[string]*FieldOverride, len(detail.Fields))
	for name, info := range detail.Fields {
		if def := displayDefinition(detail.MigrationState, info); def != nil {
			fields[name] = fieldOverrideFromDetail(def)
		}
	}
	return &TableSchemaManifestWithStatus{
		TableSchemaManifest: TableSchemaManifest{
			APIVersion: pkgmanifest.APIVersionV1,
			Kind:       TableSchemaKind,
			Metadata: pkgmanifest.Metadata{
				Name: detail.DatasetName,
				ID:   strconv.Itoa(detail.DatasetID),
			},
			Spec: TableSchemaSpec{Fields: fields},
		},
		Status: &TableSchemaStatus{
			SchemaState:             string(detail.MigrationState),
			State:                   string(detail.State),
			Confirmed:               detail.Confirmed,
			MigrationID:             detail.MigrationID,
			SettingsModelVersionID:  detail.SettingsModelVersionID,
			MigrationModelVersionID: detail.MigrationModelVersionID,
		},
	}
}

// fieldOverrideFromDetail copies a live definition's writable properties.
//
// A property that is nil in the live definition is OMITTED rather than emitted
// as null, both to keep the file readable and so that `null` stays
// unambiguous: at field level it means "clear this override", and it never
// appears at property level. is_deleted is copied only when true.
// original_name is not copied — see FieldOverride.
func fieldOverrideFromDetail(d *FieldSchemaDetail) *FieldOverride {
	f := &FieldOverride{
		Type:            SchemaTypeName(d),
		IsNullable:      copyBoolPtr(d.IsNullable),
		Indexed:         copyBoolPtr(d.Indexed),
		TrgmIndexed:     copyBoolPtr(d.TrgmIndexed),
		IsDollar:        copyBoolPtr(d.IsDollar),
		IsPercent:       copyBoolPtr(d.IsPercent),
		DisplayDecimals: copyIntPtr(d.DisplayDecimals),
	}
	// A stated is_deleted is a request whichever value it holds, and
	// refuseUnsendableOverride refuses one beside any other change. The details
	// route reports false on every live column (atlas
	// capi/capi/controllers/migration_controller.py
	// _extract_field_properties defaults it), so copying a false would make
	// every edit of the emitted file exit 2.
	if d.IsDeleted != nil && *d.IsDeleted {
		deleted := true
		f.IsDeleted = &deleted
	}
	if d.Args != nil && len(*d.Args) > 0 {
		args := append([]int(nil), (*d.Args)...)
		f.Args = &args
	}
	if d.DatetimeFormats != nil && len(*d.DatetimeFormats) > 0 {
		formats := append([]string(nil), (*d.DatetimeFormats)...)
		f.DatetimeFormats = &formats
	}
	return f
}

func copyIntPtr(n *int) *int {
	if n == nil {
		return nil
	}
	v := *n
	return &v
}

func copyBoolPtr(b *bool) *bool {
	if b == nil {
		return nil
	}
	v := *b
	return &v
}

// ParseTableSchemaManifest strict-parses one manifest document. source is what
// a message calls the document ("f.yaml", or "f.yaml[2]" in a multi-document
// file). Every failure exits 2.
func ParseTableSchemaManifest(data []byte, source string) (*TableSchemaManifest, error) {
	var tolerant TableSchemaManifestWithStatus
	if err := pkgmanifest.StrictUnmarshal(data, &tolerant); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", source))
	}
	m := &tolerant.TableSchemaManifest
	if m.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("unsupported apiVersion %q in %s (expected: %s)", m.APIVersion, source, pkgmanifest.APIVersionV1)
	}
	if m.Kind != TableSchemaKind {
		return nil, qerrors.NewBadUsagef("unsupported kind %q in %s (expected: %s)", m.Kind, source, TableSchemaKind)
	}
	if m.Metadata.Name == "" && m.Metadata.ID == "" {
		return nil, qerrors.NewBadUsagef("metadata.name or metadata.id is required in %s: it names the table whose schema this is", source)
	}
	// `qctl explain table-schema` marks both `spec` and `spec.fields` required,
	// and this is where that becomes true. ONE check covers both, because a
	// manifest with no spec block has no spec.fields either -- and the two
	// deserve the same answer anyway: both describe no change at all. Without
	// it, a manifest missing its whole spec resolved the table over the network
	// and printed "unchanged", which is a shipped doc asserting a rule nothing
	// enforced.
	if len(m.Spec.Fields) == 0 {
		return nil, qerrors.NewBadUsagef(
			"spec.fields is required in %s and must name at least one column: a manifest with no spec block, or with an empty spec.fields, describes no change at all. Start from `qctl get table-schema <table> -o yaml`.", source)
	}
	if err := rejectOriginalName(m.Spec.Fields, source); err != nil {
		return nil, err
	}
	if err := markResetDisplayDecimals(data, m.Spec.Fields, source); err != nil {
		return nil, err
	}
	return m, nil
}

// nullTag is yaml.v3's short tag for a null scalar. The package resolves it
// but does not export it.
const nullTag = "!!null"

// resetMarkers is the shape of a second, non-strict read of the same document:
// one raw node per column for the property whose explicit null the strict
// decode cannot carry (see FieldOverride.ResetDisplayDecimals).
//
// A yaml.Node destination is the one the decoder hands a null scalar unchanged,
// and it is reached before prepare() can discard it. Decoding the document
// again, rather than walking its node tree by hand, is what keeps an alias or
// a merge key resolved the way the strict decode resolved it.
type resetMarkers struct {
	Spec struct {
		Fields map[string]*struct {
			DisplayDecimals yaml.Node `yaml:"display_decimals"`
		} `yaml:"fields"`
	} `yaml:"spec"`
}

// markResetDisplayDecimals sets ResetDisplayDecimals on each field of fields
// whose block in data writes `display_decimals: null`.
func markResetDisplayDecimals(data []byte, fields map[string]*FieldOverride, source string) error {
	var markers resetMarkers
	if err := yaml.Unmarshal(data, &markers); err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", source))
	}
	for name, block := range markers.Spec.Fields {
		f := fields[name]
		// A nil block is `<column>: null`, which clears the whole override.
		if f == nil || block == nil {
			continue
		}
		// The emptiness test has to come first: ShortTag reports the null tag
		// for a ZERO node too — yaml.v3's convenience case for the zero value —
		// and a zero node here is an omitted display_decimals.
		if n := block.DisplayDecimals; !n.IsZero() && n.ShortTag() == nullTag {
			f.ResetDisplayDecimals = true
		}
	}
	return nil
}

// rejectOriginalName is why FieldOverride declares OriginalName at all: so a
// written original_name is refused with a reason instead of with strict
// parsing's anonymous "unknown field".
//
// Names are sorted so a manifest with two offending fields names the same one
// on every run, and a nil block is skipped rather than dereferenced --
// `field: null` is the documented way to clear an override and stays legal.
func rejectOriginalName(fields map[string]*FieldOverride, source string) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if f := fields[name]; f != nil && f.OriginalName != nil {
			return qerrors.NewBadUsagef(
				"spec.fields.%s.original_name is not writable in %s: it records what the column was called in the source data, "+
					"and this route cannot rename a column, so writing it would rewrite the provenance while renaming nothing. "+
					"Drop the key -- `qctl get table-schema -o yaml` never emits it.", name, source)
		}
	}
	return nil
}

// ManifestTableRef returns the identifier to resolve the table by, preferring
// metadata.id: it survives a rename, and Client.ResolveID takes an integer
// fast path before it ever lists by name.
func (m *TableSchemaManifest) ManifestTableRef() string {
	if m.Metadata.ID != "" {
		return m.Metadata.ID
	}
	return m.Metadata.Name
}
