package deliverables

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// ManifestKind is the kind of a deliverable manifest.
const ManifestKind = "Deliverable"

const invalidManifest = "invalid Deliverable manifest"

// Keys of a Deliverable manifest's spec.
const (
	keyName             = "name"
	keyReceiverLabel    = "receiver_label"
	keyFilenamePattern  = "filename_pattern"
	keyPeriodParam      = "period_param"
	keyRelease          = "release"
	keyDescription      = "description"
	keyTemplate         = "template"
	keyAcknowledgeLossy = "acknowledge_lossy"
	keySheets           = "sheets"
	keyCells            = "cells"
)

// Manifest is the writable deliverable document.
type Manifest struct {
	APIVersion string           `yaml:"apiVersion" json:"apiVersion"`
	Kind       string           `yaml:"kind" json:"kind"`
	Metadata   ManifestMetadata `yaml:"metadata" json:"metadata"`
	Spec       ManifestSpec     `yaml:"spec" json:"spec"`
	Status     *ManifestStatus  `yaml:"status,omitempty" json:"status,omitempty"`
	present    map[string]bool
}

// ManifestMetadata identifies a deliverable by slug.
type ManifestMetadata struct {
	Slug string `yaml:"slug" json:"slug"`
}

// ManifestSpec holds a deliverable's basic fields and one release.
type ManifestSpec struct {
	Name             string  `yaml:"name" json:"name"`
	ReceiverLabel    *string `yaml:"receiver_label,omitempty" json:"receiver_label,omitempty"`
	FilenamePattern  string  `yaml:"filename_pattern" json:"filename_pattern"`
	PeriodParam      *string `yaml:"period_param" json:"period_param"`
	Release          string  `yaml:"release,omitempty" json:"release,omitempty"`
	Description      *string `yaml:"description,omitempty" json:"description,omitempty"`
	Template         string  `yaml:"template,omitempty" json:"template,omitempty"`
	AcknowledgeLossy bool    `yaml:"acknowledge_lossy,omitempty" json:"acknowledge_lossy,omitempty"`
	// Sheets are the release's sheet bindings, keyed by template sheet name.
	Sheets map[string]ManifestSheet `yaml:"sheets,omitempty" json:"sheets,omitempty"`
	Cells  map[string]ManifestCell  `yaml:"cells,omitempty" json:"cells,omitempty"`
}

// ManifestStatus is the exported deliverable and revision identity.
type ManifestStatus struct {
	ID            uuid.UUID  `yaml:"id" json:"id"`
	RevisionID    *uuid.UUID `yaml:"revision_id,omitempty" json:"revision_id,omitempty"`
	RevisionState string     `yaml:"revision_state,omitempty" json:"revision_state,omitempty"`
}

// ParseManifest decodes and validates a Deliverable document. Every error it
// returns exits 2.
func ParseManifest(data []byte) (*Manifest, error) {
	var envelope struct {
		Spec map[string]any `yaml:"spec"`
	}
	envelopeErr := yaml.Unmarshal(data, &envelope)
	sheetKeys, totalKeys := writtenSheetKeys(data), writtenTotalKeys(data)
	if err := validateSheetShapes(data); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, invalidManifest)
	}
	var m Manifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, invalidManifest)
	}
	if envelopeErr != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, envelopeErr, invalidManifest)
	}
	m.present = map[string]bool{}
	for key := range envelope.Spec {
		m.present[key] = true
	}
	for name, sheet := range m.Spec.Sheets {
		sheet.present = sheetKeys[name]
		if sheet.present == nil {
			sheet.present = map[string]bool{}
		}
		m.Spec.Sheets[name] = sheet
	}
	for key, cell := range m.Spec.Cells {
		if cell.Total != nil {
			cell.Total.present = totalKeys[key]
			if cell.Total.present == nil {
				cell.Total.present = map[string]bool{}
			}
		}
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	spec := m.Spec
	switch {
	case m.APIVersion != pkgmanifest.APIVersionV1 || m.Kind != ManifestKind:
		return qerrors.NewBadUsage("Deliverable requires apiVersion: qluster.ai/v1 and kind: Deliverable")
	case m.Metadata.Slug == "":
		return qerrors.NewBadUsage("metadata.slug is required")
	case spec.Name == "" || utf8.RuneCountInString(spec.Name) > 200:
		return qerrors.NewBadUsage("spec.name must be 1–200 characters")
	case spec.FilenamePattern == "":
		return qerrors.NewBadUsage("spec.filename_pattern is required")
	case m.Has(keyReceiverLabel) && spec.ReceiverLabel == nil:
		return qerrors.NewBadUsage(`spec.receiver_label cannot be null; use "" for none`)
	case spec.PeriodParam != nil && isBlank(*spec.PeriodParam):
		return qerrors.NewBadUsage("spec.period_param must be non-blank; use null to clear it")
	}
	if err := m.validateRelease(); err != nil {
		return err
	}
	if err := m.validateSheets(); err != nil {
		return err
	}
	return m.validateCells()
}

// validateRelease checks spec.release and the keys that require it.
func (m *Manifest) validateRelease() error {
	spec := m.Spec
	if !m.Has(keyRelease) {
		for _, key := range []string{keyDescription, keyTemplate, keyAcknowledgeLossy, keySheets, keyCells} {
			if m.Has(key) {
				return qerrors.NewBadUsagef("spec.%s requires spec.release", key)
			}
		}
		return nil
	}
	if spec.Release == "" || utf8.RuneCountInString(spec.Release) > 100 {
		return qerrors.NewBadUsage("spec.release must be 1–100 characters")
	}
	if !m.Has(keyTemplate) {
		if m.Has(keyAcknowledgeLossy) {
			return qerrors.NewBadUsage("spec.acknowledge_lossy requires spec.template")
		}
		return nil
	}
	return ValidateTemplatePath(spec.Template)
}

// isBlank reports whether s holds only Unicode white space and the separators
// U+001C to U+001F.
func isBlank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }) == ""
}

// excelCellLimit is the most characters an Excel cell holds.
const excelCellLimit = 32767

// validateLiteral is bad usage, worded as a sentence about "literal", when
// value is longer than excelCellLimit characters or holds a control character
// below U+0020 other than tab, newline and carriage return.
func validateLiteral(value string) error {
	if n := utf8.RuneCountInString(value); n > excelCellLimit {
		return qerrors.NewBadUsagef("literal is %d characters long; an Excel cell holds at most %d", n, excelCellLimit)
	}
	position := 0
	for _, r := range value {
		position++
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return qerrors.NewBadUsagef("literal holds control character U+%04X at character %d; "+
				"an Excel cell takes no control character but tab, newline and carriage return", r, position)
		}
	}
	return nil
}

// Has reports whether spec.<key> was written, even as null: in the parsed
// document, or by FromAPI.
func (m *Manifest) Has(key string) bool {
	return m.present[key]
}

// ExportRevision returns the enabled revision, else the only revision, else nil.
func ExportRevision(revisions []Revision) *Revision {
	for i := range revisions {
		if revisions[i].State == api.DeliverableRevisionStateEnabled {
			return &revisions[i]
		}
	}
	if len(revisions) == 1 {
		return &revisions[0]
	}
	return nil
}

// FromAPI returns item as a manifest and, when rev is non-nil, rev's release,
// its description when non-nil, and its id and state under status. It always
// writes receiver_label and period_param, and never writes template or
// acknowledge_lossy.
func FromAPI(item *Deliverable, rev *Revision) *Manifest {
	label := item.ReceiverLabel
	m := &Manifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       ManifestKind,
		Metadata:   ManifestMetadata{Slug: item.Slug},
		Spec: ManifestSpec{
			Name: item.Name, ReceiverLabel: &label,
			FilenamePattern: item.FilenamePattern, PeriodParam: item.PeriodParam,
		},
		Status:  &ManifestStatus{ID: item.ID},
		present: map[string]bool{keyName: true, keyReceiverLabel: true, keyFilenamePattern: true, keyPeriodParam: true},
	}
	if rev == nil {
		return m
	}
	m.Spec.Release = rev.Release
	m.present[keyRelease] = true
	if rev.Description != nil {
		m.Spec.Description = rev.Description
		m.present[keyDescription] = true
	}
	id := rev.ID
	m.Status.RevisionID = &id
	m.Status.RevisionState = string(rev.State)
	return m
}
