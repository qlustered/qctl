package slack_config //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
)

// Change is what to do to one alert type's entry. A nil field is left as it
// is. Reset drops the entry and ignores the other fields.
type Change struct {
	Enabled    *FieldValue[bool]
	DelayHours *FieldValue[int]
	Reset      bool
}

// FieldValue is a new value for one entry field; Default clears the field.
type FieldValue[T any] struct {
	Value   T
	Default bool
}

// Apply returns entry with change applied, and false when the result sets
// neither field.
func (change Change) Apply(entry *AlertTypeEntry) (AlertTypeEntry, bool) {
	if change.Reset {
		return AlertTypeEntry{}, false
	}
	var out AlertTypeEntry
	if entry != nil {
		out = *entry
	}
	if change.Enabled != nil {
		out.Enabled = fieldPointer(change.Enabled)
	}
	if change.DelayHours != nil {
		out.DelayHours = fieldPointer(change.DelayHours)
	}
	return out, out.Enabled != nil || out.DelayHours != nil
}

func fieldPointer[T any](field *FieldValue[T]) *T {
	if field.Default {
		return nil
	}
	value := field.Value
	return &value
}

// WithAlertTypes returns a copy of config with change applied to the
// alert_types entry of each of alertTypes, and whether any entry changed.
func WithAlertTypes(config Config, alertTypes []string, change Change) (Config, bool) {
	entries := maps.Clone(config.AlertUnattended.AlertTypes)
	if entries == nil {
		entries = map[string]AlertTypeEntry{}
	}
	changed := false
	for _, alertType := range alertTypes {
		before, had := entries[alertType]
		var current *AlertTypeEntry
		if had {
			current = &before
		}
		after, keep := change.Apply(current)
		switch {
		case keep && (!had || Describe(&before) != Describe(&after)):
			entries[alertType] = after
			changed = true
		case !keep && had:
			delete(entries, alertType)
			changed = true
		}
	}
	config.AlertUnattended.AlertTypes = entries
	return config, changed
}

// Entry returns alertType's entry in config, or nil when it has none.
func Entry(config *Config, alertType string) *AlertTypeEntry {
	entry, ok := config.AlertUnattended.AlertTypes[alertType]
	if !ok {
		return nil
	}
	return &entry
}

// Describe renders an entry as "enabled <on|off|default>, delay <N>h|default";
// a nil entry has both fields at their default.
func Describe(entry *AlertTypeEntry) string {
	if entry == nil {
		entry = &AlertTypeEntry{}
	}
	return fmt.Sprintf("enabled %s, delay %s", EnabledText(entry.Enabled), DelayText(entry.DelayHours))
}

// EnabledText renders an entry's enabled field: on, off, or default.
func EnabledText(enabled *bool) string {
	switch {
	case enabled == nil:
		return "default"
	case *enabled:
		return "on"
	default:
		return "off"
	}
}

// DelayText renders a delay in hours as "<N>h", and nil as default.
func DelayText(hours *int) string {
	if hours == nil {
		return "default"
	}
	return strconv.Itoa(*hours) + "h"
}

// SortedAlertTypes returns the alert types config has entries for, sorted.
func SortedAlertTypes(config *Config) []string {
	out := make([]string, 0, len(config.AlertUnattended.AlertTypes))
	for alertType := range config.AlertUnattended.AlertTypes {
		out = append(out, alertType)
	}
	sort.Strings(out)
	return out
}
