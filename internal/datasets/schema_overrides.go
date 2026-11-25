package datasets

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
)

// SchemaPropChange is one property's transition, for the confirmation prompt
// and for `diff -f`.
type SchemaPropChange struct {
	Property string
	Old      string
	New      string
}

// SchemaFieldChange is one field's transition. Cleared marks `field: null` —
// reset to the inferred definition — which has no per-property diff. Added
// marks a column the schema does not have yet, whose Props are the properties
// the block states rather than transitions away from a live value.
type SchemaFieldChange struct {
	Field   string
	Cleared bool
	Added   bool
	Props   []SchemaPropChange
}

// BuildSchemaOverrides translates a TableSchema manifest against the live
// schema into the override set PATCH takes, plus the change list to render.
// apply and diff BOTH call it, so a document's preview cannot drift from the
// request apply would send for that document. Whole-FILE agreement is not this
// function's to give: it comes from `qctl diff -f` refusing a multi-document
// manifest (internal/commands/diff/diff.go), because diff previews one
// document where apply applies every one.
//
// Per key in spec.fields:
//
//	manifest    live      action
//	null        present   send null — clear the override, back to inferred
//	differs     present   send the COMPLETE override (see below)
//	identical   present   send nothing
//	stated      absent    send the block AS STATED — add the column
//	null        absent    exit 2: nothing to clear
//	null        deleted   exit 2: restore the column first
//	differs     deleted   exit 2, unless the only change is is_deleted: false
//
// A deleted column is one whose displayDefinition states is_deleted: true.
//
// A live field ABSENT from spec.fields is a no-op, not a drop. Deleting a field
// from the file does not drop the column; `is_deleted: true` does.
//
// INSIDE a block, one property takes a null of its own: `display_decimals:
// null` sends null, which is the backend's default places for the column's
// flags, where an omitted line keeps the live value. Every other property's
// null is an omitted line, because unset is not a state the backend gives them
// a meaning for.
//
// WHY THE COMPLETE OVERRIDE AND NOT A DELTA. Two merges run server-side and
// they are different. merge_schema_overrides
// (atlas cettings/cettings/controllers/migration_model_controller.py)
// folds the request into SettingsModel.schema_overrides one FIELD ENTRY at a
// time: stored[k] = v swaps a field's whole override, and a null value pops
// the key rather than storing it. schema_overrides_merge then rebuilds
// the migration model as model[f] = {**model[f], **override} over the
// INFERRED model. So an override that omits a property does not
// inherit it from a previous override — it inherits it from inference. Sending
// a minimal {type, args} for a field that already carried {type, is_nullable}
// silently discards the nullability override. Overlaying live's effective
// definition is what makes editing one line of the emitted YAML safe, and
// `field: null` stays the one explicit way to go back to inferred.
//
// A future "optimisation" to a minimal delta is a silent data-losing
// regression. TestBuildSchemaOverrides_OverlayKeepsUnstatedLiveProperties
// exists to stop it landing.
func BuildSchemaOverrides(detail *TableSchemaDetail, spec TableSchemaSpec, source string) (map[string]any, []SchemaFieldChange, error) {
	overrides := map[string]any{}
	var changes []SchemaFieldChange

	names := make([]string, 0, len(spec.Fields))
	for name := range spec.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		f := spec.Fields[name]
		info, present := detail.Fields[name]
		if !present {
			override, props, err := addedFieldOverride(name, f, detail, source)
			if err != nil {
				return nil, nil, err
			}
			overrides[name] = override
			changes = append(changes, SchemaFieldChange{Field: name, Added: true, Props: props})
			continue
		}
		if f == nil {
			if isDeletedDefinition(displayDefinition(detail.MigrationState, info)) {
				return nil, nil, deletedColumnEdit(name, source, nil)
			}
			overrides[name] = nil
			changes = append(changes, SchemaFieldChange{Field: name, Cleared: true})
			continue
		}
		live := displayDefinition(detail.MigrationState, info)
		if live == nil {
			return nil, nil, qerrors.NewBadUsagef(
				"column %q of table %q has no definition to override (%s)", name, detail.DatasetName, source)
		}
		override, props, err := overlayFieldOverride(name, f, live, source)
		if err != nil {
			return nil, nil, err
		}
		if len(props) == 0 {
			continue
		}
		overrides[name] = override
		changes = append(changes, SchemaFieldChange{Field: name, Props: props})
	}
	return overrides, changes, nil
}

// addedFieldOverride builds the override for a column the schema does not have.
// Declaratively, as `kubectl apply` would: the key is in spec.fields and the
// live schema has nothing by that name, so the manifest is asking for the
// column to exist.
//
// The route is built for this. schema_overrides_merge's `else` arm writes an
// unfamiliar field straight into the rebuilt model as
// `model[field_name] = type_override` (atlas
// cettings/cettings/controllers/migration_model_controller.py), and
// apply_schema_overrides_to_migration_model does the same in place on a
// MigrationModel's own model, under a comment reading "handles new fields and
// modifications". cmapper skips quarantine for one with
// `continue  # New column, no quarantine needed` (cmapper/cmapper/migrator.py),
// and cmigrator's _add_column carries a written migration policy for one
// (ALTER TABLE ADD COLUMN, nullable). Rule-produced columns already ride the
// same model with no source data behind them.
//
// The server refuses a new name that is malformed, reserved or collides with an
// existing column, but not one that is merely close to an existing name, so a
// well-formed misspelling is added. `diff -f` and the confirmation prompt render
// an addition as `add column` with its properties beneath, the way kubectl
// shows a create.
//
// Three shapes are refused rather than sent:
//
//   - `field: null` — there is no override to clear, and
//     validate_new_field_overrides (atlas
//     cettings/cettings/controllers/migration_model_controller.py) answers 422.
//   - is_deleted — there is nothing to delete. validate_new_field_overrides
//     answers 422 to true, and false adds no column, because
//     schema_overrides_merge sets is_deleted only on a column the migration
//     already has.
//   - is_nullable: false — a new column is always added nullable, and
//     validate_new_field_overrides answers 422.
//
// The override itself is exactly the block as stated, and overlayFieldOverride
// builds it: handed the ZERO definition below in place of a live one, its base
// comes out empty and every stated property "differs", so it both records the
// property as a change and writes it to the override. "The properties that
// differ from live" and "the properties the block states" are the same list
// when there is no live definition, which is why one function serves both paths
// and there is no second property table here to drift out of step with it.
//
// One property parts them: a stated `display_decimals: null` asks for the
// default places a new column has anyway, so it is written to the override and
// reported as no change. The preview states what the column will be, not what
// it would have been without the line.
func addedFieldOverride(name string, f *FieldOverride, detail *TableSchemaDetail, source string) (map[string]any, []SchemaPropChange, error) {
	if f == nil {
		return nil, nil, qerrors.NewBadUsagef(
			"cannot clear column %q of table %q (%s): `null` resets a column to its inferred definition, and this schema has no column by that name.\n"+
				"To ADD it, state its definition in place of the null.\n"+
				"Run `qctl get table-schema %s -o yaml` for the columns the schema has.",
			name, detail.DatasetName, source, detail.DatasetName)
	}
	if f.IsDeleted != nil {
		return nil, nil, qerrors.NewBadUsagef(
			"spec.fields.%s sets is_deleted on a column the schema does not have (%s): this block ADDS %q, and there is nothing to delete.\n"+
				"Remove the `is_deleted` line to add the column, or remove the whole block to leave the schema alone.",
			name, source, name)
	}
	if f.IsNullable != nil && !*f.IsNullable {
		return nil, nil, qerrors.NewBadUsagef(
			"spec.fields.%s.is_nullable is false on a column the schema does not have (%s): a new column is always added nullable, and the server refuses `is_nullable: false` on one.\n"+
				"Remove the `is_nullable: false` line, or set it to true, to add %q.",
			name, source, name)
	}
	// The zero definition, not nil: every property reads as unset, which is
	// exactly what "the schema has nothing by this name" means. Constructing
	// it here rather than letting overlayFieldOverride branch on nil keeps
	// that function's contract -- and its complexity -- where it was.
	return overlayFieldOverride(name, f, &FieldSchemaDetail{}, source)
}

// overlayFieldOverride builds one field's complete override and the list of
// properties the manifest states that actually differ from live.
//
// live must not be nil. A column the schema does not have is overlaid on the
// ZERO definition instead (addedFieldOverride), which is what makes this one
// function serve the addition path too.
func overlayFieldOverride(name string, f *FieldOverride, live *FieldSchemaDetail, source string) (map[string]any, []SchemaPropChange, error) {
	// ParseTableSchemaManifest already refuses a written original_name, so a
	// manifest read from disk never reaches this. It stays because
	// BuildSchemaOverrides is a library entry point apply and diff both call,
	// and the spec it is handed need not have come from the parser.
	if f.OriginalName != nil {
		return nil, nil, qerrors.NewBadUsagef(
			"spec.fields.%s.original_name is read-only (%s): it records what the column was called in the source data, not what you want it to be, and the schema route cannot rename a column", name, source)
	}
	if f.Type == "" {
		return nil, nil, qerrors.NewBadUsagef(
			"spec.fields.%s.type is required (%s); valid types: %s", name, source, strings.Join(api.FieldTypes, ", "))
	}
	// An unknown type is refused UNLESS it is exactly what live already holds.
	// FieldDatabaseDetailDict.type is `FieldType | str` (atlas
	// common/common/types.py), so a model can carry a type outside the
	// generated FieldTypes enum -- and `get table-schema -o yaml` emits it
	// verbatim. Refusing it unconditionally broke the headline workflow on an
	// UNEDITED file: read, apply, exit 2, with nothing the user could fix
	// short of deleting a column block they never touched.
	//
	// The validation loses nothing. A typo does not equal the live value, so
	// it is still caught; only the value that would change nothing is let
	// through, and a field whose stated type matches live contributes no
	// property and so no override at all.
	//
	// On an ADDED column the exception cannot fire -- the zero live definition
	// has no type name and f.Type is required non-empty -- and must not: there
	// is no established value for an unknown type to be, so it is a typo.
	if !isKnownFieldType(f.Type) && f.Type != SchemaTypeName(live) {
		return nil, nil, qerrors.NewBadUsagef(
			"spec.fields.%s.type %q is not a column type (%s); valid types: %s", name, f.Type, source, strings.Join(api.FieldTypes, ", "))
	}

	override := liveOverrideBase(live, f.Type)

	var props []SchemaPropChange
	add := func(property, old, updated string) {
		props = append(props, SchemaPropChange{Property: property, Old: old, New: updated})
	}

	// type is always stated (it is required), so it is always overlaid.
	if liveType := SchemaTypeName(live); f.Type != liveType {
		add("type", liveType, f.Type)
	}
	override["type"] = f.Type

	if f.Args != nil {
		if !equalIntSlices(f.Args, live.Args) {
			add("args", formatIntSlice(live.Args), formatIntSlice(f.Args))
		}
		override["args"] = copyInts(*f.Args)
	}
	props = append(props, overlayDisplayDecimals(override, f, live.DisplayDecimals)...)
	for _, p := range []struct {
		name  string
		want  *bool
		liveV *bool
	}{
		{"is_nullable", f.IsNullable, live.IsNullable},
		{"indexed", f.Indexed, live.Indexed},
		{"trgm_indexed", f.TrgmIndexed, effectiveTrgmIndexed(live)},
		{"is_dollar", f.IsDollar, live.IsDollar},
		{"is_percent", f.IsPercent, live.IsPercent},
		{"is_deleted", f.IsDeleted, live.IsDeleted},
	} {
		if p.want == nil {
			continue
		}
		if !equalBoolPtr(p.want, p.liveV) {
			add(p.name, formatBoolPtr(p.liveV), strconv.FormatBool(*p.want))
		}
		override[p.name] = *p.want
	}
	if f.DatetimeFormats != nil {
		if !equalStringSlices(f.DatetimeFormats, live.DatetimeFormats) {
			add("datetime_formats", formatStringSlice(live.DatetimeFormats), formatStringSlice(f.DatetimeFormats))
		}
		override["datetime_formats"] = copyStrings(*f.DatetimeFormats)
	}

	if err := refuseUnsendableOverride(name, source, f, override, props, isDeletedDefinition(live)); err != nil {
		return nil, nil, err
	}
	return override, props, nil
}

// refuseUnsendableOverride refuses a field override the backend would refuse,
// or accept and then not honour. It runs last in overlayFieldOverride because its
// checks need the complete prop list and the complete override. liveDeleted
// reports whether the live definition is deleted.
func refuseUnsendableOverride(name, source string, f *FieldOverride, override map[string]any, props []SchemaPropChange, liveDeleted bool) error {
	// is_deleted discards the rest of its own override server-side, so a block
	// that states it AND edits something else would have the edit accepted here
	// and thrown away there. Refuse rather than answer a different question.
	if others := propNamesExcept(props, "is_deleted"); f.IsDeleted != nil && len(others) > 0 {
		return isDeletedBesideAChange(name, source, others, liveDeleted)
	}

	// refuse_deleted_column_edits (atlas
	// cettings/cettings/controllers/schema_format_override_processor.py)
	// answers 409 to an override on a deleted column unless it carries a
	// boolean is_deleted, and writes nothing from the request. It reads each
	// column from DataSet.model_schema, overlaid by the current migration's
	// model and then the draft's; liveDeleted reads the last two the same way,
	// through displayDefinition. An unedited block sends nothing, so it is not
	// refused.
	if liveDeleted && f.IsDeleted == nil && len(props) > 0 {
		return deletedColumnEdit(name, source, propNamesExcept(props, "is_deleted"))
	}

	// is_dollar and is_percent together is a pair the backend cannot honour:
	// DisplayFormat.from_flags (atlas common/common/search_terms.py) shows the
	// column as currency, while the cleaner divides loaded values by 100 on
	// is_percent alone (cmapper/cmapper/cleaner.py,
	// Cleaner._get_field_value_cleaned's percent_to_decimal row-log step, which
	// the percent_to_decimal setting does not gate). Since atlas
	// #953 (atlas-a8fv.21) schema_overrides_merge refuses it with a 422 too --
	// over the columns the request names, and not over a column it deletes,
	// since #980 (atlas-a8fv.24); refusing here as well answers before the
	// apply prompt and in diff -f, which sends nothing. The web UI makes the two checkboxes mutually
	// exclusive and saves a column carrying both as dollar-only; a manifest is
	// refused instead of rewritten, so what is sent is what the file says.
	//
	// The check reads the override, not the block, because the base carries
	// live's flags, and a column the mapper inferred before #953 can carry
	// both. It fires only when something is sent -- an unedited block on such
	// a column is not a request -- and not beside is_deleted, which #980
	// exempts server-side as well.
	if f.IsDeleted == nil && len(props) > 0 && overrideFlag(override, "is_dollar") && overrideFlag(override, "is_percent") {
		return qerrors.NewBadUsagef(
			"spec.fields.%s (%s) would be sent with is_dollar and is_percent both true, which the backend refuses: it would display such a column as currency while its cleaner divides loaded values by 100 as a percent.\n"+
				"Set one of them to false in this block. A block that leaves a flag out inherits it from the live column, which may already carry both.",
			name, source)
	}

	// Same gate as the pair above, for the same reasons: an unedited block is
	// not a request, and is_deleted keeps the inferred field's flags rather
	// than deriving them from the block's type.
	if f.IsDeleted == nil && len(props) > 0 {
		return refuseOverwrittenIndexFlag(name, source, f)
	}
	return nil
}

// isDeletedBesideAChange is the refusal for a block that states is_deleted
// alongside the changes named by others. It names only those, never
// is_deleted itself, and its ways forward depend on liveDeleted.
func isDeletedBesideAChange(name, source string, others []string, liveDeleted bool) error {
	changed := strings.Join(others, ", ")
	head := fmt.Sprintf("spec.fields.%s (%s) sets is_deleted alongside another change (%s): "+
		"the backend applies is_deleted on its own and discards every other property in the same block, "+
		"and it forces is_nullable: true whichever value is_deleted takes.\n", name, source, changed)
	if !liveDeleted {
		return qerrors.NewBadUsagef("%s"+
			"To change the definition: remove the `is_deleted` line from this block. The column is not deleted, so the block then changes only %s.\n"+
			"To delete the column: state `is_deleted: true` and revert %s in this block to what `qctl get table-schema -o yaml` emitted.",
			head, changed, changed)
	}
	// The usual way here on a deleted column is an edit of the emitted file,
	// which states `is_deleted: true` (fieldOverrideFromDetail). Deleting that
	// line is not the way forward: deletedColumnEdit refuses the result.
	return qerrors.NewBadUsagef("%s"+
		"This column is deleted, and `qctl get table-schema -o yaml` emits `is_deleted: true` for a deleted column, so this block may state it without your having typed it.\n"+
		"To un-delete the column and change it, apply twice: first make `is_deleted: false` this block's only change, reverting %s to what `qctl get table-schema -o yaml` emitted; then make the change in a second apply. "+
		"Removing the `is_deleted` line instead is refused too: %s.\n"+
		"To leave the column deleted: state `is_deleted: true` and revert %s in this block to what `qctl get table-schema -o yaml` emitted.",
		head, changed, deletedColumnRefused, changed)
}

// deletedColumnRefused is the reason a refusal on a deleted column gives.
const deletedColumnRefused = "the backend refuses any edit or null reset of a deleted column, " +
	"and refuses this table's whole schema change with it, so nothing is written, the other columns' changes included"

// deletedColumnEdit is the refusal for a block on a deleted column that does
// not state is_deleted. changed names the properties the block changes; nil
// means the block is a `null` reset. The column name is sanitized for the
// terminal.
func deletedColumnEdit(name, source string, changed []string) error {
	safe := terminal.Sanitize(name)
	if changed == nil {
		return qerrors.NewBadUsagef(
			"spec.fields.%s (%s) is null on a deleted column: %s.\n"+
				"To reset the column, apply twice: first replace the null with the block `qctl get table-schema -o yaml` emitted for it and make `is_deleted: false` that block's only change, which restores the column; then put the null back in a second apply.\n"+
				"To leave the column deleted: replace the null with the block `qctl get table-schema -o yaml` emitted for it, `is_deleted: true` included.",
			safe, source, deletedColumnRefused)
	}
	list := strings.Join(changed, ", ")
	return qerrors.NewBadUsagef(
		"spec.fields.%s (%s) changes %s on a deleted column: %s.\n"+
			"To change the column, apply twice: first make `is_deleted: false` this block's only change, reverting %s to what `qctl get table-schema -o yaml` emitted, which restores the column; then make the change in a second apply.\n"+
			"To leave the column deleted: revert this block to what `qctl get table-schema -o yaml` emitted, `is_deleted: true` included.",
		safe, source, list, deletedColumnRefused, list)
}

// isDeletedDefinition reports whether def states is_deleted: true.
func isDeletedDefinition(def *FieldSchemaDetail) bool {
	return def != nil && def.IsDeleted != nil && *def.IsDeleted
}

func overrideFlag(override map[string]any, key string) bool {
	v, _ := override[key].(bool)
	return v
}

// overlayDisplayDecimals overlays the one property a block may state as an
// explicit null.
//
// A reset writes null OVER the base's live value rather than deleting the key.
// Deleting it would leave the merge nothing to apply, and the merge runs over
// the INFERRED model (see BuildSchemaOverrides), so the column would keep
// whatever inference gave it instead of going back to the default. The null
// itself is what the backend reads as "the default for this column's flags":
// _reject_invalid_display_decimals returns on None (atlas
// cettings/cettings/controllers/migration_model_controller.py) and
// schema_overrides_merge's `{**model[f], **override}` then carries it into the
// rebuilt model.
//
// A reset of a column that is already unset states what is already true, so it
// is no change at all — the same answer any other property gets for stating
// live's value.
func overlayDisplayDecimals(override map[string]any, f *FieldOverride, live *int) []SchemaPropChange {
	if f.ResetDisplayDecimals {
		override["display_decimals"] = nil
		if live == nil {
			return nil
		}
		return []SchemaPropChange{{
			Property: "display_decimals",
			Old:      displayDecimalsText(live),
			New:      displayDecimalsText(nil),
		}}
	}
	if f.DisplayDecimals == nil {
		return nil
	}
	override["display_decimals"] = *f.DisplayDecimals
	if equalIntPtr(f.DisplayDecimals, live) {
		return nil
	}
	return []SchemaPropChange{{
		Property: "display_decimals",
		Old:      displayDecimalsText(live),
		New:      displayDecimalsText(f.DisplayDecimals),
	}}
}

// displayDecimalsText renders one side of a display_decimals change. Unset is
// spelled out rather than left as the grid's "-", because a manifest can ask
// for it: "0 → -" reads like a value the renderer did not have, where
// "0 → unset" reads like the change it is.
func displayDecimalsText(n *int) string {
	if n == nil {
		return "unset"
	}
	return strconv.Itoa(*n)
}

// liveOverrideBase seeds a field's override with live's effective definition,
// which is what makes the complete override safe to send (see
// BuildSchemaOverrides). It omits a forced indexed flag when targetType
// changes: that flag belongs to the old type, and leaving it out lets the
// backend discard its index method before deriving the new type's flags.
//
// Two writable-looking properties are deliberately NOT copied.
//
// original_name, because it is inference provenance: not sending it lets
// the inferred model's own value survive schema_overrides_merge's
// {**model[f], **override}.
//
// is_deleted, because it is a MODE SWITCH rather than a property.
// schema_overrides_merge branches on `type_override.get("is_deleted") is
// None` (migration_model_controller.py); when it is present — false
// as well as true — the merge writes ONLY is_deleted and forces
// is_nullable=True, discarding type, args and everything else in the same
// override. Copying a live is_deleted into the base would therefore turn
// every edit of a previously-deleted column into a silent no-op.
func liveOverrideBase(live *FieldSchemaDetail, targetType string) map[string]any {
	base := map[string]any{}
	if live.Args != nil {
		base["args"] = copyInts(*live.Args)
	}
	putBool(base, "is_nullable", live.IsNullable)
	liveType := SchemaTypeName(live)
	if liveType == targetType || !typeIndexFlags[liveType].forcedIndex {
		putBool(base, "indexed", live.Indexed)
	}
	putBool(base, "trgm_indexed", live.TrgmIndexed)
	putBool(base, "is_dollar", live.IsDollar)
	putBool(base, "is_percent", live.IsPercent)
	if live.DisplayDecimals != nil {
		base["display_decimals"] = *live.DisplayDecimals
	}
	if live.DatetimeFormats != nil {
		base["datetime_formats"] = copyStrings(*live.DatetimeFormats)
	}
	return base
}

func equalIntPtr(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func formatIntPtr(n *int) string {
	if n == nil {
		return "-"
	}
	return strconv.Itoa(*n)
}

// SchemaPatchBody assembles the PATCH body from the override set and the live
// version tokens.
//
// settings_version_id is ALWAYS sent, and that is now an opt-in rather than a
// requirement. patch_migration compares the token only when one is sent (atlas
// capi/capi/views/migration_view.py), so omitting it would let the
// write land over a settings edit made since the read. qctl read the schema a
// moment before building this body, so it sends what it read and takes the 409
// against the version the caller saw. Omitting it would not buy a lost update
// anyway: SettingsModel.version_id is a SQLAlchemy version column, so the
// database refuses one either way, and capi maps StaleDataError to a 409
// ERR_DB_CONCURRENCY_CONFLICT (capi/capi/middleware.py).
//
// draft_version_id is sent exactly when a draft exists. patch_migration
// (migration_view.py) answers 400 when a draft exists and none was
// sent, and 404 when one was sent and no draft exists — so "always send it"
// and "never send it" are both wrong.
//
// The body is a hand-built map rather than api.MigrationPatchRequest for two
// independent reasons, both still true of the type the 2026-09-10 spec put on
// this route. api.FieldOverrideDict (client.gen.go) has omitempty on
// NOTHING, so marshaling the struct sends "is_nullable":null,"args":null,… —
// merge_schema_overrides stores that field entry verbatim and
// schema_overrides_merge then splats it over the inferred definition
// (migration_model_controller.py), wiping nullability, args and indexing on
// a column the caller only meant to retype. And the request's schema_overrides
// is *map[string]FieldOverrideDict (client.gen.go) whose VALUE is a
// non-pointer, so the `null` reset — which the route now documents rather than
// merely tolerating — cannot be expressed through it at all. client.gen.go is
// not touched.
func SchemaPatchBody(detail *TableSchemaDetail, overrides map[string]any) map[string]any {
	body := map[string]any{
		"settings_version_id": detail.SettingsModelVersionID,
		"schema_overrides":    overrides,
	}
	if detail.MigrationState != api.CurrentOnly {
		body["draft_version_id"] = detail.MigrationModelVersionID
	}
	return body
}

// FormatSchemaFieldChanges renders the change list for the confirmation prompt
// and for `diff -f`. Both go through this one function so the preview and the
// prompt read identically.
func FormatSchemaFieldChanges(changes []SchemaFieldChange, indent string) string {
	var b strings.Builder
	for _, c := range changes {
		if c.Cleared {
			fmt.Fprintf(&b, "%s%s: clear the override (back to the inferred definition)\n", indent, c.Field)
			continue
		}
		// An addition has no old value to point an arrow away from, so it
		// renders as a create does: the verb, then the definition being asked
		// for. This IS the typo protection for a column name -- a mistyped name
		// is a column the schema does not have, and it now says so in the one
		// place a caller looks before applying.
		if c.Added {
			fmt.Fprintf(&b, "%s%s: add column\n", indent, c.Field)
			for _, p := range c.Props {
				fmt.Fprintf(&b, "%s  %s: %s\n", indent, p.Property, p.New)
			}
			continue
		}
		fmt.Fprintf(&b, "%s%s:\n", indent, c.Field)
		for _, p := range c.Props {
			fmt.Fprintf(&b, "%s  %s: %s → %s\n", indent, p.Property, p.Old, p.New)
		}
	}
	// Both callers print this straight to a terminal, and the column names are
	// the server's. Sanitized once at the return rather than per field, the way
	// the other report builders do it.
	return terminal.Sanitize(b.String())
}

// copyInts and copyStrings copy a slice property into the override body.
//
// They exist instead of `append([]int(nil), v...)` because that idiom returns a
// NIL slice for an empty source, and a nil slice marshals to JSON `null`, not
// `[]`. merge_schema_overrides stores that null straight into the field's
// override entry, so a live `"args": []` -- legal on the wire,
// common/common/types.py types args as
// `tuple[int, ...] | list[int] | None` -- would come back as a wiped
// args rather than an empty one. An empty non-nil slice marshals to `[]`.
func copyInts(v []int) []int {
	out := make([]int, len(v))
	copy(out, v)
	return out
}

func copyStrings(v []string) []string {
	out := make([]string, len(v))
	copy(out, v)
	return out
}

func isKnownFieldType(name string) bool {
	for _, t := range api.FieldTypes {
		if t == name {
			return true
		}
	}
	return false
}

func putBool(m map[string]any, key string, v *bool) {
	if v != nil {
		m[key] = *v
	}
}

// propNamesExcept lists the changed properties other than skip, which is both
// the guard's condition and the name it reports: a block whose only change is
// is_deleted is legal, and one that changes more must not be told that
// is_deleted collides with itself.
func propNamesExcept(props []SchemaPropChange, skip string) []string {
	out := make([]string, 0, len(props))
	for _, p := range props {
		if p.Property != skip {
			out = append(out, p.Property)
		}
	}
	return out
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalIntSlices(a, b *[]int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(*a) != len(*b) {
		return false
	}
	for i := range *a {
		if (*a)[i] != (*b)[i] {
			return false
		}
	}
	return true
}

func equalStringSlices(a, b *[]string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(*a) != len(*b) {
		return false
	}
	for i := range *a {
		if (*a)[i] != (*b)[i] {
			return false
		}
	}
	return true
}

func formatIntSlice(v *[]int) string {
	if v == nil {
		return "-"
	}
	parts := make([]string, len(*v))
	for i, n := range *v {
		parts[i] = strconv.Itoa(n)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatStringSlice(v *[]string) string {
	if v == nil {
		return "-"
	}
	return "[" + strings.Join(*v, ",") + "]"
}

// ProducedColumnEdit is one edit to a column whose definition carries the
// server's rule_output stamp. RuleID is the stamp's dataset_rule_id, and Props
// names the edited properties among type, args and is_deleted.
type ProducedColumnEdit struct {
	Field  string
	RuleID string
	Props  []string
}

// ProducedColumnEdits names the fields in changes whose definition — the one
// the overlay edits — is stamped rule_output and whose change moves type or
// args, or sets is_deleted to true. Order follows changes.
func ProducedColumnEdits(detail *TableSchemaDetail, changes []SchemaFieldChange) []ProducedColumnEdit {
	var edits []ProducedColumnEdit
	for _, c := range changes {
		info, ok := detail.Fields[c.Field]
		if !ok {
			continue
		}
		owner := ruleOutputOwner(displayDefinition(detail.MigrationState, info))
		if owner == "" {
			continue
		}
		var props []string
		for _, p := range c.Props {
			if p.Property == "type" || p.Property == "args" || setsDeleted(p) { //nolint:goconst // Property names are wire schema keys.
				props = append(props, p.Property)
			}
		}
		if len(props) > 0 {
			edits = append(edits, ProducedColumnEdit{Field: c.Field, RuleID: owner, Props: props})
		}
	}
	return edits
}

// FormatProducedColumnWarning renders edits on table datasetID, or "" when
// there are none. ruleNames maps a rule ID to its instance name; a rule it does
// not name is printed by ID alone. It ends with a blank line.
func FormatProducedColumnWarning(edits []ProducedColumnEdit, ruleNames map[string]string, datasetID int) string {
	if len(edits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Warning: this edit changes column(s) a rule produces, and the schema route does not check that:\n")
	for _, e := range edits {
		rule := e.RuleID
		if name := ruleNames[e.RuleID]; name != "" {
			rule = name + " (" + e.RuleID + ")"
		}
		fmt.Fprintf(&b, "  %s (%s): produced by rule %s\n", e.Field, strings.Join(e.Props, ", "), rule)
	}
	b.WriteString("  A produced column left deleted, or retyped to a type the rule's declaration does not\n")
	b.WriteString("  accept, fails the rule's enable check with a 409: enabling the rule is refused, applying\n")
	b.WriteString("  this migration moves a pending_validation rule to validation_failed, and an enabled rule\n")
	b.WriteString("  has changes to its produced fields or column mapping refused.\n")
	b.WriteString("  The check does not read args, so a resize passes it.\n")
	fmt.Fprintf(&b, "  `qctl describe table-rule <rule-id> --table-id %d` shows what a rule declares.\n\n", datasetID)
	// Column and rule names are the server's, printed straight to a terminal.
	return terminal.Sanitize(b.String())
}

// setsDeleted reports whether p sets is_deleted to true.
func setsDeleted(p SchemaPropChange) bool {
	return p.Property == "is_deleted" && p.New == "true" //nolint:goconst // Property values are wire schema strings.
}

// DeletedColumns names the fields in changes whose change sets is_deleted to
// true. Order follows changes.
func DeletedColumns(changes []SchemaFieldChange) []string {
	var deleted []string
	for _, c := range changes {
		for _, p := range c.Props {
			if setsDeleted(p) {
				deleted = append(deleted, c.Field)
				break
			}
		}
	}
	return deleted
}

// DraftDeletedColumns names columns marked deleted or omitted from the draft.
func DraftDeletedColumns(detail *TableSchemaDetail) []string {
	if detail == nil || detail.State != api.MigrationStateDraft ||
		(detail.MigrationState != api.DraftOnly && detail.MigrationState != api.DraftWithCurrent) {
		return nil
	}
	var deleted []string
	for name, field := range detail.Fields {
		if field.New != nil {
			if field.New.IsDeleted != nil && *field.New.IsDeleted {
				deleted = append(deleted, name)
			}
		} else if field.Current != nil {
			deleted = append(deleted, name)
		}
	}
	sort.Strings(deleted)
	return deleted
}

// FilterColumnRule is one table rule whose row filter reads deleted columns.
// Builtin is nil when unknown. Columns names the deleted columns the filter
// reads, in the filter's order.
type FilterColumnRule struct {
	RuleID       string
	InstanceName string
	State        api.DatasetRuleState
	Builtin      *bool
	Columns      []string
}

// FilterColumnReaders names the rules whose row filter reads a column in
// deleted, skipping rules in state deleted. Order follows rules; Builtin is
// left nil.
func FilterColumnReaders(rules []api.DatasetRuleTinySchema, deleted []string) []FilterColumnRule {
	var readers []FilterColumnRule
	for _, r := range rules {
		if r.State == api.DatasetRuleStateDeleted {
			continue
		}
		var columns []string
		for _, column := range row_filters.FromAPI(r.RowFilter).Columns() {
			if slices.Contains(deleted, column) {
				columns = append(columns, column)
			}
		}
		if len(columns) > 0 {
			readers = append(readers, FilterColumnRule{
				RuleID: r.ID.String(), InstanceName: r.InstanceName, State: r.State, Columns: columns,
			})
		}
	}
	return readers
}

// FormatRowFilterColumnWarning renders rules on table datasetID, or "" when
// there are none. A rule with no instance name is printed by ID alone. It ends
// with a blank line.
func FormatRowFilterColumnWarning(rules []FilterColumnRule, datasetID int) string {
	if len(rules) == 0 {
		return ""
	}
	const (
		builtinFate = "    The stale-column retirement pass retires (deletes) this enabled builtin rule.\n" +
			"    No patch restores a deleted rule (409).\n"
		customFate = "    The stale-column retirement pass leaves custom rules alone. If this rule stays enabled\n" +
			"    with this filter, every row it meets gets a validation_problem blocker.\n"
		unknownFate = "    Whether the rule is builtin could not be read. The stale-column retirement pass deletes\n" +
			"    enabled builtin rules; no patch restores a deleted rule (409). It leaves custom rules\n" +
			"    alone; if this rule stays enabled with this filter, every row it meets gets a\n" +
			"    validation_problem blocker.\n"
		notEnabledFate = "    The rule is not enabled, so the stale-column retirement pass leaves it alone.\n" +
			"    After the migration, an enable that leaves row_filter out is refused (422); fix or\n" +
			"    clear the filter first.\n"
	)
	var b strings.Builder
	b.WriteString("Warning: this migration deletes column(s) a row filter reads, and the schema route does not check that:\n")
	for _, r := range rules {
		rule := r.RuleID
		if r.InstanceName != "" {
			rule = r.InstanceName + " (" + r.RuleID + ")"
		}
		kind, fate := "", notEnabledFate
		if r.State == api.DatasetRuleStateEnabled {
			switch {
			case r.Builtin == nil:
				fate = unknownFate
			case *r.Builtin:
				kind, fate = " builtin", builtinFate
			default:
				kind, fate = " custom", customFate
			}
		}
		fmt.Fprintf(&b, "  %s: %s%s rule; its row filter reads %s\n", rule, r.State, kind, strings.Join(r.Columns, ", "))
		b.WriteString(fate)
	}
	b.WriteString("  This preview reads live rules. Staged suggestions may change their filters or states;\n" +
		"  produced-field activation moves a pending_validation rule whose filter reads a deleted\n" +
		"  column to validation_failed instead of enabling it.\n")
	fmt.Fprintf(&b, "  `qctl describe table-rule <rule-id> --table-id %d` shows a rule's row filter.\n\n", datasetID)
	// Column and rule names are the server's, printed straight to a terminal.
	return terminal.Sanitize(b.String())
}
