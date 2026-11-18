package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/concepts"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/slug"
)

// Manifest is the writable table-kind document.
type Manifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		Slug string `yaml:"slug" json:"slug"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		Name           string   `yaml:"name" json:"name"`
		Description    *string  `yaml:"description" json:"description"`
		StandardFields []string `yaml:"standard_fields" json:"standard_fields"`
		LegacyConcepts []string `yaml:"concepts,omitempty" json:"concepts,omitempty"`
	} `yaml:"spec" json:"spec"`
	Status *struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	} `yaml:"status,omitempty" json:"status,omitempty"`
}

// ParseManifest validates a TableKind document.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid TableKind manifest")
	}
	if m.APIVersion != pkgmanifest.APIVersionV1 || m.Kind != "TableKind" {
		return nil, qerrors.NewBadUsage("TableKind requires apiVersion: qluster.ai/v1 and kind: TableKind")
	}
	if m.Metadata.Slug == "" {
		return nil, qerrors.NewBadUsage("metadata.slug is required")
	}
	if strings.TrimSpace(m.Spec.Name) == "" || len([]rune(m.Spec.Name)) > 255 {
		return nil, qerrors.NewBadUsage("spec.name must be 1–255 characters")
	}
	if m.Spec.StandardFields != nil && m.Spec.LegacyConcepts != nil {
		return nil, qerrors.NewBadUsage("set spec.standard_fields or spec.concepts, not both")
	}
	if m.Spec.LegacyConcepts != nil {
		m.Spec.StandardFields = m.Spec.LegacyConcepts
		m.Spec.LegacyConcepts = nil
	}
	if m.Spec.StandardFields == nil {
		return nil, qerrors.NewBadUsage("spec.standard_fields must be a list (use [] to unlink all standard fields)")
	}
	seen := map[string]bool{}
	for _, slug := range m.Spec.StandardFields {
		if slug == "" || seen[slug] {
			return nil, qerrors.NewBadUsagef("spec.standard_fields contains an empty or duplicate slug %q", slug)
		}
		seen[slug] = true
	}
	return &m, nil
}

// FromAPI converts a table kind to an applyable manifest.
func FromAPI(item *DatasetKindWithConcepts) *Manifest {
	m := &Manifest{APIVersion: pkgmanifest.APIVersionV1, Kind: "TableKind"}
	m.Metadata.Slug = item.Slug
	m.Spec.Name = item.Name
	m.Spec.Description = item.Description
	m.Spec.StandardFields = []string{}
	if item.Concepts != nil {
		for _, concept := range *item.Concepts {
			m.Spec.StandardFields = append(m.Spec.StandardFields, concept.Slug)
		}
	}
	sort.Strings(m.Spec.StandardFields)
	m.Status = &struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	}{ID: item.ID}
	return m
}

// Change describes one table-kind field change.
type Change struct {
	Field string
	Old   any
	New   any
}

// Plan holds the body shared by apply and diff.
type Plan struct {
	ID      uuid.UUID
	Create  bool
	Current *DatasetKindWithConcepts
	Body    map[string]any
	Changes []Change
}

// Prepare compares a manifest with the exact table-kind slug and resolves its concept slugs.
func (c *Client) Prepare(token string, m *Manifest, conceptClient *concepts.Client) (*Plan, error) {
	current, err := c.BySlug(token, m.Metadata.Slug)
	if err != nil {
		return nil, err
	}
	if current == nil && slug.Generate(m.Spec.Name, 0) != m.Metadata.Slug {
		return nil, qerrors.NewBadUsagef("metadata.slug %q differs from the slug %q generated from spec.name", m.Metadata.Slug, slug.Generate(m.Spec.Name, 0))
	}
	ids := make([]uuid.UUID, 0, len(m.Spec.StandardFields))
	for _, slug := range m.Spec.StandardFields {
		concept, err := conceptClient.BySlug(token, slug)
		if err != nil {
			return nil, fmt.Errorf("resolve standard field %q: %w", slug, err)
		}
		if concept == nil {
			return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("standard field slug %q was not found", slug)))
		}
		ids = append(ids, concept.ID)
	}
	p := &Plan{Create: current == nil, Current: current, Body: map[string]any{}}
	if current == nil {
		p.Body["name"] = m.Spec.Name
		p.Body["description"] = m.Spec.Description
		p.Body["concept_ids"] = ids
		p.Changes = []Change{{"name", nil, m.Spec.Name}, {"description", nil, m.Spec.Description}, {"standard_fields", nil, m.Spec.StandardFields}}
		return p, nil
	}
	p.ID = current.ID
	if current.Name != m.Spec.Name {
		p.Body["name"] = m.Spec.Name
		p.Changes = append(p.Changes, Change{"name", current.Name, m.Spec.Name})
	}
	if !reflect.DeepEqual(current.Description, m.Spec.Description) {
		p.Body["description"] = m.Spec.Description
		p.Changes = append(p.Changes, Change{"description", current.Description, m.Spec.Description})
	}
	old := []string{}
	if current.Concepts != nil {
		for _, concept := range *current.Concepts {
			old = append(old, concept.Slug)
		}
	}
	sort.Strings(old)
	wanted := append([]string{}, m.Spec.StandardFields...)
	sort.Strings(wanted)
	if !reflect.DeepEqual(old, wanted) {
		p.Changes = append(p.Changes, Change{"standard_fields", old, wanted})
	}
	if len(p.Changes) > 0 {
		p.Body["concept_ids"] = ids
	}
	return p, nil
}

// RequestJSON encodes the body apply sends and diff displays.
func (p *Plan) RequestJSON() ([]byte, error) { return json.Marshal(p.Body) }
