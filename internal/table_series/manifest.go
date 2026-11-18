package table_series

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// Manifest is the writable representation of a table series.
type Manifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		Name string `yaml:"name" json:"name"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		Description      *string               `yaml:"description" json:"description"`
		Frequency        *api.SeriesFrequency  `yaml:"frequency" json:"frequency"`
		FirstPeriodKey   *string               `yaml:"first_period_key" json:"first_period_key"`
		PeriodColumn     *string               `yaml:"period_column" json:"period_column"`
		PeriodColumnKind *api.PeriodColumnKind `yaml:"period_column_kind" json:"period_column_kind"`
		DeadlineSchedule *DeadlineScheduleSpec `yaml:"deadline_schedule,omitempty" json:"deadline_schedule,omitempty"`
		Members          []ManifestMember      `yaml:"members" json:"members"`
	} `yaml:"spec" json:"spec"`
	Status *struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	} `yaml:"status,omitempty" json:"status,omitempty"`
	present map[string]any
}

// ManifestMember names a table and its period.
type ManifestMember struct {
	Table     string `yaml:"table" json:"table"`
	PeriodKey string `yaml:"period_key" json:"period_key"`
}

// ParseManifest validates a TableSeries document.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid TableSeries manifest")
	}
	if m.APIVersion != pkgmanifest.APIVersionV1 || m.Kind != "TableSeries" {
		return nil, qerrors.NewBadUsage("TableSeries requires apiVersion: qluster.ai/v1 and kind: TableSeries")
	}
	if strings.TrimSpace(m.Metadata.Name) == "" || len([]rune(m.Metadata.Name)) > 200 {
		return nil, qerrors.NewBadUsage("metadata.name must be 1–200 characters")
	}
	if m.Spec.Members == nil {
		return nil, qerrors.NewBadUsage("spec.members must be a list (use [] to remove all members)")
	}
	var envelope struct {
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid TableSeries manifest")
	}
	m.present = envelope.Spec
	if err := m.validateFrequency(); err != nil {
		return nil, err
	}
	if err := m.validatePeriodColumn(); err != nil {
		return nil, err
	}
	if err := m.validateDeadlineSchedule(); err != nil {
		return nil, err
	}
	tables, keys := map[string]bool{}, map[string]bool{}
	for i, member := range m.Spec.Members {
		if err := validateMember(member); err != nil {
			return nil, fmt.Errorf("spec.members[%d]: %w", i, err)
		}
		if tables[member.Table] || keys[member.PeriodKey] {
			return nil, qerrors.NewBadUsagef("spec.members[%d]: duplicate table or period_key", i)
		}
		tables[member.Table], keys[member.PeriodKey] = true, true
	}
	return &m, nil
}

func (m *Manifest) validateFrequency() error {
	if _, ok := m.present["frequency"]; ok && m.Spec.Frequency == nil {
		return qerrors.NewBadUsage("spec.frequency cannot be null; omit it to keep the current frequency")
	}
	values := api.EnumValues[api.SeriesFrequency]()
	if m.Spec.Frequency != nil && !slices.Contains(values, string(*m.Spec.Frequency)) {
		return qerrors.NewBadUsagef("spec.frequency must be one of: %s", strings.Join(values, ", "))
	}
	if m.Spec.FirstPeriodKey != nil {
		if err := ValidatePeriodKey(*m.Spec.FirstPeriodKey); err != nil {
			return fmt.Errorf("spec.first_period_key: %w", err)
		}
	}
	return nil
}

func (m *Manifest) validatePeriodColumn() error {
	_, column := m.present["period_column"]
	_, kind := m.present["period_column_kind"]
	if column != kind || (m.Spec.PeriodColumn == nil) != (m.Spec.PeriodColumnKind == nil) {
		return qerrors.NewBadUsage("spec.period_column and spec.period_column_kind must be supplied together, both set or both null")
	}
	if m.Spec.PeriodColumnKind != nil && !slices.Contains(api.EnumValues[api.PeriodColumnKind](), string(*m.Spec.PeriodColumnKind)) {
		return qerrors.NewBadUsage("spec.period_column_kind must be period_key or date")
	}
	return nil
}

func (m *Manifest) validateDeadlineSchedule() error {
	value, ok := m.present["deadline_schedule"]
	if !ok {
		return nil
	}
	block, _ := value.(map[string]any)
	if m.Spec.DeadlineSchedule == nil {
		return qerrors.NewBadUsage("spec.deadline_schedule cannot be null or empty: omit it to keep the schedule, or set an override to null to put its default back")
	}
	if len(block) == 0 {
		return qerrors.NewBadUsage("an empty spec.deadline_schedule changes nothing: omit it, or set an override to null to put its default back")
	}
	keys := make([]string, 0, len(block))
	for key := range block {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if strings.HasSuffix(key, "_enabled") && block[key] == nil {
			return qerrors.NewBadUsagef("spec.deadline_schedule.%s cannot be null; omit it to keep the switch", key)
		}
	}
	return nil
}

// scheduleNulls returns the spec.deadline_schedule keys written as null.
func (m *Manifest) scheduleNulls() map[string]bool {
	block, _ := m.present["deadline_schedule"].(map[string]any)
	nulls := map[string]bool{}
	for key, value := range block {
		if value == nil {
			nulls[key] = true
		}
	}
	return nulls
}

func validateMember(member ManifestMember) error {
	if strings.TrimSpace(member.Table) == "" {
		return qerrors.NewBadUsage("table is required")
	}
	if id, err := strconv.Atoi(member.Table); err == nil && id <= 0 {
		return qerrors.NewBadUsage("table ID must be positive")
	}
	return ValidatePeriodKey(member.PeriodKey)
}

// ValidatePeriodKey checks an explicit reporting period key.
func ValidatePeriodKey(key string) error {
	if key == "" || len([]rune(key)) > 200 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n") {
		return qerrors.NewBadUsage("period_key must be 1–200 characters on one line without surrounding whitespace")
	}
	return nil
}

// FromAPI converts a series, and its deadline schedule when schedule is not nil, to an apply-compatible manifest.
func FromAPI(item *Series, schedule *DeadlineSchedule) *Manifest {
	m := &Manifest{APIVersion: pkgmanifest.APIVersionV1, Kind: "TableSeries"}
	m.Metadata.Name = item.Name
	m.Spec.Description = item.Description
	frequency := item.Frequency
	m.Spec.Frequency = &frequency
	m.Spec.FirstPeriodKey = item.FirstPeriodKey
	m.Spec.PeriodColumn = item.PeriodColumn
	m.Spec.PeriodColumnKind = item.PeriodColumnKind
	if schedule != nil {
		m.Spec.DeadlineSchedule = ScheduleSpecFromAPI(schedule)
	}
	m.Spec.Members = []ManifestMember{}
	for _, member := range item.Members {
		m.Spec.Members = append(m.Spec.Members, ManifestMember{
			Table: strconv.Itoa(member.DatasetID), PeriodKey: member.PeriodKey,
		})
	}
	m.Status = &struct {
		ID uuid.UUID `yaml:"id" json:"id"`
	}{ID: item.ID}
	return m
}
