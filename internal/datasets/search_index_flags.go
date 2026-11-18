package datasets

import qerrors "github.com/qlustered/qctl/internal/errors"

// typeIndexFlags is what the backend does to a column's index flags, by type,
// whatever the override says: a trigram type always gets trgm_indexed and any
// other type never keeps it, and a forced-index type always gets indexed. It
// mirrors set_search_index_flags (atlas common/common/types.py), which
// schema_overrides_merge runs on every override that does not state
// is_deleted.
//
// Every api.FieldTypes value has an entry, the ones that get neither flag
// included, so TestTypeIndexFlagsClassifyEveryFieldType fails when a regen adds
// a type nobody has classified. A type missing from the map gets neither.
var typeIndexFlags = map[string]struct{ trigram, forcedIndex bool }{
	"String":        {trigram: true},
	"Text":          {trigram: true},
	"Array":         {trigram: true},
	"Json":          {trigram: true},
	"Jsonb":         {trigram: true},
	"SmallInteger":  {trigram: true},
	"Integer":       {trigram: true},
	"BigInteger":    {trigram: true},
	"Decimal":       {trigram: true},
	"Float":         {trigram: true},
	"Date":          {forcedIndex: true},
	"DateTime":      {forcedIndex: true},
	"UUID":          {forcedIndex: true},
	"SmallIntArray": {forcedIndex: true},
	"IntArray":      {forcedIndex: true},
	"BigIntArray":   {forcedIndex: true},
	"UUIDArray":     {forcedIndex: true},
	"Boolean":       {},
	"Bytea":         {},
	"Varbit":        {},
}

func effectiveTrgmIndexed(d *FieldSchemaDetail) *bool {
	if d.TrgmIndexed != nil || !typeIndexFlags[SchemaTypeName(d)].trigram {
		return d.TrgmIndexed
	}
	derived := true
	return &derived
}

// refuseOverwrittenIndexFlag refuses a stated trgm_indexed, or a stated
// indexed: false, that disagrees with what typeIndexFlags gives the block's
// type. The backend would store its own value instead, so the file and the
// table would differ after every apply.
func refuseOverwrittenIndexFlag(name, source string, f *FieldOverride) error {
	flags := typeIndexFlags[f.Type]
	if f.TrgmIndexed != nil && flags.trigram && !*f.TrgmIndexed {
		return qerrors.NewBadUsagef(
			"spec.fields.%s.trgm_indexed is false (%s), but the backend gives every %s column a trigram index and overwrites the flag, so the line would show as a change on every diff and apply.\n"+
				"Remove the `trgm_indexed` line, or set it to true.",
			name, source, f.Type)
	}
	if f.TrgmIndexed != nil && !flags.trigram {
		return qerrors.NewBadUsagef(
			"spec.fields.%s.trgm_indexed is stated (%s), but type %s gets no trigram index: the backend sets the flag from the type and keeps none for this one, so the line would show as a change on every diff and apply.\n"+
				"If you retyped this block from a trigram-indexed type, remove the stale `trgm_indexed` line.",
			name, source, f.Type)
	}
	if f.Indexed != nil && flags.forcedIndex && !*f.Indexed {
		return qerrors.NewBadUsagef(
			"spec.fields.%s.indexed is false (%s), but the backend indexes every %s column and overwrites the flag, so the line would show as a change on every diff and apply.\n"+
				"Remove the `indexed` line, or set it to true.",
			name, source, f.Type)
	}
	return nil
}
