package logs

import (
	"fmt"
	"strings"
	"time"
)

// LogsDateDelimiter matches the backend delimiter used to embed timestamps and levels.
const LogsDateDelimiter = "◭◘"

// Entry represents a single log line in structured form.
type Entry struct {
	Timestamp *time.Time `yaml:"timestamp,omitempty" json:"timestamp,omitempty"`
	Publisher string     `yaml:"publisher,omitempty" json:"publisher,omitempty"`
	Message   string     `yaml:"message" json:"message"`
}

// ParseRaw converts the API's msg_logs shape into structured entries.
func ParseRaw(rawLogs *[][]interface{}) []Entry {
	if rawLogs == nil {
		return nil
	}

	var entries []Entry
	for _, item := range *rawLogs {
		if len(item) == 0 {
			continue
		}

		// If the backend already split into parts, honor that structure.
		if len(item) >= 3 {
			ts := fmt.Sprint(item[0])
			publisher := fmt.Sprint(item[1])
			message := fmt.Sprint(item[2])
			entries = append(entries, entryFromParts(ts, publisher, message))
			continue
		}

		// Otherwise treat the first element as the combined log string.
		logStr := fmt.Sprint(item[0])
		if strings.TrimSpace(logStr) == "" {
			continue
		}
		entries = append(entries, SplitLogMsgByDelimiter(logStr))
	}

	return entries
}

// SplitLogMsgByDelimiter replicates the backend helper: parses a single delimited log string.
func SplitLogMsgByDelimiter(msg string) Entry {
	parts := strings.Split(msg, LogsDateDelimiter)

	// Pad or truncate to exactly 3 parts
	switch {
	case len(parts) < 3:
		padding := make([]string, 3-len(parts))
		parts = append(padding, parts...)
	case len(parts) > 3:
		parts = parts[:3]
	}

	timestampRaw, publisher, message := parts[0], parts[1], parts[2]
	return entryFromParts(timestampRaw, publisher, message)
}

func entryFromParts(timestampRaw, publisher, message string) Entry {
	timestampRaw = strings.TrimSpace(timestampRaw)
	publisher = strings.TrimSpace(publisher)
	message = strings.TrimSpace(message)

	return Entry{
		Timestamp: parseTimestamp(timestampRaw),
		Publisher: publisher,
		Message:   message,
	}
}

func parseTimestamp(raw string) *time.Time {
	if raw == "" {
		return nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}

	return nil
}
