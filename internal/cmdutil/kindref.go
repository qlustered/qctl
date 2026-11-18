package cmdutil

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// idShape is the form of the id a kind/<id> reference carries.
type idShape int

const (
	intID idShape = iota + 1
	uuidID
	// nameID is any non-empty text: the kind is keyed by name.
	nameID
)

// refKinds are the kinds `-o name` prints, each with the shape of its id.
var refKinds = map[string]idShape{
	"alert":                 intID,
	"backup-settings":       intID,
	"change-proposal":       uuidID,
	"cloud-source":          intID,
	"comment-thread":        uuidID,
	"composite-dry-run-job": intID,
	"composite-run-job":     intID,
	"context":               nameID,
	"data-review":           uuidID,
	"data-review-group":     uuidID,
	"deliverable":           uuidID,
	"deliverable-revision":  uuidID,
	"deliverable-run":       intID,
	"deliverable-template":  intID,
	"destination":           intID,
	"duplicate-group":       uuidID,
	"dry-run-job":           intID,
	"email-sender":          uuidID,
	"error-incident":        intID,
	"file":                  intID,
	"history":               intID,
	"ingestion-job":         intID,
	"linked-table":          intID,
	"notification":          uuidID,
	"org":                   uuidID,
	"pii-access-request":    intID,
	"pii-grant":             intID,
	"privilege-request":     uuidID,
	"profiling-job":         intID,
	"queue-message":         intID,
	"rule":                  uuidID,
	"rule-merge":            intID,
	"rule-review":           uuidID,
	"rule-revision":         uuidID,
	"rule-run-replay":       intID,
	"rule-suggestion":       uuidID,
	"session":               uuidID,
	"standard-field":        uuidID,
	"table":                 intID,
	"table-kind":            uuidID,
	"table-rule":            uuidID,
	"table-series":          uuidID,
	"warning":               intID,
}

// refKindAliases maps legacy prefix names to their current names.
var refKindAliases = map[string]string{
	"concept": "standard-field",
}

// RefKinds returns every kind ParseKindRef recognizes, sorted.
func RefKinds() []string {
	kinds := make([]string, 0, len(refKinds))
	for k := range refKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// slotAccepts lists, for a slot kind, every prefix the slot takes.
var slotAccepts = map[string][]string{
	"linked-table": {"linked-table", "table"},
	"rule":         {"rule", "rule-revision"},
}

func accepted(slot string) []string {
	if kinds, ok := slotAccepts[slot]; ok {
		return kinds
	}
	return []string{slot}
}

// ParseKindRef strips a kind/ prefix that slot accepts from v.
//
// v passes through unchanged unless it is <known kind>/<positive int or
// canonical UUID>, so a name such as "Q1/2024" stays a name. A name-keyed
// kind's <kind>/<name> is stripped only for a slot that accepts the kind, and
// passes through for any other. A recognized reference whose kind slot does
// not accept, or whose id has the wrong shape for its kind, exits 2.
func ParseKindRef(slot, v string) (string, error) {
	kind, id, ok := strings.Cut(v, "/")
	if ok && refKindAliases[kind] != "" {
		kind = refKindAliases[kind]
		v = kind + "/" + id
	}
	if !ok {
		return v, nil
	}
	shape, known := refKinds[kind]
	if !known {
		return v, nil
	}
	if shape == nameID {
		if id == "" || !containsString(accepted(slot), kind) {
			return v, nil
		}
		return id, nil
	}
	got := shapeOf(id)
	if got == 0 {
		return v, nil
	}
	kinds := accepted(slot)
	if !containsString(kinds, kind) {
		return "", qerrors.NewBadUsagef("%q is a %s reference; this takes %s", v, kind, strings.Join(kinds, " or "))
	}
	if got != shape {
		want := "an integer id"
		if shape == uuidID {
			want = "a full UUID"
		}
		return "", qerrors.NewBadUsagef("%q: a %s reference carries %s", v, kind, want)
	}
	return id, nil
}

func shapeOf(id string) idShape {
	if n, err := strconv.Atoi(id); err == nil && n > 0 && strconv.Itoa(n) == id {
		return intID
	}
	if len(id) == 36 {
		if _, err := uuid.Parse(id); err == nil {
			return uuidID
		}
	}
	return 0
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// AnnotationArgKinds is the cobra annotation key declaring the kind of
// reference each positional argument names, comma-separated by slot.
const AnnotationArgKinds = "arg_kinds"

// WithArgKinds declares the kind each positional slot names, merged into the
// annotation map catalog.For built. The last kind repeats for any further
// arguments, and "-" marks a slot that is not a reference.
func WithArgKinds(base map[string]string, kinds ...string) map[string]string {
	merged := make(map[string]string, len(base)+1)
	for k, v := range base {
		merged[k] = v
	}
	merged[AnnotationArgKinds] = strings.Join(kinds, ",")
	return merged
}

// noRefArg is the slot kind for a positional that is not a reference.
const noRefArg = "-"

// ArgKinds returns the slot kinds of cmd's positionals: its declaration, else
// its own resource when that is a kind ParseKindRef recognizes.
func ArgKinds(cmd *cobra.Command) []string {
	if raw, ok := cmd.Annotations[AnnotationArgKinds]; ok {
		return strings.Split(raw, ",")
	}
	if resource := cmd.Annotations[catalog.AnnotationResource]; refKinds[resource] != 0 {
		return []string{resource}
	}
	return nil
}

// StripKindRefs wraps the RunE of c and every command below it so that each
// positional argument has a kind/ prefix its slot accepts removed.
func StripKindRefs(c *cobra.Command) {
	if inner := c.RunE; inner != nil {
		cmd := c
		c.RunE = func(cc *cobra.Command, args []string) error {
			if err := stripArgs(ArgKinds(cmd), args); err != nil {
				return err
			}
			return inner(cc, args)
		}
	}
	for _, sub := range c.Commands() {
		StripKindRefs(sub)
	}
}

func stripArgs(kinds, args []string) error {
	if len(kinds) == 0 {
		return nil
	}
	for i, arg := range args {
		slot := kinds[len(kinds)-1]
		if i < len(kinds) {
			slot = kinds[i]
		}
		if slot == noRefArg {
			continue
		}
		stripped, err := ParseKindRef(slot, arg)
		if err != nil {
			return err
		}
		args[i] = stripped
	}
	return nil
}

// pflag type names a ref flag reports, so that pflag's GetInt and GetIntSlice
// read it.
const (
	flagTypeInt      = "int"
	flagTypeIntSlice = "intSlice"
)

// refValue is a pflag.Value that strips a kind/ prefix before storing.
type refValue struct {
	slot  string
	typ   string
	value string
	intp  *int
}

func (v *refValue) String() string { return v.value }
func (v *refValue) Type() string   { return v.typ }

func (v *refValue) Set(s string) error {
	stripped, err := ParseKindRef(v.slot, s)
	if err != nil {
		return err
	}
	if v.typ == flagTypeInt {
		n, err := atoiLikePflag(stripped)
		if err != nil {
			return err
		}
		if v.intp != nil {
			*v.intp = n
		}
	}
	v.value = stripped
	return nil
}

func atoiLikePflag(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("strconv.ParseInt: parsing %q: invalid syntax", s)
	}
	return n, nil
}

// RefInt registers an int flag that also takes slot/<id>. GetInt reads it.
func RefInt(flags *pflag.FlagSet, name, slot, usage string) {
	flags.Var(&refValue{slot: slot, typ: flagTypeInt, value: "0"}, name, usage)
}

// RefIntVar is RefInt that also stores the value in p, which it zeroes.
func RefIntVar(flags *pflag.FlagSet, p *int, name, slot, usage string) {
	*p = 0
	flags.Var(&refValue{slot: slot, typ: flagTypeInt, value: "0", intp: p}, name, usage)
}

// RefString registers a string flag that also takes slot/<id>. GetString reads it.
func RefString(flags *pflag.FlagSet, name, slot, usage string) {
	flags.Var(&refValue{slot: slot, typ: "string"}, name, usage)
}

// RefStringP is RefString with a shorthand.
func RefStringP(flags *pflag.FlagSet, name, shorthand, slot, usage string) {
	flags.VarP(&refValue{slot: slot, typ: "string"}, name, shorthand, usage)
}

// refListValue is a repeatable pflag.Value that strips a kind/ prefix from
// each element.
type refListValue struct {
	slot   string
	typ    string
	values []string
}

func (v *refListValue) Type() string { return v.typ }

func (v *refListValue) String() string {
	if v.typ == flagTypeIntSlice {
		return "[" + strings.Join(v.values, ",") + "]"
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(v.values)
	w.Flush()
	return "[" + strings.TrimSuffix(b.String(), "\n") + "]"
}

func (v *refListValue) Set(s string) error {
	parts := []string{s}
	if v.typ == flagTypeIntSlice {
		parts = strings.Split(s, ",")
	}
	stripped := make([]string, 0, len(parts))
	for _, part := range parts {
		id, err := ParseKindRef(v.slot, part)
		if err != nil {
			return err
		}
		if v.typ == flagTypeIntSlice {
			if _, err := atoiLikePflag(id); err != nil {
				return err
			}
		}
		stripped = append(stripped, id)
	}
	v.values = append(v.values, stripped...)
	return nil
}

// Append adds s as Set does.
func (v *refListValue) Append(s string) error { return v.Set(s) }

// Replace sets the values to vals, each checked and stripped as Set does it.
// A refused element leaves the values unchanged.
func (v *refListValue) Replace(vals []string) error {
	next := &refListValue{slot: v.slot, typ: v.typ}
	for _, s := range vals {
		if err := next.Set(s); err != nil {
			return err
		}
	}
	v.values = next.values
	return nil
}

// GetSlice returns a copy of the stripped values.
func (v *refListValue) GetSlice() []string { return append([]string(nil), v.values...) }

// RefStringArray registers a repeatable string flag whose every value also
// takes slot/<id>. GetStringArray reads it.
func RefStringArray(flags *pflag.FlagSet, name, slot, usage string) {
	varList(flags, &refListValue{slot: slot, typ: "stringArray"}, name, usage)
}

// RefIntSlice registers an int-list flag whose every element also takes
// slot/<id>. GetIntSlice reads it.
func RefIntSlice(flags *pflag.FlagSet, name, slot, usage string) {
	varList(flags, &refListValue{slot: slot, typ: flagTypeIntSlice}, name, usage)
}

// varList registers v with an empty default, so --help shows none.
func varList(flags *pflag.FlagSet, v *refListValue, name, usage string) {
	flags.Var(v, name, usage)
	flags.Lookup(name).DefValue = ""
}

// RefFlagKind returns the slot kind of a flag registered by one of the Ref
// helpers, or "" for any other flag.
func RefFlagKind(f *pflag.Flag) string {
	switch v := f.Value.(type) {
	case *refValue:
		return v.slot
	case *refListValue:
		return v.slot
	}
	return ""
}
