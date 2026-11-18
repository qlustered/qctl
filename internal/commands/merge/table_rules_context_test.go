package merge

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestMergeWaitCancellationPreservesTheLastJSONDocument(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "SIGTERM"}[interrupt], func(t *testing.T) {
			e := newMergeEnv(t)
			lastBody := testMergeBody("applying", "null", testWaitingItems())
			polls := 0
			e.mock.RegisterHandler("GET", testMergePath, func(w http.ResponseWriter, r *http.Request) {
				polls++
				if polls == 2 {
					if interrupt {
						p, err := os.FindProcess(os.Getpid())
						if err != nil {
							t.Fatal(err)
						}
						if err := p.Signal(syscall.SIGTERM); err != nil {
							t.Fatal(err)
						}
					}
					testutil.WaitForRequestCancellation(t, r)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, testMergeBody("applied", `"too late"`, testWaitingItems()))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, lastBody)
			})
			stdout, _, err := runMerge(t, testFullResolution, "orders", "--from", "orders_eu", "-f", "-", "--yes", "--wait", "--wait-timeout", "1", "-o", "json")
			want := qerrors.ExitTimeout
			if interrupt {
				want = 143
			} else if !errors.Is(err, rule_merges.ErrWaitTimeout) {
				t.Errorf("error = %v, want merge wait timeout", err)
			}
			if code := qerrors.GetExitCode(err); code != want {
				t.Fatalf("exit = %d (%v), want %d", code, err, want)
			}
			if !json.Valid([]byte(stdout)) {
				t.Fatalf("stdout contains an incomplete JSON document: %q", stdout)
			}
			if strings.TrimSpace(stdout) != strings.TrimSpace(lastBody) {
				t.Errorf("stdout = %s, want the last uncancelled poll %s", stdout, lastBody)
			}
		})
	}
}
