//nolint:revive // Tests intentionally match the command package directory.
package queue_message

import (
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// retire, resurrect-dead and its --dry-list write the server's response under
// -o json and name each message under -o name; the prose moves to stderr.
func TestBulkQueueVerbsWriteTheResponse(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		idsKey string
		prose  string
	}{
		{"retire", []string{"retire", "--older-than", "90 days", "--yes"}, "retired_ids", "Retired 2"},
		{"resurrect-dead", []string{"resurrect-dead", "--yes"}, "resurrected_ids", "Resurrected 2"},
		{"resurrect-dead --dry-list", []string{"resurrect-dead", "--dry-list"}, "dead_ids", "2 candidate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{"json", "name"} {
				mock := newQueueMessageTestEnv(t)
				registerRetire(mock, []int{11, 12})
				registerResurrectBatch(mock, []int{11, 12})
				registerDeadList(mock, []int{11, 12})

				stdout, stderr, err := runQueueMessage(t, append(tc.args, "-o", format)...)
				if err != nil {
					t.Fatalf("-o %s: unexpected error: %v", format, err)
				}
				if !strings.Contains(stderr, tc.prose) {
					t.Errorf("-o %s: stderr missing %q:\n%s", format, tc.prose, stderr)
				}
				if format == "name" {
					if want := "queue-message/11\nqueue-message/12\n"; stdout != want {
						t.Errorf("-o name stdout = %q, want %q", stdout, want)
					}
					continue
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(stdout), &got); err != nil {
					t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
				}
				if ids, _ := got[tc.idsKey].([]any); len(ids) != 2 {
					t.Errorf("%s = %v, want [11 12]", tc.idsKey, got[tc.idsKey])
				}
			}
		})
	}
}

// retry and resurrect only repeat their input, so they declare table alone.
func TestSingleQueueVerbsRefuseJSON(t *testing.T) {
	newQueueMessageTestEnv(t)
	for _, verb := range []string{"retry", "resurrect"} {
		_, _, err := runQueueMessage(t, verb, "11", "-o", "json")
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s -o json: exit %d (%v), want %d", verb, code, err, qerrors.ExitBadUsage)
		}
	}
}
