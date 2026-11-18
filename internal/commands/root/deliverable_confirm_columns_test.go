package root

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/pflag"
)

const (
	confirmColumnsPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-sheet-bindings/" + suggestionsSheetID + "/column-bindings/"
	// The draft's Alpha sheet binds AA (run parameter period, flagged) and B
	// (column premium, format 0.00, not flagged): revisionTreeRecord.
	confirmAAID = "20000000-0000-4000-8000-000000000001"
	confirmBID  = "20000000-0000-4000-8000-000000000002"

	confirmedAARecord = `{"id":"` + confirmAAID + `","target_column_index":27,"source_type":"run_param","source_column":null,` +
		`"run_param":"period","literal_value":null,"format_override":null,"origin":"user","target_label":"Period","needs_confirmation":false,"warnings":[]}`
	confirmedBRecord = `{"id":"` + confirmBID + `","target_column_index":2,"source_type":"column","source_column":"premium",` +
		`"run_param":null,"literal_value":null,"format_override":"0.00","origin":"user","target_label":"Premium","needs_confirmation":false,"warnings":[]}`
	storedBRecord = `{"id":"` + confirmBID + `","target_column_index":2,"source_type":"column","source_column":"premium",` +
		`"run_param":null,"literal_value":null,"format_override":"0.00","origin":"suggested","target_label":"Premium","needs_confirmation":false,"warnings":[]}`

	confirmAABody = `{"format_override":null,"run_param":"period","source_column":null,"source_type":"run_param","target_column_index":27}`
	confirmBBody  = `{"format_override":"0.00","run_param":null,"source_column":"premium","source_type":"column","target_column_index":2}`
)

func resetConfirmColumnsFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable", "confirm-columns"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "sheet", "column"} {
			flag := cmd.Flags().Lookup(name)
			if slice, ok := flag.Value.(pflag.SliceValue); ok {
				_ = slice.Replace(nil)
			} else if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// confirmColumnsServer serves the deliverable fixtures and answers a PATCH of
// each Alpha column binding with responses[id] at statuses[id] (default 200),
// recording each body by id.
func confirmColumnsServer(t *testing.T, responses map[string]string, statuses map[string]int) (*deliverableTestServer, map[string]string) {
	t.Helper()
	resetConfirmColumnsFlags(t)
	s := revisionTestServer(t)
	bodies := map[string]string{}
	for _, id := range []string{confirmAAID, confirmBID} {
		s.RegisterHandler("PATCH", confirmColumnsPath+id, func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			sent, _ := io.ReadAll(r.Body)
			bodies[id] = string(sent)
			status := http.StatusOK
			if code, ok := statuses[id]; ok {
				status = code
			}
			testutil.RespondJSON(w, status, json.RawMessage(responses[id]))
		})
	}
	return s, bodies
}

func runConfirmColumns(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"deliverable", "confirm-columns"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), errOut.String(), err
}

func confirmPatches(s *deliverableTestServer) []string {
	var patches []string
	for _, request := range s.Requests() {
		if strings.HasPrefix(request, "PATCH ") || strings.HasPrefix(request, "POST ") || strings.HasPrefix(request, "DELETE ") {
			patches = append(patches, request)
		}
	}
	return patches
}

func TestDeliverableConfirmColumnsFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s, bodies := confirmColumnsServer(t, map[string]string{confirmAAID: confirmedAARecord}, nil)
			out, errOut, err := runConfirmColumns(t, "monthly-bordereau", "--sheet", "Alpha", "--column", "AA,b", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			wantRequests := []string{
				deliverableRequest(), deliverableRequest(liveDeliverableID, "revisions"),
				"GET " + revisionTestPath + draftRevisionID, "PATCH " + confirmColumnsPath + confirmAAID,
			}
			if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
			if got := bodies[confirmAAID]; got != confirmAABody {
				t.Errorf("AA body = %s, want %s", got, confirmAABody)
			}
			report := errOut
			wantJSON := "[" + storedBRecord + "," + confirmedAARecord + "]"
			switch format {
			case "json":
				assertSeriesJSON(t, out, wantJSON)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, wantJSON)
			default:
				report = out
				if errOut != "" {
					t.Errorf("stderr = %q", errOut)
				}
			}
			want := "deliverable/" + liveDeliverableID + ` release "2026.2" sheet "Alpha"` + "\n" +
				"  Column 2 (B) <- column premium: already confirmed\n" +
				"  Column 27 (AA) <- run parameter period: confirmed\n"
			if report != want {
				t.Errorf("report = %q, want %q", report, want)
			}
		})
	}
}

func TestDeliverableConfirmColumnsSendsNothingForAnUnflaggedColumn(t *testing.T) {
	s, _ := confirmColumnsServer(t, nil, nil)
	out, _, err := runConfirmColumns(t, liveDeliverableID, "--sheet", "Alpha", "--column", "2")
	if err != nil {
		t.Fatal(err)
	}
	if got := confirmPatches(s); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
	if !strings.HasSuffix(out, "  Column 2 (B) <- column premium: already confirmed\n") || strings.Contains(out, "AA") {
		t.Errorf("report = %q, want B alone, already confirmed", out)
	}
}

func TestDeliverableConfirmColumnsSelectors(t *testing.T) {
	for _, tc := range []struct {
		name, ref string
		flags     []string
	}{
		{"sole draft", liveDeliverableID, nil},
		{"release", "Monthly bordereau", []string{"--release", "2026.2"}},
		{"uuid release", liveDeliverableID, []string{"--release", draftRevisionID}},
		{"revision ref", "deliverable/" + liveDeliverableID, []string{"--revision", "deliverable-revision/" + draftRevisionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := confirmColumnsServer(t, map[string]string{confirmAAID: confirmedAARecord}, nil)
			_, _, err := runConfirmColumns(t, append([]string{tc.ref, "--sheet", "Alpha", "--column", "AA"}, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			requests := s.Requests()
			if n := len(requests); n < 3 || requests[n-2] != "GET "+revisionTestPath+draftRevisionID || requests[n-1] != "PATCH "+confirmColumnsPath+confirmAAID {
				t.Errorf("requests = %v", requests)
			}
		})
	}
}

// Every flag refusal runs before Bootstrap: with no context configured, one
// that ran after it would exit 1 instead.
func TestDeliverableConfirmColumnsLocalValidation(t *testing.T) {
	sheet := []string{liveDeliverableID, "--sheet", "Alpha"}
	for _, args := range [][]string{
		nil,
		{""},
		{"one", "two"},
		{liveDeliverableID, "--column", "C"},
		{liveDeliverableID, "--sheet", "", "--column", "C"},
		sheet,
		append(sheet[:3:3], "--column", ""),
		append(sheet[:3:3], "--column", "C,"),
		append(sheet[:3:3], "--column", "C,3"),
		append(sheet[:3:3], "--column", "aa", "--column", "AA"),
		append(sheet[:3:3], "--column", "0"),
		append(sheet[:3:3], "--column", "XFE"),
		append(sheet[:3:3], "--column", "C1"),
		append(sheet[:3:3], "--column", "C", "--release", ""),
		append(sheet[:3:3], "--column", "C", "--revision", ""),
		append(sheet[:3:3], "--column", "C", "--revision", "bad"),
		append(sheet[:3:3], "--column", "C", "--release", "2026.2", "--revision", draftRevisionID),
		append(sheet[:3:3], "--column", "C", "-o", "name"),
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetConfirmColumnsFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"deliverable", "confirm-columns"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}

func TestDeliverableConfirmColumnsRefusesWrongTargets(t *testing.T) {
	for _, tc := range []struct {
		name, sheet, column, detail string
		flags                       []string
		code                        int
		message                     string
	}{
		{name: "enabled", flags: []string{"--release", "2026.1"}, code: 2, message: "can only be confirmed on a draft"},
		{name: "enabled after listing", detail: revisionTreeRecord, code: 2, message: "can only be confirmed on a draft"},
		{name: "missing sheet", sheet: "missing", code: 3, message: "no sheet binding matches"},
		{name: "unbound column", column: "C,AA,D", code: 2, message: `columns C, D are not bound on sheet "Alpha" (bound: B, AA)`},
		{
			name: "parent changed", detail: strings.ReplaceAll(revisionTreeRecord, liveDeliverableID, trashedDeliverableID),
			code: 3, message: "does not belong",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := confirmColumnsServer(t, map[string]string{confirmAAID: confirmedAARecord}, nil)
			if tc.detail != "" {
				s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.detail))
				})
			}
			sheet, column := tc.sheet, tc.column
			if sheet == "" {
				sheet = "Alpha"
			}
			if column == "" {
				column = "AA"
			}
			_, _, err := runConfirmColumns(t, append([]string{liveDeliverableID, "--sheet", sheet, "--column", column}, tc.flags...)...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			if got := confirmPatches(s); len(got) != 0 {
				t.Errorf("refused target sent writes: %v", got)
			}
		})
	}
}

func TestDeliverableConfirmColumnsPartialFailure(t *testing.T) {
	bothFlagged := strings.ReplaceAll(revisionTreeRecord,
		`"origin":"suggested","target_label":"Premium","needs_confirmation":false`, `"origin":"suggested","target_label":"Premium","needs_confirmation":true`)
	if bothFlagged == revisionTreeRecord {
		t.Fatal("fixture did not flag B")
	}
	bothFlagged = strings.ReplaceAll(bothFlagged, enabledRevisionID, draftRevisionID)
	bothFlagged = strings.ReplaceAll(bothFlagged, `"release":"2026.1","state":"enabled"`, `"release":"2026.2","state":"draft"`)
	for _, tc := range []struct {
		name   string
		status int
		code   int
	}{
		{"409", http.StatusConflict, qerrors.ExitBadUsage},
		{"404", http.StatusNotFound, qerrors.ExitNotFound},
		{"500", http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bodies := confirmColumnsServer(t,
				map[string]string{confirmBID: confirmedBRecord, confirmAAID: `{"detail":{"msg":"Revision is no longer draft"}}`},
				map[string]int{confirmAAID: tc.status})
			s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(bothFlagged))
			})
			out, _, err := runConfirmColumns(t, liveDeliverableID, "--sheet", "Alpha", "--column", "AA", "--column", "B", "-o", "json")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), "confirm column AA: ") || !strings.HasSuffix(err.Error(), "\n\nConfirmed before the failure: B") {
				t.Errorf("error = %q, want it to name AA and that B was confirmed", err)
			}
			want := []string{"PATCH " + confirmColumnsPath + confirmBID, "PATCH " + confirmColumnsPath + confirmAAID}
			if got := confirmPatches(s); !reflect.DeepEqual(got, want) {
				t.Errorf("writes = %v, want %v (column order)", got, want)
			}
			if got := bodies[confirmBID]; got != confirmBBody {
				t.Errorf("B body = %s, want %s", got, confirmBBody)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing after a failure", out)
			}
		})
	}
}
