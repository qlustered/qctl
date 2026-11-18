package cmdutil

import (
	"errors"
	"strings"
	"testing"
)

// TestDeletedColumnRuleListFailure: the line keeps the first line of the error only,
// shows its control bytes as visible escapes, and ends in a blank line.
func TestDeletedColumnRuleListFailure(t *testing.T) {
	got := schemaColumnRuleListFailure(errors.New("request failed: cannot reach http://rules\x1b[2J: refused\nRead-only commands accept --retries N"), []string{"amount"}, nil)
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("the line carries a raw ESC: %q", got)
	}
	if !strings.HasSuffix(got, `http://rules\x1b[2J: refused`+"\n\n") {
		t.Errorf("the line does not end in the escaped first line of the error and a blank line: %q", got)
	}
	if strings.Contains(got, "--retries") {
		t.Errorf("the line keeps more than the error's first line: %q", got)
	}
}
