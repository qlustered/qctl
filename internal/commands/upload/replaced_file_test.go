package upload

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// replacedFile is a replaced-file answer naming loaded file id.
func replacedFile(id int) map[string]any {
	return map[string]any{
		"period_key": "2026-08", "period_label": "Aug 2026", "replaced_stored_item_id": id,
		"replaced_file_name": "jul.csv", "replaced_loaded_at": "2026-11-13T10:00:00Z", "replaced_row_count": 260,
	}
}

func mirrorFixture() *deltaFixture {
	return &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil)}
}

// answerPrompts feeds lines to the confirmation prompts, one byte per read so
// each prompt's reader takes only its own line.
func answerPrompts(t *testing.T, lines ...string) {
	t.Helper()
	cmdutil.SetReader(iotest.OneByteReader(strings.NewReader(strings.Join(lines, "\n") + "\n")))
	t.Cleanup(cmdutil.ResetReader)
}

// replacesQueries returns each publish's replaces_stored_item_id, "" when absent.
func replacesQueries(f *deltaFixture) []string {
	got := make([]string, len(f.publishQueries))
	for i, q := range f.publishQueries {
		got[i] = q.Get("replaces_stored_item_id")
	}
	return got
}

func TestUploadFileReplacingNothingPublishesUnconfirmed(t *testing.T) {
	f := mirrorFixture()
	_, out, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "" {
		t.Errorf("publish replaces_stored_item_id = %q, want one publish without it", got)
	}
	if f.replacedReads != 1 {
		t.Errorf("replaced-file reads = %d, want 1", f.replacedReads)
	}
	if strings.Contains(out+stderr, "would replace") || strings.Contains(out, "replaces ") {
		t.Errorf("an upload replacing nothing mentions a replacement:\n%s%s", out, stderr)
	}
}

func TestUploadFileYesPublishesAReplacementUnconfirmed(t *testing.T) {
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812)}
	_, out, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "" {
		t.Errorf("publish replaces_stored_item_id = %q, want one publish without it", got)
	}
	for _, want := range []string{
		"This file would replace jul.csv (file/812, loaded ", ", 260 rows) in Aug 2026.",
		"preload_check_held alert", "upload with --replaces 812",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestUploadFileReplacesConfirmsTheNamedFile(t *testing.T) {
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812)}
	_, out, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "file/812", "-o", "json")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "812" {
		t.Errorf("publish replaces_stored_item_id = %q, want [812]", got)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v: %s", err, out)
	}
	if doc["replaces_stored_item_id"] != float64(812) {
		t.Errorf("replaces_stored_item_id = %v, want 812", doc["replaces_stored_item_id"])
	}
	if !strings.Contains(stderr, "replaces         : jul.csv (file/812, ") || strings.Contains(stderr, "Note:") {
		t.Errorf("summary does not name the confirmed replacement, or adds a note:\n%s", stderr)
	}
}

func TestUploadFileReplacesAnotherFileIsRefused(t *testing.T) {
	f := mirrorFixture()
	f.replaced = []any{replacedFile(813)}
	mock, _, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "812")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2: %v", got, err)
	}
	for _, want := range []string{"--replaces 812 is not the loaded file", "jul.csv (file/813)", "--replaces 813", "Unpublished file record 99 was removed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if len(f.publishQueries) != 0 || countRequests(mock, isZombieCleanup) != 1 {
		t.Errorf("publishes = %d, cleanups = %d; want 0 and 1", len(f.publishQueries), countRequests(mock, isZombieCleanup))
	}
}

func TestUploadFileReplacesWithNothingReplacedPublishesUnconfirmed(t *testing.T) {
	f := mirrorFixture()
	_, out, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "812")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "" {
		t.Errorf("publish replaces_stored_item_id = %q, want one publish without it", got)
	}
	if !strings.Contains(out, "replaces no loaded file, so --replaces 812 is not sent") {
		t.Errorf("output does not say --replaces was dropped:\n%s", out)
	}
}

// A failed read leaves --replaces for the server to check at publish.
func TestUploadFileReplacesSurvivesAFailedRead(t *testing.T) {
	f := mirrorFixture()
	f.replacedCode = http.StatusInternalServerError
	_, out, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "812")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "812" {
		t.Errorf("publish replaces_stored_item_id = %q, want [812]", got)
	}
	if !strings.Contains(out, "Warning: could not read which loaded file this upload replaces") ||
		!strings.Contains(out, "replaces         : file/812") {
		t.Errorf("output lacks the warning or the replaces line:\n%s", out)
	}
}

func TestUploadFileReplacesRefusesDeltaBeforeAnyRequest(t *testing.T) {
	f := mirrorFixture()
	mock, _, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7",
		"--delta", "--delta-cursor", "5", "--replaces", "812")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2: %v", got, err)
	}
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("requests = %d, want 0: %v", n, mock.Requests())
	}
}

func TestUploadFilePromptConfirmsAReplacement(t *testing.T) {
	answerPrompts(t, "y", "y")
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812)}
	_, _, stderr, err := runUploadCommand(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := replacesQueries(f); len(got) != 1 || got[0] != "812" {
		t.Errorf("publish replaces_stored_item_id = %q, want [812]", got)
	}
	if !strings.Contains(stderr, "Replace Aug 2026's data with this file? [y/N]") {
		t.Errorf("stderr lacks the replacement prompt:\n%s", stderr)
	}
}

func TestUploadFilePromptDeclinedRemovesTheRecord(t *testing.T) {
	answerPrompts(t, "y", "n")
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812)}
	mock, out, _, err := runUploadCommand(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1: %v", got, err)
	}
	if !strings.Contains(err.Error(), "upload cancelled") || !strings.Contains(err.Error(), "Unpublished file record 99 was removed") {
		t.Errorf("error = %v", err)
	}
	if len(f.publishQueries) != 0 || countRequests(mock, isZombieCleanup) != 1 {
		t.Errorf("publishes = %d, cleanups = %d; want 0 and 1", len(f.publishQueries), countRequests(mock, isZombieCleanup))
	}
	if strings.Contains(out, "queued for ingestion") {
		t.Errorf("declined upload claims success:\n%s", out)
	}
}

// A REPLACED_FILE_CHANGED refusal reads the replaced file again and asks
// about the one loaded now.
func TestUploadFilePromptAsksAgainWhenTheReplacedFileChanged(t *testing.T) {
	answerPrompts(t, "y", "y", "y")
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812), replacedFile(813)}
	f.publishCodes = []int{http.StatusConflict}
	_, out, stderr, err := runUploadCommand(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := strings.Join(replacesQueries(f), ","); got != "812,813" {
		t.Errorf("publish replaces_stored_item_id = %s, want 812,813", got)
	}
	if !strings.Contains(out, "Aug 2026 has a different loaded file now.") ||
		strings.Count(stderr, "Replace Aug 2026's data with this file?") != 2 {
		t.Errorf("second question missing:\nstdout:\n%s\nstderr:\n%s", out, stderr)
	}
}

func TestUploadFileReplacesRefusedWhenTheReplacedFileChanged(t *testing.T) {
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812), replacedFile(813)}
	f.publishCodes = []int{http.StatusConflict}
	_, _, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "812")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2: %v", got, err)
	}
	if got := strings.Join(replacesQueries(f), ","); got != "812" || !strings.Contains(err.Error(), "file/813") {
		t.Errorf("publishes = %s, err = %v; want one publish and a refusal naming file/813", got, err)
	}
}

// A server that keeps refusing the file it reports stops after
// maxReplacementPublishes publishes and keeps the record.
func TestUploadFileReplacementPublishesAreBounded(t *testing.T) {
	f := mirrorFixture()
	f.replaced = []any{replacedFile(812)}
	f.publishCodes = []int{http.StatusConflict, http.StatusConflict, http.StatusConflict}
	mock, _, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--replaces", "812")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2: %v", got, err)
	}
	if n := len(f.publishQueries); n != maxReplacementPublishes {
		t.Errorf("publishes = %d, want %d", n, maxReplacementPublishes)
	}
	if !strings.Contains(err.Error(), "File record 99 was kept") || countRequests(mock, isZombieCleanup) != 0 {
		t.Errorf("record not kept: %v", err)
	}
}
