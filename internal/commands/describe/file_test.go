package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupFileDescribeTestCommand creates a root command with global flags and adds the describe file command
func setupFileDescribeTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a specific resource",
	}
	describeCmd.AddCommand(NewFileCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleStoredItemFull() stored_items.StoredItemFull {
	csvDelimiter := ","
	csvEscape := "\\"
	csvQuote := "\""
	headerLine := 1
	firstDataLine := 2
	fileSize := 2048
	backupSettingsID := 9
	arrayDelims := map[string]interface{}{"tags": ";"}
	rawHeaders := []string{"col_a", "col_b"}
	ignoreColumns := []string{"ignore_me"}
	otherNames := []string{"legacy.csv"}
	deletedLineSigs := []string{"sig-1"}
	deletedRows := map[string]string{"10": "sig-10"}
	parentID := 7
	startsAt := 3
	signature := "abc123"
	duplicateOfID := 5
	cleanRows := 150
	badRows := 2
	deletedLines := 0
	isEverLoaded := true
	uploadedViaSignedURL := true
	backupEncrypted := false
	compression := api.CompressionType("gzip")
	fileType := stored_items.FileTypes("csv")
	fieldMap := map[string]string{"old": "new"}
	createdAt := time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC)

	return stored_items.StoredItemFull{
		ArrayDelimiterPerColumn:          &arrayDelims,
		BackupKey:                        "org/1/file.csv",
		BackupSettingsID:                 &backupSettingsID,
		BadRowsCount:                     &badRows,
		CleanRowsCount:                   &cleanRows,
		CompressionTypeOfBackupData:      &compression,
		CreatedAt:                        createdAt,
		CsvDelimiter:                     &csvDelimiter,
		CsvEscapechar:                    &csvEscape,
		CsvQuotechar:                     &csvQuote,
		DataSourceModelID:                22,
		DataSourceModelName:              "s3-bucket",
		DatasetID:                        11,
		DatasetName:                      "orders",
		ExcludedRowSignatures:            &deletedLineSigs,
		DeletedRowsLineNumberToSignature: &deletedRows,
		DuplicateOfID:                    &duplicateOfID,
		Encoding:                         "utf-8",
		ExcelSheetNameForFile:            "Sheet1",
		FieldNameFullConversion:          &fieldMap,
		FileName:                         "orders.csv",
		FileSize:                         &fileSize,
		FileType:                         &fileType,
		HeaderLineNumberForFile:          &headerLine,
		ID:                               1,
		IgnoreColumnNames:                &ignoreColumns,
		IsTrashed:                        false,
		DeletedLinesCount:                &deletedLines,
		IsBackupEncrypted:                &backupEncrypted,
		IsEverLoaded:                     &isEverLoaded,
		IsUploadedViaSignedURL:           &uploadedViaSignedURL,
		Key:                              "files/1/orders.csv",
		OtherNames:                       &otherNames,
		ParentID:                         &parentID,
		RawHeadersForFile:                &rawHeaders,
		RowNumberForFirstLineOfData:      &firstDataLine,
		Signature:                        &signature,
		StartsAtParentRowNumber:          &startsAt,
	}
}

func TestDescribeFileCommand_YAMLManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleStoredItemFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupFileDescribeTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "file", "1"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest stored_items.FileManifest
	if err := yaml.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.Kind != "File" {
		t.Fatalf("expected kind File, got %s", manifest.Kind)
	}
	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("expected apiVersion qluster.ai/v1, got %s", manifest.APIVersion)
	}
	if manifest.Metadata.Name != "orders.csv" {
		t.Errorf("expected metadata.name orders.csv, got %s", manifest.Metadata.Name)
	}
	if manifest.Metadata.Labels["dataset_id"] != "11" || manifest.Metadata.Labels["data_source_model_id"] != "22" {
		t.Errorf("metadata labels not set correctly: %#v", manifest.Metadata.Labels)
	}
	if manifest.Spec.CloudSourceName != "s3-bucket" {
		t.Errorf("expected cloud_source_name s3-bucket, got %s", manifest.Spec.CloudSourceName)
	}
	if manifest.Spec.ArrayDelimiterPerColumn == nil || (*manifest.Spec.ArrayDelimiterPerColumn)["tags"] != ";" {
		t.Errorf("expected array_delimiter_per_column.tags to be ';', got %#v", manifest.Spec.ArrayDelimiterPerColumn)
	}
	if manifest.Status == nil {
		t.Fatal("expected status to be present")
	}
	if manifest.Status.CompressionTypeOfBackupData == nil || *manifest.Status.CompressionTypeOfBackupData != "gzip" {
		t.Errorf("expected compression type gzip, got %v", manifest.Status.CompressionTypeOfBackupData)
	}
	if manifest.Status.CreatedAt != "2024-01-02T15:04:05Z" {
		t.Errorf("expected created_at 2024-01-02T15:04:05Z, got %s", manifest.Status.CreatedAt)
	}
	if !strings.Contains(stdout.String(), "deleted_lines_count: 0") || strings.Contains(stdout.String(), "ignored_rows_count") {
		t.Errorf("expected status.deleted_lines_count: 0 and no ignored_rows_count, got:\n%s", stdout.String())
	}
}

func TestDescribeFileCommand_JSONManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleStoredItemFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupFileDescribeTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "file", "1", "--output", "json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest stored_items.FileManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.Spec.FileType == nil || string(*manifest.Spec.FileType) != "csv" {
		t.Errorf("expected spec.file_type csv, got %v", manifest.Spec.FileType)
	}
	if manifest.Spec.FieldNameFullConversion == nil || (*manifest.Spec.FieldNameFullConversion)["old"] != "new" {
		t.Errorf("expected field_name_full_conversion.old to be new, got %#v", manifest.Spec.FieldNameFullConversion)
	}
	if manifest.Status == nil || manifest.Status.CleanRowsCount == nil || *manifest.Status.CleanRowsCount != 150 {
		t.Errorf("expected clean_rows_count 150, got %#v", manifest.Status)
	}
}

func TestDescribeFileCommand_InvalidID(t *testing.T) {
	rootCmd := setupFileDescribeTestCommand()
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"describe", "file", "abc"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid id, got nil")
	}
	if !strings.Contains(err.Error(), "invalid file ID") {
		t.Errorf("expected invalid file ID error, got %v", err)
	}
}

func TestDescribeFileCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupFileDescribeTestCommand()
	rootCmd.SetArgs([]string{"describe", "file", "1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected not logged in error, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected not logged in error, got %v", err)
	}
}

// describeFileWith serves file 1 with thread as its submission_comment_thread
// and the replacement-question route with status and body, and returns the
// command's stdout and stderr.
func describeFileWith(t *testing.T, thread *api.SubmissionCommentThreadSchema, status int, body string, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	item := sampleStoredItemFull()
	item.SubmissionCommentThread = thread
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, item)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1/replacement-question",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, status, json.RawMessage(body))
		})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupFileDescribeTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"describe", "file", "1"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

const openReplacementQuestion = `{"new_stored_item_id":1,"older_stored_item_id":99,"new_file_name":"orders.csv",` +
	`"old_file_name":"orders-draft.csv","old_file_sent_at":"2026-10-01T09:00:00Z","old_period_label":null,` +
	`"new_period_label":"June 2026","answer":null,"answered_at":null}`

func TestDescribeFileShowsAnOpenReplacementQuestion(t *testing.T) {
	stdout, stderr, err := describeFileWith(t, nil, http.StatusOK, openReplacementQuestion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var manifest stored_items.FileManifest
	if err := yaml.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	q := manifest.Status.ReplacementQuestion
	if q == nil || q.OlderStoredItemID != 99 || q.OldFileName != "orders-draft.csv" || q.Answer != nil ||
		q.NewPeriodLabel == nil || *q.NewPeriodLabel != "June 2026" || q.OldPeriodLabel != nil {
		t.Fatalf("status.replacement_question = %+v", q)
	}
	for _, want := range []string{
		"File 1 asks whether it replaces file 99 (orders-draft.csv)",
		"qctl file replace-older 1",
		"qctl file keep-both 1",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "qctl file") {
		t.Errorf("the hint leaked into stdout:\n%s", stdout)
	}
}

// An answered question is shown with its answer and asks for nothing; a
// null one is left out.
func TestDescribeFileReplacementQuestionAnsweredOrAbsent(t *testing.T) {
	answered := strings.Replace(openReplacementQuestion, `"answer":null,"answered_at":null`,
		`"answer":"keep_both","answered_at":"2026-10-02T09:00:00Z"`, 1)
	stdout, stderr, err := describeFileWith(t, nil, http.StatusOK, answered, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, `"answer": "keep_both"`) || strings.Contains(stderr, "replace-older") {
		t.Errorf("answered question: stdout %s\nstderr %s", stdout, stderr)
	}

	stdout, _, err = describeFileWith(t, nil, http.StatusOK, `null`, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, "replacement_question") {
		t.Errorf("a null question was rendered:\n%s", stdout)
	}
}

// The question is one extra read: a caller who may not answer it (403) still
// gets the file and no warning, while any other failure warns.
func TestDescribeFileSurvivesAnUnreadableQuestion(t *testing.T) {
	_, stderr, err := describeFileWith(t, nil, http.StatusForbidden, `{"detail":{"msg":"no permission"}}`)
	if err != nil {
		t.Fatalf("403: unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("403: stderr = %q, want nothing", stderr)
	}

	_, stderr, err = describeFileWith(t, nil, http.StatusInternalServerError, `{"detail":{"msg":"boom"}}`)
	if err != nil {
		t.Fatalf("500: unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "warning: replacement question unavailable") {
		t.Errorf("500: stderr lacks the warning: %q", stderr)
	}
}

func TestDescribeFileShowsTheSubmissionEmailThread(t *testing.T) {
	thread := &api.SubmissionCommentThreadSchema{ID: uuid.MustParse("0190bbbb-1111-2222-3333-444444444444"), DatasetID: 11}
	stdout, stderr, err := describeFileWith(t, thread, http.StatusOK, `null`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var manifest stored_items.FileManifest
	if err := yaml.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	got := manifest.Status.SubmissionCommentThread
	if got == nil || got.ID != thread.ID.String() || got.DatasetID != 11 {
		t.Errorf("status.submission_comment_thread = %+v", got)
	}
	if want := "qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table-id 11"; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

func TestDescribeFileCommand_DroppedLinesHintOnlyWhenPositive(t *testing.T) {
	three, zero := 3, 0
	for name, count := range map[string]*int{"positive": &three, "zero": &zero, "null": nil} {
		t.Run(name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			item := sampleStoredItemFull()
			item.DeletedLinesCount = count
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, item)
			})
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			rootCmd := setupFileDescribeTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SetArgs([]string{"describe", "file", "1", "-o", "json"})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hinted := strings.Contains(stderr.String(), "Next: qctl get dropped-lines 1 ")
			if hinted != (name == "positive") {
				t.Errorf("stderr = %q, want the hint only for a positive count", stderr.String())
			}
			if strings.Contains(stdout.String(), "dropped-lines") {
				t.Errorf("hint leaked onto stdout:\n%s", stdout.String())
			}
		})
	}
}
