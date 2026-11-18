package logs

import (
	"testing"
	"time"
)

func TestSplitLogMsgByDelimiter(t *testing.T) {
	entry := SplitLogMsgByDelimiter("2025-01-20T16:00:00Z◭◘INFO◭◘job started")

	if entry.Timestamp == nil {
		t.Fatalf("expected timestamp, got nil")
	}

	if got := entry.Timestamp.Format(time.RFC3339); got != "2025-01-20T16:00:00Z" {
		t.Errorf("unexpected timestamp: %s", got)
	}

	if entry.Publisher != "INFO" || entry.Message != "job started" {
		t.Errorf("unexpected entry: %+v", entry)
	}
}

func TestParseRaw(t *testing.T) {
	raw := [][]interface{}{
		{"2025-01-20T16:00:00Z◭◘INFO◭◘job started"},
		{"2025-01-20T16:05:00Z", "ERROR", "failed"},
	}

	entries := ParseRaw(&raw)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	first := entries[0]
	if first.Timestamp == nil || first.Timestamp.Format(time.RFC3339) != "2025-01-20T16:00:00Z" || first.Publisher != "INFO" || first.Message != "job started" {
		t.Errorf("unexpected first entry: %+v", first)
	}

	second := entries[1]
	if second.Timestamp == nil || second.Timestamp.Format(time.RFC3339) != "2025-01-20T16:05:00Z" || second.Publisher != "ERROR" || second.Message != "failed" {
		t.Errorf("unexpected second entry: %+v", second)
	}
}
