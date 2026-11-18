package rows

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestFileScopeRejectsAnotherTablesFileBeforeConfirmation(t *testing.T) {
	for _, verb := range []string{"delete-quarantined"} {
		for _, tableArgs := range [][]string{{"orders"}, {"42"}, {"--table-id", "42"}} {
			for _, yes := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/yes=%t", verb, strings.Join(tableArgs, "_"), yes), func(t *testing.T) {
					mock := newRowsTestEnv(t)
					read := record(mock, "GET", "/api/orgs/"+testOrgID+"/stored-items/3", `{"id":3,"dataset_id":99}`)
					write := record(mock, "POST", "/api/orgs/"+testOrgID+"/stored-items/3/delete-rows", `{"result":"ok"}`)
					args := append([]string{"rows", verb, "--reason", testReason}, tableArgs...)
					args = append(args, "--file-id", "3", "--signature", "deadbeef")
					if yes {
						args = append(args, "--yes")
					}
					out, stderr, err := runRows(t, args...)
					if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "file 3 belongs to table 99, not selected table 42") {
						t.Fatalf("err = %v, want a file/table mismatch with exit 2", err)
					}
					if read.hits != 1 || write.hits != 0 || out != "" || stderr != "" {
						t.Fatalf("reads=%d writes=%d stdout=%q stderr=%q", read.hits, write.hits, out, stderr)
					}
				})
			}
		}
	}
}

func TestFileScopeMetadataFailuresStopDeletion(t *testing.T) {
	for _, verb := range []string{"delete-quarantined"} {
		for _, tc := range []struct {
			name, body string
			status     int
			code       int
		}{
			{"unauthorized", `{"detail":"denied"}`, 401, 4},
			{"forbidden", `{"detail":"denied"}`, 403, 4},
			{"missing", `{"detail":"missing"}`, 404, 3},
			{"server error", `{"detail":"unavailable"}`, 500, 1},
			{"malformed", `{`, 200, 1},
			{"missing table", `{"id":3}`, 200, 1},
			{"invalid table", `{"id":3,"dataset_id":-1}`, 200, 1},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/3", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				})
				write := record(mock, "POST", "/api/orgs/"+testOrgID+"/stored-items/3/delete-rows", `{"result":"ok"}`)
				out, stderr, err := runRows(t, "rows", verb, "--reason", testReason, "--table-id", "42", "--file-id", "3", "--signature", "deadbeef", "--yes")
				if qerrors.GetExitCode(err) != tc.code || !strings.Contains(err.Error(), "cannot verify table for file 3") {
					t.Fatalf("err = %v, want metadata failure with exit %d", err, tc.code)
				}
				if write.hits != 0 || len(mock.Requests()) != 1 || out != "" || stderr != "" {
					t.Fatalf("requests=%v writes=%d stdout=%q stderr=%q", mock.Requests(), write.hits, out, stderr)
				}
			})
		}
	}
}

const (
	sharedFileReadPath   = "/api/orgs/" + testOrgID + "/third-party/stored-items/3"
	sharedFileDeletePath = "/api/orgs/" + testOrgID + "/third-party/stored-items/3/delete-rows"
)

func TestSharedFileScopeRejectsAnotherTablesFileBeforeConfirmation(t *testing.T) {
	for _, verb := range []string{"delete-quarantined"} {
		for _, yes := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/yes=%t", verb, yes), func(t *testing.T) {
				mock := newRowsTestEnv(t)
				read := record(mock, "GET", sharedFileReadPath, `{"id":3,"dataset_id":99,"file_name":"a.csv","data_source_model_id":7,"created_at":"2026-11-01T00:00:00Z"}`)
				write := record(mock, "POST", sharedFileDeletePath, `{"result":"ok"}`)
				args := []string{"rows", verb, "--reason", testReason, "42", "--shared", "--file-id", "3", "--signature", "deadbeef"}
				if yes {
					args = append(args, "--yes")
				}
				out, stderr, err := runRows(t, args...)
				if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "file 3 belongs to table 99, not selected table 42") {
					t.Fatalf("err = %v, want a file/table mismatch with exit 2", err)
				}
				if read.hits != 1 || write.hits != 0 || len(mock.Requests()) != 1 || out != "" || stderr != "" {
					t.Fatalf("requests=%v writes=%d stdout=%q stderr=%q", mock.Requests(), write.hits, out, stderr)
				}
			})
		}
	}
}

func TestSharedFileScopeDeletesMatchingTablesFile(t *testing.T) {
	for _, tc := range []struct {
		verb, query, followUp string
	}{
		{"delete-quarantined", "dataset_id=42&is_bad_data=true", "  qctl get data 42 --shared --stored-item-id 3 --is-bad-data\n"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			read := record(mock, "GET", sharedFileReadPath, `{"id":3,"dataset_id":42,"file_name":"a.csv","data_source_model_id":7,"created_at":"2026-11-01T00:00:00Z"}`)
			write := record(mock, "POST", sharedFileDeletePath, `{"result":"ok"}`)
			out, stderr, err := runRows(t, "rows", tc.verb, "--reason", testReason, "42", "--shared", "--file-id", "3", "--signature", "deadbeef", "--yes")
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			if read.hits != 1 || write.hits != 1 || write.query != tc.query || write.body != testSignatureBody {
				t.Fatalf("reads=%d writes=%d query=%q body=%q", read.hits, write.hits, write.query, write.body)
			}
			for _, req := range mock.Requests() {
				if !strings.Contains(req, "/third-party/") {
					t.Fatalf("used a first-party route: %s", req)
				}
			}
			if !strings.Contains(out, tc.followUp) {
				t.Fatalf("stdout = %q, want follow-up %q", out, tc.followUp)
			}
		})
	}
}

func TestSharedFileScopeReadFailuresStopDeletion(t *testing.T) {
	for _, verb := range []string{"delete-quarantined"} {
		for _, tc := range []struct {
			name, body string
			status     int
			code       int
			hint       bool
		}{
			{"missing route or file", `{"detail":"Not Found"}`, 404, 3, true},
			{"unshared table", `{"detail":"User is not authorized to access third-party table 99"}`, 403, 4, false},
			{"own organization file", `{"detail":"own file"}`, 400, 2, false},
			{"server error", `{"detail":"unavailable"}`, 500, 1, false},
			{"malformed", `{`, 200, 1, false},
			{"missing table", `{"id":3}`, 200, 1, false},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				mock.RegisterHandler("GET", sharedFileReadPath, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				})
				write := record(mock, "POST", sharedFileDeletePath, `{"result":"ok"}`)
				out, stderr, err := runRows(t, "rows", verb, "--reason", testReason, "42", "--shared", "--file-id", "3", "--signature", "deadbeef", "--yes")
				if qerrors.GetExitCode(err) != tc.code || !strings.Contains(err.Error(), "cannot verify table for file 3") {
					t.Fatalf("err = %v, want read failure with exit %d", err, tc.code)
				}
				if got := strings.Contains(err.Error(), "shared file read"); got != tc.hint {
					t.Fatalf("err = %v, old-server hint present = %t, want %t", err, got, tc.hint)
				}
				if write.hits != 0 || len(mock.Requests()) != 1 || out != "" || stderr != "" {
					t.Fatalf("requests=%v writes=%d stdout=%q stderr=%q", mock.Requests(), write.hits, out, stderr)
				}
			})
		}
	}
}

// The file can move tables between the read and the delete; the server's
// refusal of the selected dataset_id then surfaces as the command's error.
func TestSharedFileScopeDeleteRefusalIsReported(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "GET", sharedFileReadPath, `{"id":3,"dataset_id":42,"file_name":"a.csv","data_source_model_id":7,"created_at":"2026-11-01T00:00:00Z"}`)
	mock.RegisterHandler("POST", sharedFileDeletePath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"detail":"File id 3 not found in table 42"}`)
	})
	out, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "42", "--shared", "--file-id", "3", "--signature", "deadbeef", "--yes")
	if qerrors.GetExitCode(err) != 3 || !strings.Contains(err.Error(), "File id 3 not found in table 42") {
		t.Fatalf("err = %v, want the server's 404 with exit 3", err)
	}
	if strings.Contains(out, "requested") {
		t.Fatalf("stdout = %q reports a delete the server refused", out)
	}
}
