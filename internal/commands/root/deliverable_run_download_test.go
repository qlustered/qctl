package root

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const runDownloadBody = "PK\x03\x04 rendered workbook bytes\n"

func resetRunDownloadFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"download", "deliverable-run"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"output", "force"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

func runDownloadRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetRunDownloadFlags(t)
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetArgs(append([]string{"download", "deliverable-run"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
	})
	err := Execute()
	return out.String(), stderr.String(), err
}

type runDownloadServer struct {
	*deliverableTestServer
	transfers atomic.Int32
	filename  string
	state     string
	signature any
	method    string
}

func newRunDownloadServer(t *testing.T) *runDownloadServer {
	t.Helper()
	sum := sha256.Sum256([]byte(runDownloadBody))
	s := &runDownloadServer{
		deliverableTestServer: newDeliverableTestServer(t),
		filename:              "bordereau-2026-09-DRAFT.xlsx",
		state:                 "rendered",
		signature:             hex.EncodeToString(sum[:]),
		method:                http.MethodGet,
	}
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.transfers.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Errorf("object request = %s, Authorization = %q", r.Method, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Disposition", `attachment; filename="3.xlsx"`)
		_, _ = w.Write([]byte(runDownloadBody))
	}))
	t.Cleanup(object.Close)
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		var run map[string]any
		if err := json.Unmarshal([]byte(liveDeliverableRun), &run); err != nil {
			t.Fatal(err)
		}
		run["state"] = s.state
		run["rendered_file_signature"] = s.signature
		testutil.RespondJSON(w, http.StatusOK, run)
	})
	s.RegisterHandler("GET", runTestPath+"41/download-url", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		mint := map[string]any{
			"run_id": 41, "filename": s.filename, "url": object.URL,
			"is_draft": s.state != "signed_off" && s.state != "delivered", "run_state": s.state,
		}
		if s.method != "" {
			mint["http_method"] = s.method
		}
		testutil.RespondJSON(w, http.StatusOK, mint)
	})
	return s
}

func assertRunDownloadFile(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != runDownloadBody {
		t.Fatalf("downloaded bytes = %q; error = %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file permissions = %o, want 600", got)
	}
}

func TestDownloadDeliverableRunVerifiesBytesAndUsesMintFilename(t *testing.T) {
	for _, state := range []string{"rendered", "stale", "signed_off", "delivered", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			s := newRunDownloadServer(t)
			s.state = state
			draft := state != "signed_off" && state != "delivered"
			if !draft {
				s.filename = "bordereau-2026-09.xlsm"
			}
			dir := t.TempDir()
			t.Chdir(dir)
			out, stderr, err := runDownloadRun(t, "deliverable-run/41")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, s.filename)
			assertRunDownloadFile(t, path)
			if out != "Successfully downloaded file to: "+path+"\n" {
				t.Errorf("stdout = %q", out)
			}
			if strings.Contains(stderr, "DRAFT (state:") != draft {
				t.Errorf("draft = %v, stderr = %q", draft, stderr)
			}
			want := []string{"GET " + runTestPath + "41", "GET " + runTestPath + "41/download-url"}
			if got := s.Requests(); !reflect.DeepEqual(got, want) || s.transfers.Load() != 1 {
				t.Errorf("API requests = %v, want %v; transfers = %d", got, want, s.transfers.Load())
			}
		})
	}
}

func TestDownloadDeliverableRunDestination(t *testing.T) {
	for _, tc := range []struct {
		name, filename, output, want string
	}{
		{"server traversal", "../../elsewhere/report.xlsx", "", "report.xlsx"},
		{"absolute server name", "/elsewhere/report.xlsx", "", "report.xlsx"},
		{"custom path", "..", "chosen/report.xlsx", "chosen/report.xlsx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunDownloadServer(t)
			s.filename, s.method = tc.filename, ""
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.Mkdir("chosen", 0o700); err != nil {
				t.Fatal(err)
			}
			args := []string{"41"}
			if tc.output != "" {
				args = append(args, "--output", tc.output)
			}
			if _, _, err := runDownloadRun(t, args...); err != nil {
				t.Fatal(err)
			}
			assertRunDownloadFile(t, filepath.Join(dir, tc.want))
		})
	}
}

func TestDownloadDeliverableRunExistingDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
		force    bool
	}{
		{"explicit collision before network", true, false},
		{"default collision before transfer", false, false},
		{"overwrite", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunDownloadServer(t)
			dir := t.TempDir()
			t.Chdir(dir)
			path := filepath.Join(dir, s.filename)
			if err := os.WriteFile(path, []byte("previous file"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"41"}
			if tc.explicit {
				args = append(args, "--output", path)
			}
			if tc.force {
				args = append(args, "--force")
			}
			out, _, err := runDownloadRun(t, args...)
			if tc.force {
				if err != nil {
					t.Fatal(err)
				}
				assertRunDownloadFile(t, path)
				return
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || out != "" {
				t.Fatalf("exit = %d, stdout = %q; %v", code, out, err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != "previous file" || s.transfers.Load() != 0 {
				t.Errorf("previous file = %q; read error = %v; transfers = %d", got, readErr, s.transfers.Load())
			}
			if tc.explicit && len(s.Requests()) != 0 {
				t.Errorf("preflight made requests: %v", s.Requests())
			}
		})
	}
}

func TestDownloadDeliverableRunRejectsUnverifiedFile(t *testing.T) {
	for _, tc := range []struct {
		name      string
		signature any
		transfer  int32
	}{
		{"mismatch", strings.Repeat("00", sha256.Size), 1},
		{"mismatch overwriting", strings.Repeat("00", sha256.Size), 1},
		{"missing", nil, 0},
		{"empty", "", 0},
		{"malformed", strings.Repeat("z", sha256.Size*2), 0},
		{"short", "abcd", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunDownloadServer(t)
			s.signature = tc.signature
			path := filepath.Join(t.TempDir(), "report.xlsx")
			args := []string{"41", "--output", path}
			if tc.name == "mismatch overwriting" {
				if err := os.WriteFile(path, []byte("old workbook"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--force")
			}
			out, _, err := runDownloadRun(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError || out != "" {
				t.Fatalf("exit = %d, stdout = %q; %v", code, out, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("unverified file remains: %v", err)
			}
			if got := s.transfers.Load(); got != tc.transfer {
				t.Errorf("transfers = %d, want %d", got, tc.transfer)
			}
			if tc.transfer == 1 && !strings.Contains(err.Error(), "SHA-256") {
				t.Errorf("error = %v", err)
			}
		})
	}
}

func TestDownloadDeliverableRunHTTPExits(t *testing.T) {
	for _, endpoint := range []string{"", "/download-url"} {
		for _, tc := range []struct{ status, exit int }{{403, 4}, {404, 3}, {409, 2}, {422, 2}, {500, 1}} {
			t.Run(endpoint+"/"+http.StatusText(tc.status), func(t *testing.T) {
				s := newRunDownloadServer(t)
				s.RegisterHandler("GET", runTestPath+"41"+endpoint, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, json.RawMessage(`{"detail":{"msg":"Download refused.","error_code":"REFUSED"}}`))
				})
				out, _, err := runDownloadRun(t, "41", "--output", filepath.Join(t.TempDir(), "report.xlsx"))
				if code := qerrors.GetExitCode(err); code != tc.exit || out != "" {
					t.Fatalf("exit = %d, want %d; stdout = %q; %v", code, tc.exit, out, err)
				}
				if !strings.Contains(err.Error(), "Download refused.") || s.transfers.Load() != 0 {
					t.Errorf("error = %v; transfers = %d", err, s.transfers.Load())
				}
			})
		}
	}
}

func TestDownloadDeliverableRunTransferFailureKeepsExistingFile(t *testing.T) {
	s := newRunDownloadServer(t)
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Expired download URL"))
	}))
	t.Cleanup(object.Close)
	s.RegisterHandler("GET", runTestPath+"41/download-url", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"run_id": 41, "filename": "report.xlsx", "url": object.URL,
			"is_draft": false, "run_state": "delivered",
		})
	})
	path := filepath.Join(t.TempDir(), "report.xlsx")
	if err := os.WriteFile(path, []byte("previous file"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runDownloadRun(t, "41", "--output", path, "--force")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError || out != "" {
		t.Fatalf("exit = %d; stdout = %q; %v", code, out, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "previous file" {
		t.Errorf("previous file = %q; %v", got, readErr)
	}
}

func TestDownloadDeliverableRunArgumentsFailBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{nil, {"0"}, {"abc"}, {"4.2"}, {"deliverable-run/0"}, {"table/41"}, {"41", "42"}, {"--", "-1"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newRunDownloadServer(t)
			_, _, err := runDownloadRun(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || len(s.Requests()) != 0 {
				t.Fatalf("exit = %d; requests = %v; %v", code, s.Requests(), err)
			}
		})
	}
}
