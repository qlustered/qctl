// Package paramflags derives one cobra flag per field of a generated API params
// struct, and reads those flags back into such a struct. `get data` and
// `download data` drive the same query-parameter surface over two different
// generated types, so the reflection lives here rather than in either command.
package paramflags

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// Kind classifies how a params-struct field maps to a cobra flag.
type Kind int

const (
	KindBool Kind = iota
	KindInt
	KindString
	KindStringSlice
	KindIntSlice
	KindUUIDSlice
	KindUUID
	KindUnsupported
)

// Flag describes one derived flag.
type Flag struct {
	FieldName string // Go struct field, e.g. "ShowMetadata"
	WireName  string // form tag, e.g. "show_metadata"
	FlagName  string // CLI flag, e.g. "show-metadata"
	Kind      Kind
	// Type is what the params field points AT -- bool for *bool, and the
	// generated named string type for a *QuarantineHold. Kind flattens every
	// named string type into KindString, so it is Type, not Kind, that says
	// which flags carry a closed value set (see enumValuesForType).
	Type reflect.Type
}

var uuidType = reflect.TypeOf(openapi_types.UUID{})

// FlagNameForWire converts a wire name to a flag name: '_' -> '-', trailing '-' trimmed.
func FlagNameForWire(wireName string) string {
	return strings.TrimRight(strings.ReplaceAll(wireName, "_", "-"), "-")
}

// WireNameFromTag extracts the form tag's name (the part before any comma).
func WireNameFromTag(field reflect.StructField) string {
	tag := field.Tag.Get("form")
	if tag == "" {
		return ""
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag
}

// KindForField classifies a *T field; returns KindUnsupported for types we cannot map.
func KindForField(t reflect.Type) Kind {
	if t.Kind() != reflect.Ptr {
		return KindUnsupported
	}
	el := t.Elem()
	switch el.Kind() {
	case reflect.Bool:
		return KindBool
	case reflect.Int:
		return KindInt
	case reflect.String:
		return KindString // includes named string types like FilterOperator
	case reflect.Array:
		// openapi_types.UUID is [16]byte, so a SCALAR uuid parameter arrives here
		// rather than as a Kind we recognise. Before this case existed it fell
		// through to KindUnsupported and was dropped with no flag and no error —
		// see TestNoParamIsSilentlyUnsupported.
		if el == uuidType {
			return KindUUID
		}
	case reflect.Slice:
		switch {
		case el.Elem() == uuidType:
			return KindUUIDSlice
		case el.Elem().Kind() == reflect.String:
			return KindStringSlice
		case el.Elem().Kind() == reflect.Int:
			return KindIntSlice
		}
	}
	return KindUnsupported
}

// Derive returns one Flag per field of paramsType that carries a form tag, is
// not in denylist, and maps to a supported kind.
func Derive(paramsType reflect.Type, denylist map[string]bool) []Flag {
	var out []Flag
	for i := 0; i < paramsType.NumField(); i++ {
		f := paramsType.Field(i)
		wire := WireNameFromTag(f)
		if wire == "" || denylist[wire] {
			continue
		}
		kind := KindForField(f.Type)
		if kind == KindUnsupported {
			continue
		}
		out = append(out, Flag{
			FieldName: f.Name,
			WireName:  wire,
			FlagName:  FlagNameForWire(wire),
			Kind:      kind,
			Type:      f.Type.Elem(),
		})
	}
	return out
}

// refFlagKinds names, by flag, the kind/<id> reference a derived id flag
// also takes.
var refFlagKinds = map[string]string{
	"comment-thread-id":        "comment-thread",
	"data-source-model-id":     "cloud-source",
	"dataset-rule-ids":         "table-rule",
	"history-ids":              "history",
	"problem-dataset-rule-id":  "table-rule",
	"review-change-history-id": "history",
	"review-group-id":          "data-review-group",
	"stored-item-id":           "file",
	"warning-item-id":          "warning",
}

// Register adds one cobra flag per derived Flag. help supplies the description
// for a wire name; it is the caller's, because the two commands that use this
// share most parameters but not all of them.
func Register(cmd *cobra.Command, flags []Flag, help func(wireName string) string) {
	for _, f := range flags {
		if slot, ok := refFlagKinds[f.FlagName]; ok && registerRef(cmd, f, slot, help(f.WireName)) {
			continue
		}
		switch f.Kind {
		case KindBool:
			cmd.Flags().Bool(f.FlagName, false, help(f.WireName))
		case KindInt:
			cmd.Flags().Int(f.FlagName, 0, help(f.WireName))
		case KindString, KindUUID:
			cmd.Flags().String(f.FlagName, "", help(f.WireName))
		case KindStringSlice, KindUUIDSlice:
			cmd.Flags().StringArray(f.FlagName, nil, help(f.WireName))
		case KindIntSlice:
			cmd.Flags().IntSlice(f.FlagName, nil, help(f.WireName))
		}
	}
}

// registerRef registers f as a flag that also takes slot/<id>, reporting
// false for a kind no Ref helper covers.
func registerRef(cmd *cobra.Command, f Flag, slot, usage string) bool {
	switch f.Kind {
	case KindInt:
		cmdutil.RefInt(cmd.Flags(), f.FlagName, slot, usage)
	case KindString, KindUUID:
		cmdutil.RefString(cmd.Flags(), f.FlagName, slot, usage)
	case KindStringSlice, KindUUIDSlice:
		cmdutil.RefStringArray(cmd.Flags(), f.FlagName, slot, usage)
	case KindIntSlice:
		cmdutil.RefIntSlice(cmd.Flags(), f.FlagName, slot, usage)
	default:
		return false
	}
	return true
}

// Build fills params (a pointer to a params struct) from the registered flags.
// Only flags the user changed are set, so an untouched flag leaves its pointer
// nil and the backend default applies.
//
// override runs for every flag the user did NOT change. Returning true makes
// Build set the field anyway from the flag's current (default) value -- which
// is how `get data` pins page=1 and limit=20 while every other parameter stays
// absent from the query string.
//
// override may mutate cmd's flag set — calling cmd.Flags().Set is how a caller
// pins a default, and pflag records that as Changed — so once Build returns,
// neither cmd.Flags().Changed(name) nor a read of the flag's value (GetInt,
// GetString, ...) still distinguishes what the user typed from what an override
// pinned. Do all flag-inspecting validation before calling Build.
func Build(cmd *cobra.Command, params any, flags []Flag, override func(cmd *cobra.Command, f Flag) bool) error {
	pv := reflect.ValueOf(params).Elem()
	for _, f := range flags {
		changed := cmd.Flags().Changed(f.FlagName)
		forced := false
		if !changed && override != nil {
			forced = override(cmd, f)
		}
		if !changed && !forced {
			continue // omit: keep pointer nil, backend default applies
		}
		field := pv.FieldByName(f.FieldName)
		if !field.CanSet() {
			continue
		}
		if err := setField(cmd, field, f); err != nil {
			return err
		}
	}
	return nil
}

// setField assigns one parsed flag value onto a *T params field via reflection.
func setField(cmd *cobra.Command, field reflect.Value, f Flag) error {
	switch f.Kind {
	case KindBool:
		v, _ := cmd.Flags().GetBool(f.FlagName)
		p := reflect.New(field.Type().Elem())
		p.Elem().SetBool(v)
		field.Set(p)
	case KindInt:
		v, _ := cmd.Flags().GetInt(f.FlagName)
		p := reflect.New(field.Type().Elem())
		p.Elem().SetInt(int64(v))
		field.Set(p)
	case KindString:
		v, _ := cmd.Flags().GetString(f.FlagName)
		p := reflect.New(field.Type().Elem()) // handles named string types (FilterOperator)
		p.Elem().SetString(v)
		field.Set(p)
	case KindStringSlice:
		v, _ := cmd.Flags().GetStringArray(f.FlagName)
		if f.WireName == fieldsWire {
			names := splitFieldNames(v)
			if len(names) == 0 {
				return qerrors.NewBadUsagef("--%s needs at least one column name", f.FlagName)
			}
			v = names
		}
		field.Set(reflect.ValueOf(&v)) // field is *[]string
	case KindIntSlice:
		v, _ := cmd.Flags().GetIntSlice(f.FlagName)
		field.Set(reflect.ValueOf(&v)) // field is *[]int
	case KindUUIDSlice:
		v, _ := cmd.Flags().GetStringArray(f.FlagName)
		uuids := make([]openapi_types.UUID, 0, len(v))
		for _, s := range v {
			u, err := parseUUIDFlag(s, f.FlagName)
			if err != nil {
				return err
			}
			uuids = append(uuids, u)
		}
		field.Set(reflect.ValueOf(&uuids)) // field is *[]openapi_types.UUID
	case KindUUID:
		v, _ := cmd.Flags().GetString(f.FlagName)
		parsed, err := parseUUIDFlag(v, f.FlagName)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(&parsed)) // field is *openapi_types.UUID
	}
	return nil
}

// fieldsWire is the wire name of the parameter whose values Build splits with
// splitFieldNames.
const fieldsWire = "fields"

// splitFieldNames splits each value on commas, trims surrounding spaces from
// every piece and drops the empty ones, so `a,b` and `a` `b` give the same
// list.
func splitFieldNames(values []string) []string {
	var names []string
	for _, v := range values {
		for _, piece := range strings.Split(v, ",") {
			if piece = strings.TrimSpace(piece); piece != "" {
				names = append(names, piece)
			}
		}
	}
	return names
}

// parseUUIDFlag parses one UUID flag value. A malformed value is a client-side
// flag validation failure, so it carries ExitBadUsage (2) — and it Wraps rather
// than reformats so qerrors.GetExitCode's Unwrap chain stays intact.
func parseUUIDFlag(value, flagName string) (openapi_types.UUID, error) {
	u, err := uuid.Parse(value)
	if err != nil {
		return openapi_types.UUID{}, qerrors.Wrap(qerrors.ExitBadUsage, err,
			fmt.Sprintf("invalid UUID %q for --%s", value, flagName))
	}
	return u, nil
}
