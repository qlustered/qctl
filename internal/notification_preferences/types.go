package notification_preferences //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// Kind is the manifest kind `qctl apply -f` dispatches here.
const Kind = "NotificationPreferences"

// ManifestName is the metadata.name `get notification-preferences -o yaml`
// writes.
const ManifestName = "me"

// Row is one notification type in the table view.
type Row struct {
	Type       string `json:"type"`
	Email      bool   `json:"email"`
	Default    bool   `json:"default"`
	Overridden bool   `json:"overridden"`
}

// Metadata is the manifest metadata block.
type Metadata struct {
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// Spec is the desired state: spec.email replaces the stored overrides. A
// value is a pointer so a null in the manifest can be told apart from false.
type Spec struct {
	Email map[string]*bool `yaml:"email" json:"email"`
}

// Status is read-only context written by `get -o yaml`; apply ignores it.
type Status struct {
	Effective map[string]bool `yaml:"effective,omitempty" json:"effective,omitempty"`
	Defaults  map[string]bool `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// Manifest is the kind: NotificationPreferences manifest.
type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
	Status     *Status  `yaml:"status,omitempty" json:"status,omitempty"`
}

// Keys returns the notification types p carries, sorted.
func Keys(p *Preferences) []string {
	return slices.Sorted(maps.Keys(p.Preferences))
}

// Overrides returns the entries of p's effective preferences that differ from
// its defaults, as a non-nil map.
func Overrides(p *Preferences) map[string]bool {
	return WithoutDefaults(p, p.Preferences)
}

// WithoutDefaults returns the entries of values that differ from p's
// defaults, as a non-nil map.
func WithoutDefaults(p *Preferences, values map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range values {
		if def, ok := p.Defaults[k]; !ok || def != v {
			out[k] = v
		}
	}
	return out
}

// Rows returns one Row per notification type, sorted by type.
func Rows(p *Preferences) []Row {
	keys := Keys(p)
	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		email, def := p.Preferences[k], p.Defaults[k]
		rows = append(rows, Row{Type: k, Email: email, Default: def, Overridden: email != def})
	}
	return rows
}

// ToManifest converts p into an apply-able manifest whose spec.email holds
// the entries that differ from the defaults.
func ToManifest(p *Preferences) *Manifest {
	return &Manifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       Kind,
		Metadata:   Metadata{Name: ManifestName},
		Spec:       Spec{Email: pointers(Overrides(p))},
		Status:     &Status{Effective: maps.Clone(p.Preferences), Defaults: maps.Clone(p.Defaults)},
	}
}

func pointers(values map[string]bool) map[string]*bool {
	out := make(map[string]*bool, len(values))
	for k, v := range values {
		out[k] = &v
	}
	return out
}

// EmailValues returns m's spec.email as plain values, or an exit-2 error
// naming every key whose value is null.
func (m *Manifest) EmailValues() (map[string]bool, error) {
	out := make(map[string]bool, len(m.Spec.Email))
	var nulls []string
	for k, v := range m.Spec.Email {
		if v == nil {
			nulls = append(nulls, k)
			continue
		}
		out[k] = *v
	}
	if len(nulls) > 0 {
		slices.Sort(nulls)
		return nil, qerrors.NewBadUsagef("manifest validation failed:\n  - spec.email.%s must be true or false, not null",
			strings.Join(nulls, ", spec.email."))
	}
	return out, nil
}

// RequireKnownTypes returns an exit-2 error naming every entry of types that
// is not a key of p, and the keys that are.
func RequireKnownTypes(p *Preferences, types []string) error {
	var unknown []string
	for _, t := range types {
		if _, ok := p.Preferences[t]; !ok && !slices.Contains(unknown, t) {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	quoted := make([]string, len(unknown))
	for i, t := range unknown {
		quoted[i] = fmt.Sprintf("%q", t)
	}
	noun := "type"
	if len(unknown) > 1 {
		noun = "types"
	}
	return qerrors.NewBadUsagef("unknown notification email preference %s %s\nvalid types: %s",
		noun, strings.Join(quoted, ", "), strings.Join(Keys(p), ", "))
}

// WithEmail returns the overrides that make each of types' effective setting
// enabled and leave every other type's effective setting as it is in p.
// Entries equal to their default are left out.
func WithEmail(p *Preferences, types []string, enabled bool) map[string]bool {
	effective := maps.Clone(p.Preferences)
	for _, t := range types {
		effective[t] = enabled
	}
	return WithoutDefaults(p, effective)
}
