package concepts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// Manifest is the writable representation of a concept.
type Manifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		Slug string `yaml:"slug" json:"slug"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		Name              *string        `yaml:"name,omitempty" json:"name,omitempty"`
		Description       *string        `yaml:"description,omitempty" json:"description,omitempty"`
		Aliases           *[]string      `yaml:"aliases,omitempty" json:"aliases,omitempty"`
		ExpectedFieldType *api.FieldType `yaml:"expected_field_type,omitempty" json:"expected_field_type,omitempty"`
	} `yaml:"spec" json:"spec"`
	Status *struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	} `yaml:"status,omitempty" json:"status,omitempty"`
	present map[string]bool
}

// ParseManifest validates a StandardField document and records which spec keys it sets.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid StandardField manifest")
	}
	if m.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion %q", pkgmanifest.APIVersionV1)
	}
	canonical := pkgmanifest.CanonicalKind(m.Kind)
	if canonical != "StandardField" { //nolint:goconst // The wire kind belongs to this manifest type.
		return nil, qerrors.NewBadUsagef("expected kind StandardField (or Concept), got %q", m.Kind)
	}
	if strings.TrimSpace(m.Metadata.Slug) == "" {
		return nil, qerrors.NewBadUsage("metadata.slug is required")
	}
	present, err := specKeys(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid StandardField manifest")
	}
	m.present = present
	if m.present["name"] && m.Spec.Name == nil {
		return nil, qerrors.NewBadUsage("spec.name cannot be null")
	}
	if m.Spec.Name != nil && (strings.TrimSpace(*m.Spec.Name) == "" || len(*m.Spec.Name) > 255) {
		return nil, qerrors.NewBadUsage("spec.name must be 1–255 characters")
	}
	if m.Spec.Name != nil {
		*m.Spec.Name = strings.TrimSpace(*m.Spec.Name)
	}
	if m.present["aliases"] && m.Spec.Aliases == nil {
		return nil, qerrors.NewBadUsage("spec.aliases cannot be null")
	}
	return &m, nil
}

func specKeys(data []byte) (map[string]bool, error) {
	var envelope struct {
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	present := make(map[string]bool)
	for key := range envelope.Spec {
		present[key] = true
	}
	return present, nil
}

// FromAPI converts a concept to an apply-compatible manifest.
func FromAPI(item *Full) *Manifest {
	m := &Manifest{APIVersion: pkgmanifest.APIVersionV1, Kind: "StandardField"}
	m.Metadata.Slug = item.Slug
	m.Spec.Name = &item.Name
	m.Spec.Description = item.Description
	aliases := []string{}
	if item.Aliases != nil {
		aliases = *item.Aliases
	}
	m.Spec.Aliases = &aliases
	m.Spec.ExpectedFieldType = item.ExpectedFieldType
	m.Status = &struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	}{ID: item.ID}
	return m
}

// Change describes one requested field change.
type Change struct {
	Field string
	Old   any
	New   any
}

// Plan is the request apply will send, with the changes diff will display.
type Plan struct {
	ID      uuid.UUID
	Create  bool
	Current *Full
	Body    map[string]any
	Changes []Change
}

// Prepare compares the manifest with the exact slug in the organization's catalog.
func (c *Client) Prepare(token string, m *Manifest) (*Plan, error) {
	item, err := c.BySlug(token, m.Metadata.Slug)
	if err != nil {
		return nil, err
	}
	var p *Plan
	if item == nil {
		p, err = createPlan(m)
	} else {
		p = updatePlan(m, item)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Field < p.Changes[j].Field })
	return p, nil
}

func (p *Plan) add(key string, present bool, old, wanted any) {
	if !present {
		return
	}
	if !p.Create && reflect.DeepEqual(old, wanted) {
		return
	}
	p.Body[key] = wanted
	p.Changes = append(p.Changes, Change{key, old, wanted})
}

func createPlan(m *Manifest) (*Plan, error) {
	if m.Spec.Name == nil {
		return nil, qerrors.NewBadUsage("spec.name is required to create a standard field")
	}
	p := &Plan{Create: true, Body: map[string]any{"slug": m.Metadata.Slug}, Changes: []Change{{"slug", nil, m.Metadata.Slug}}}
	p.add("name", true, nil, *m.Spec.Name)
	p.add("description", m.present["description"], nil, m.Spec.Description)
	if m.Spec.Aliases != nil {
		p.add("aliases", true, nil, *m.Spec.Aliases)
	}
	p.add("expected_field_type", m.present["expected_field_type"], nil, m.Spec.ExpectedFieldType)
	return p, nil
}

func updatePlan(m *Manifest, item *Full) *Plan {
	p := &Plan{ID: item.ID, Current: item, Body: map[string]any{}}
	if m.Spec.Name != nil {
		p.add("name", true, item.Name, *m.Spec.Name)
	}
	p.add("description", m.present["description"], item.Description, m.Spec.Description)
	if m.Spec.Aliases != nil {
		old := []string{}
		if item.Aliases != nil {
			old = *item.Aliases
		}
		p.add("aliases", true, old, *m.Spec.Aliases)
	}
	p.add("expected_field_type", m.present["expected_field_type"], item.ExpectedFieldType, m.Spec.ExpectedFieldType)
	return p
}

// RequestJSON encodes the exact body to send or preview.
func (p *Plan) RequestJSON() ([]byte, error) {
	b, err := json.Marshal(p.Body)
	if err != nil {
		return nil, fmt.Errorf("encode standard field request: %w", err)
	}
	return b, nil
}
