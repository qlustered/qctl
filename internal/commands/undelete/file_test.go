package undelete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupUndeleteFileTestCommand creates a root command with global flags and
// the undelete file command, following the pattern in docs/patterns.md.
func setupUndeleteFileTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "") // NOTE: CountP, not BoolP

	undeleteCmd := &cobra.Command{Use: "undelete"}
	undeleteCmd.AddCommand(NewFileCommand())
	rootCmd.AddCommand(undeleteCmd)
	return rootCmd
}

func TestUndeleteFileInvalidIDExitsTwo(t *testing.T) {
	cmd := NewFileCommand()
	cmd.SetArgs([]string{"not-a-number"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid ID to fail")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
}

// Flipping is_trashed back to false makes the server create and queue a fresh
// ingestion job (atlas StorageController.handle_file_ignore in
// storage_controller.py -> check_and_put_ingestion_job_on_queue,
// cettings/cettings/controllers/ingestion_controller.py), so help that tells
// the user to re-run ingestion by hand is false.
//
// Nothing else can catch this: catalog.Signature deliberately excludes Long and
// Short text (internal/catalog/signature.go), so page signatures never
// fire on help prose. This test is the whole gate.
func TestUndeleteFileHelpDoesNotClaimManualIngestion(t *testing.T) {
	for name, text := range map[string]string{
		"Long":        NewFileCommand().Long,
		"note":        undeleteIngestionNote,
		"successLine": undeleteSuccessLine,
	} {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "manually") {
			t.Errorf("%s still tells the user to re-run ingestion manually; the server queues the job itself:\n%s", name, text)
		}
		if strings.Contains(lower, "does not automatically restore") {
			t.Errorf("%s still claims undelete restores nothing:\n%s", name, text)
		}
	}
}

// The queued job can land blocked when the table has no current migration
// (the `if not model_schema:` branch of check_and_put_ingestion_job_on_queue,
// cettings/cettings/controllers/ingestion_controller.py), so the note must
// point somewhere the user can check rather than promise the rows back.
func TestUndeleteFileHelpPointsAtIngestionJobs(t *testing.T) {
	long := NewFileCommand().Long
	if !strings.Contains(long, "get ingestion-jobs") {
		t.Errorf("undelete file --help should point at `qctl get ingestion-jobs --table-id <table-id>`, got:\n%s", long)
	}
}

// The banner printed before the confirmation prompt and the line printed after
// success must render the same note as --help, not their own copies.
func TestUndeleteBannerAndSuccessUseTheSharedNote(t *testing.T) {
	if !strings.Contains(NewFileCommand().Long, undeleteIngestionNote) {
		t.Error("Long does not embed undeleteIngestionNote; the three copies can drift apart again")
	}
	if !strings.Contains(strings.ToLower(undeleteSuccessLine), "re-queued") {
		t.Errorf("success line should say ingestion was re-queued, got: %s", undeleteSuccessLine)
	}
}

// Calling undelete on a file that was never deleted is a genuine server
// no-op: atlas storage_controller.py's handle_file_ignore gates the entire
// body -- including check_and_put_ingestion_job_on_queue -- on
// `is_trashed != stored_item.is_trashed or force`. Nothing is queued, so
// qctl must not print the re-queue banner/success line for this case; it
// must tell the truth and return before ever promising a queued job.
func TestUndeleteFileNotDeleted_NoOp(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemFull{
			ID:        42,
			FileName:  "already-active.csv",
			IsTrashed: false,
		})
	})
	// Deliberately no handler registered for PATCH /stored-items or for
	// auth GetMe: if the command falls through to the confirmation banner
	// or the server call, the mock transport 404s and the test fails.

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupUndeleteFileTestCommand()
	rootCmd.SetArgs([]string{"undelete", "file", "42"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	err := rootCmd.Execute()
	output := buf.String()

	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "not deleted") {
		t.Errorf("expected output to explain the file is not deleted, got:\n%s", output)
	}

	lower := strings.ToLower(output)
	if strings.Contains(lower, "re-queued") {
		t.Errorf("no-op path must not claim ingestion was re-queued, got:\n%s", output)
	}
	if strings.Contains(output, undeleteSuccessLine) {
		t.Errorf("no-op path must not print the real undeleteSuccessLine, got:\n%s", output)
	}
}

// The undelete banner names the file, its table and its cloud source from the
// server's response, through the command writer.
func TestPrintUndeleteContextSanitizesServerText(t *testing.T) {
	file := &stored_items.StoredItemFull{
		ID: 7, FileName: "invoices\x1b[31m.csv", DatasetID: 42,
		DatasetName: "orders\x1b]0;pwned\x07", DataSourceModelID: 3,
		DataSourceModelName: "s3\x1b[2J",
	}

	var buf strings.Builder
	printUndeleteContext(&buf, "Acme\x1b[1m", "org\x1b[0m", file)
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{
		`invoices\x1b[31m.csv`,
		`orders\x1b]0;pwned\x07`,
		`s3\x1b[2J`,
		`Acme\x1b[1m`,
		`org\x1b[0m`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}
