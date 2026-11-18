package upload

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testUploadOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// The API can return a null cloud source name, and ResolveCloudSource passes
// that through as "" — printing it raw leaves a blank field in a prompt asking
// the user to confirm an upload.
func TestDisplayNameFallsBackForEmptyNames(t *testing.T) {
	if got := displayName("S3 Import"); got != "S3 Import" {
		t.Errorf("displayName(%q) = %q, want it unchanged", "S3 Import", got)
	}
	if got := displayName(""); got != "(unnamed)" {
		t.Errorf(`displayName("") = %q, want "(unnamed)"`, got)
	}
}

func setupUploadTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

// --table-id 0 reached ResolveTable, whose default branch answered "must
// specify either --table-id or --table" — to a user who had just specified it.
func TestUploadFileRejectsNonPositiveIDFlags(t *testing.T) {
	cases := []struct{ flag, value string }{
		{"--table-id", "0"},
		{"--table-id", "-1"},
		{"--cloud-source-id", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testUploadOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testUploadOrgID)
			env.SetupCredential(endpointKey, testUploadOrgID, "test-token")

			path := filepath.Join(t.TempDir(), "data.csv")
			if err := os.WriteFile(path, []byte("id,name\n1,a\n"), 0o644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			cmd := setupUploadTestCommand()
			cmd.SetArgs([]string{"upload", "file", path, tc.flag, tc.value, "--yes"})

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for %s %s", tc.flag, tc.value)
			}
			if !strings.Contains(err.Error(), tc.flag+" must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if strings.Contains(err.Error(), "must specify") {
				t.Errorf("error must not tell the user to pass the flag they just passed, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("expected no table lookup, got %d call(s)", calls)
			}
		})
	}
}

func TestUploadFileMultipleCloudSourcesExitTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testUploadOrgID)
	env.SetupCredential(endpointKey, testUploadOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testUploadOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"role": "admin"})
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testUploadOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 42, "name": "orders"})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testUploadOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 7, "name": "s3-day"},
				{"id": 8, "name": "s3-night"},
			},
		})
	})

	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, []byte("id,name\n1,a\n"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	cmd := setupUploadTestCommand()
	cmd.SetArgs([]string{"upload", "file", path, "--table-id", "42", "--yes"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "multiple cloud sources") {
		t.Fatalf("err = %v, want multiple-cloud-sources refusal", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The confirmation block is the last thing the user reads before agreeing to
// the upload, and every value on it but the local path is the API's: the org
// name, the table name and the cloud source name. The rendered text must make control characters visible before confirmation.
func TestDisplayUploadConfirmationSanitizesServerNames(t *testing.T) {
	uc := &uploadContext{
		filePath:        filepath.Join(t.TempDir(), "data.csv"),
		fileSize:        12,
		datasetID:       42,
		datasetName:     "orders\x1b[31m",
		cloudSourceID:   7,
		cloudSourceName: "S3\x1b]0;pwned\x07Import",
		orgID:           "550e8400-e29b-41d4-a716-446655440000\x00",
		orgName:         "Acme\x1b[2KCorp",
	}

	var buf strings.Builder
	displayUploadConfirmation(&buf, uc)
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07\x00") {
		t.Fatalf("the confirmation block reached the terminal with a live control byte: %q", out)
	}
	for _, want := range []string{
		`orders\x1b[31m`, `S3\x1b]0;pwned\x07Import`, `Acme\x1b[2KCorp`,
		`550e8400-e29b-41d4-a716-446655440000\x00`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the visible escape %q:\n%s", want, out)
		}
	}
}

// displayName is the one shared renderer for the cloud source name, so the pass
// lives in it rather than at its two call sites.
func TestDisplayNameSanitizes(t *testing.T) {
	if got := displayName("S3\x1b[31mImport"); got != `S3\x1b[31mImport` {
		t.Errorf("displayName did not make the escape visible: %q", got)
	}
}

// The success block names the ingestion job the publish queued, and the next
// step follows that job; a publish response with no job ID drops both lines.
func TestUploadFileSummaryNamesTheIngestionJob(t *testing.T) {
	cases := []struct {
		name        string
		publishBody interface{}
		want        string
	}{
		{
			name:        "queued",
			publishBody: map[string]interface{}{"ingestion_job_id": 555},
			want: "Uploaded file and queued for ingestion.\n" +
				"stored_item_id   : 99\n" +
				"ingestion_job_id : 555\n" +
				"table            : orders\n" +
				"cloud_source     : Airbyte CDC\n" +
				"Next: qctl describe ingestion-job 555\n",
		},
		{
			name:        "null id",
			publishBody: map[string]interface{}{"ingestion_job_id": nil},
			want: "Uploaded file and queued for ingestion.\n" +
				"stored_item_id   : 99\n" +
				"table            : orders\n" +
				"cloud_source     : Airbyte CDC\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, 1000),
				publishBody: tc.publishBody,
			}
			_, out, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
			if err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if !strings.HasSuffix(out, "\n\n"+tc.want) {
				t.Errorf("summary block:\n%s\nwant it to end with:\n%s", out, tc.want)
			}
		})
	}
}

// -o json prints the upload's ids and names, the confirmation block, progress
// and summary move to stderr, and -o name prints file/<stored-item-id>.
func TestUploadFileWritesTheUploadDocument(t *testing.T) {
	newFixture := func(publishBody interface{}, sourceName interface{}) *deltaFixture {
		source := deltaSource(deltaTableID, 1000)
		source["name"] = sourceName
		return &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: source, publishBody: publishBody}
	}

	_, stdout, stderr, err := runDeltaUploadSplit(t, newFixture(map[string]interface{}{"ingestion_job_id": 555}, "Airbyte CDC"),
		"--table-id", "42", "--cloud-source-id", "7", "-o", "json")
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	for _, want := range []string{"Organization", "Publishing for ingestion...", "Next: qctl describe ingestion-job 555"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	want := map[string]any{
		"stored_item_id": float64(99), "ingestion_job_id": float64(555), "table_id": float64(42),
		"table": "orders", "period_key": nil, "routing_pending": false, "replaces_stored_item_id": nil,
		"cloud_source_id": float64(7), "cloud_source": "Airbyte CDC",
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("document = %v, want %v", got, want)
	}

	// A null job id and an unnamed cloud source stay null rather than vanish.
	_, stdout, _, err = runDeltaUploadSplit(t, newFixture(map[string]interface{}{"ingestion_job_id": nil}, nil),
		"--table-id", "42", "--cloud-source-id", "7", "-o", "json")
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	got = nil
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if v, ok := got["ingestion_job_id"]; !ok || v != nil {
		t.Errorf("ingestion_job_id = %v (present %v), want null", v, ok)
	}
	if v, ok := got["cloud_source"]; !ok || v != nil {
		t.Errorf("cloud_source = %v (present %v), want null", v, ok)
	}

	_, stdout, _, err = runDeltaUploadSplit(t, newFixture(map[string]interface{}{"ingestion_job_id": 555}, "Airbyte CDC"),
		"--table-id", "42", "--cloud-source-id", "7", "-o", "name")
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if stdout != "file/99\n" {
		t.Errorf("-o name stdout = %q, want %q", stdout, "file/99\n")
	}
}
