package stored_items

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// FileSpec describes the declarative configuration of a stored item (file).
type FileSpec struct {
	DatasetID                        int                `yaml:"dataset_id" json:"dataset_id"`
	DatasetName                      string             `yaml:"dataset_name" json:"dataset_name"`
	CloudSourceID                    int                `yaml:"cloud_source_id" json:"cloud_source_id"`
	CloudSourceName                  string             `yaml:"cloud_source_name" json:"cloud_source_name"`
	FileName                         string             `yaml:"file_name" json:"file_name"`
	FileType                         *FileTypes         `yaml:"file_type,omitempty" json:"file_type,omitempty"`
	Encoding                         string             `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	IsTrashed                        bool               `yaml:"is_trashed" json:"is_trashed"`
	BackupKey                        string             `yaml:"backup_key,omitempty" json:"backup_key,omitempty"`
	BackupSettingsID                 *int               `yaml:"backup_settings_id,omitempty" json:"backup_settings_id,omitempty"`
	FileSize                         *int               `yaml:"file_size,omitempty" json:"file_size,omitempty"`
	CsvDelimiter                     *string            `yaml:"csv_delimiter,omitempty" json:"csv_delimiter,omitempty"`
	CsvEscapechar                    *string            `yaml:"csv_escapechar,omitempty" json:"csv_escapechar,omitempty"`
	CsvQuotechar                     *string            `yaml:"csv_quotechar,omitempty" json:"csv_quotechar,omitempty"`
	ExcelSheetNameForFile            string             `yaml:"excel_sheet_name_for_file,omitempty" json:"excel_sheet_name_for_file,omitempty"`
	HeaderLineNumberForFile          *int               `yaml:"header_line_number_for_file,omitempty" json:"header_line_number_for_file,omitempty"`
	RowNumberForFirstLineOfData      *int               `yaml:"row_number_for_first_line_of_data,omitempty" json:"row_number_for_first_line_of_data,omitempty"`
	ArrayDelimiterPerColumn          *map[string]string `yaml:"array_delimiter_per_column,omitempty" json:"array_delimiter_per_column,omitempty"`
	RawHeadersForFile                *[]string          `yaml:"raw_headers_for_file,omitempty" json:"raw_headers_for_file,omitempty"`
	FieldNameFullConversion          *map[string]string `yaml:"field_name_full_conversion,omitempty" json:"field_name_full_conversion,omitempty"`
	IgnoreColumnNames                *[]string          `yaml:"ignore_column_names,omitempty" json:"ignore_column_names,omitempty"`
	ExcludedRowSignatures            *[]string          `yaml:"excluded_row_signatures,omitempty" json:"excluded_row_signatures,omitempty"`
	DeletedRowsLineNumberToSignature *map[string]string `yaml:"deleted_rows_line_number_to_signature,omitempty" json:"deleted_rows_line_number_to_signature,omitempty"`
	OtherNames                       *[]string          `yaml:"other_names,omitempty" json:"other_names,omitempty"`
	ParentID                         *int               `yaml:"parent_id,omitempty" json:"parent_id,omitempty"`
	StartsAtParentRowNumber          *int               `yaml:"starts_at_parent_row_number,omitempty" json:"starts_at_parent_row_number,omitempty"`
	Signature                        *string            `yaml:"signature,omitempty" json:"signature,omitempty"`
}

// FileStatus captures runtime information for a stored item.
type FileStatus struct {
	ID                             int     `yaml:"id" json:"id"`
	Key                            string  `yaml:"key" json:"key"`
	FileSize                       *int    `yaml:"file_size,omitempty" json:"file_size,omitempty"`
	CompressionTypeOfBackupData    *string `yaml:"compression_type_of_backup_data,omitempty" json:"compression_type_of_backup_data,omitempty"`
	IsBackupEncrypted              *bool   `yaml:"is_backup_encrypted,omitempty" json:"is_backup_encrypted,omitempty"`
	IsEverLoaded                   *bool   `yaml:"is_ever_loaded,omitempty" json:"is_ever_loaded,omitempty"`
	IsUploadedViaSignedURL         *bool   `yaml:"is_uploaded_via_signed_url,omitempty" json:"is_uploaded_via_signed_url,omitempty"`
	IsDelta                        bool    `yaml:"is_delta" json:"is_delta"`
	DeltaCursor                    *int    `yaml:"delta_cursor,omitempty" json:"delta_cursor,omitempty"`
	RoutingPending                 bool    `yaml:"routing_pending" json:"routing_pending"`
	RoutingPeriodKey               *string `yaml:"routing_period_key" json:"routing_period_key"`
	ContradictedExternalFixesCount *int    `yaml:"contradicted_external_fixes_count" json:"contradicted_external_fixes_count"`
	DuplicateOfID                  *int    `yaml:"duplicate_of_id,omitempty" json:"duplicate_of_id,omitempty"`
	CleanRowsCount                 *int    `yaml:"clean_rows_count,omitempty" json:"clean_rows_count,omitempty"`
	BadRowsCount                   *int    `yaml:"bad_rows_count,omitempty" json:"bad_rows_count,omitempty"`
	DeletedLinesCount              *int    `yaml:"deleted_lines_count,omitempty" json:"deleted_lines_count,omitempty"` // lines dropped by an earlier delete
	BackupSettingsID               *int    `yaml:"backup_settings_id,omitempty" json:"backup_settings_id,omitempty"`
	WhodunitID                     *string `yaml:"whodunit_id,omitempty" json:"whodunit_id,omitempty"`
	CreatedAt                      string  `yaml:"created_at" json:"created_at"`

	Hold FileHold `yaml:"hold" json:"hold"`
	// Trash is the file's latest trash; nil when the file is not trashed.
	Trash *FileTrash `yaml:"trash,omitempty" json:"trash,omitempty"`

	// SubmissionCommentThread is the discussion holding the file's submission
	// email; nil when the server sends none.
	SubmissionCommentThread *FileCommentThread `yaml:"submission_comment_thread,omitempty" json:"submission_comment_thread,omitempty"`
	// ReplacementQuestion is the file's replacement question; nil when it has
	// none or it was not read.
	ReplacementQuestion *FileReplacementQuestion `yaml:"replacement_question,omitempty" json:"replacement_question,omitempty"`
	// Submission is the file's submission progress; nil when it was not read.
	Submission *FileSubmission `yaml:"submission,omitempty" json:"submission,omitempty"`
	// Quarantine is the file's quarantine split; nil when it was not read.
	Quarantine *FileQuarantine `yaml:"quarantine,omitempty" json:"quarantine,omitempty"`
}

// FileHold is the recorded hold decision for a file.
type FileHold struct {
	Decision  *api.PreloadCheckDecision `yaml:"decision" json:"decision"`
	DecidedAt *string                   `yaml:"decided_at" json:"decided_at"`
	DecidedBy *string                   `yaml:"decided_by" json:"decided_by"`
}

// FileTrash is a trash of a file: who trashed it, when and why, and what it
// deleted.
type FileTrash struct {
	TrashedAt string `yaml:"trashed_at" json:"trashed_at"`
	// TrashedBy is the user's name, or their email when they have none; nil
	// when the system trashed the file.
	TrashedBy              *string `yaml:"trashed_by" json:"trashed_by"`
	Comment                string  `yaml:"comment" json:"comment"`
	CleanRowsDeleted       int     `yaml:"clean_rows_deleted" json:"clean_rows_deleted"`
	BadRowsDeleted         int     `yaml:"bad_rows_deleted" json:"bad_rows_deleted"`
	SharedRowsUnlinked     int     `yaml:"shared_rows_unlinked" json:"shared_rows_unlinked"`
	ApprovedRowsCount      int     `yaml:"approved_rows_count" json:"approved_rows_count"`
	HeldRowsCount          int     `yaml:"held_rows_count" json:"held_rows_count"`
	EditedRowsCount        int     `yaml:"edited_rows_count" json:"edited_rows_count"`
	PersonDeletedRowsCount int     `yaml:"person_deleted_rows_count" json:"person_deleted_rows_count"`
	RowDeleteIDs           []int   `yaml:"row_delete_ids,omitempty" json:"row_delete_ids,omitempty"`
}

// NewFileTrash is the status form of t; nil for a nil t.
func NewFileTrash(t *api.FileTrashSchema) *FileTrash {
	if t == nil {
		return nil
	}
	trash := &FileTrash{
		TrashedAt:              formatTime(t.TrashedAt),
		Comment:                t.Comment,
		CleanRowsDeleted:       t.CleanRowsDeleted,
		BadRowsDeleted:         t.BadRowsDeleted,
		SharedRowsUnlinked:     t.SharedRowsUnlinked,
		ApprovedRowsCount:      t.ApprovedRowsCount,
		HeldRowsCount:          t.HeldRowsCount,
		EditedRowsCount:        t.EditedRowsCount,
		PersonDeletedRowsCount: t.PersonDeletedRowsCount,
		RowDeleteIDs:           t.RowDeleteIds,
	}
	if by := t.TrashedBy; by != nil {
		name := strings.TrimSpace(by.FirstName + " " + by.LastName)
		if name == "" {
			name = by.Email
		}
		trash.TrashedBy = &name
	}
	return trash
}

// FileCommentThread names a comment thread and the table it belongs to.
type FileCommentThread struct {
	ID        string `yaml:"id" json:"id"`
	DatasetID int    `yaml:"dataset_id" json:"dataset_id"`
}

// FileReplacementQuestion asks whether a file replaces an older file of its
// cloud source; Answer and AnsweredAt are nil while it is open.
type FileReplacementQuestion struct {
	OlderStoredItemID int     `yaml:"older_stored_item_id" json:"older_stored_item_id"`
	OldFileName       string  `yaml:"old_file_name" json:"old_file_name"`
	OldFileSentAt     string  `yaml:"old_file_sent_at" json:"old_file_sent_at"`
	OldPeriodLabel    *string `yaml:"old_period_label" json:"old_period_label"`
	NewPeriodLabel    *string `yaml:"new_period_label" json:"new_period_label"`
	Answer            *string `yaml:"answer" json:"answer"`
	AnsweredAt        *string `yaml:"answered_at" json:"answered_at"`
}

// NewFileReplacementQuestion is the status form of q; nil for a nil q.
func NewFileReplacementQuestion(q *ReplacementQuestion) *FileReplacementQuestion {
	if q == nil {
		return nil
	}
	out := &FileReplacementQuestion{
		OlderStoredItemID: q.OlderStoredItemID,
		OldFileName:       q.OldFileName,
		OldFileSentAt:     formatTime(q.OldFileSentAt),
		OldPeriodLabel:    q.OldPeriodLabel,
		NewPeriodLabel:    q.NewPeriodLabel,
	}
	if q.Answer != nil {
		answer := string(*q.Answer)
		out.Answer = &answer
	}
	if q.AnsweredAt != nil {
		at := formatTime(*q.AnsweredAt)
		out.AnsweredAt = &at
	}
	return out
}

// FileManifest is the manifest view returned by describe.
type FileManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       FileSpec             `yaml:"spec" json:"spec"`
	Status     *FileStatus          `yaml:"status,omitempty" json:"status,omitempty"`
}

// APIResponseToManifest converts the stored item API response into a declarative manifest.
func APIResponseToManifest(resp *StoredItemFull) *FileManifest {
	labels := map[string]string{
		"dataset_id":           strconv.Itoa(resp.DatasetID),
		"data_source_model_id": strconv.Itoa(resp.DataSourceModelID),
	}

	annotations := map[string]string{}
	if resp.DatasetName != "" {
		annotations["dataset_name"] = resp.DatasetName
	}
	if resp.DataSourceModelName != "" {
		annotations["data_source_model_name"] = resp.DataSourceModelName
	}
	if resp.Key != "" {
		annotations["file_key"] = resp.Key
	}

	spec := FileSpec{
		DatasetID:                        resp.DatasetID,
		DatasetName:                      resp.DatasetName,
		CloudSourceID:                    resp.DataSourceModelID,
		CloudSourceName:                  resp.DataSourceModelName,
		FileName:                         resp.FileName,
		FileType:                         resp.FileType,
		Encoding:                         resp.Encoding,
		IsTrashed:                        resp.IsTrashed,
		BackupKey:                        resp.BackupKey,
		BackupSettingsID:                 resp.BackupSettingsID,
		FileSize:                         resp.FileSize,
		CsvDelimiter:                     resp.CsvDelimiter,
		CsvEscapechar:                    resp.CsvEscapechar,
		CsvQuotechar:                     resp.CsvQuotechar,
		ExcelSheetNameForFile:            resp.ExcelSheetNameForFile,
		HeaderLineNumberForFile:          resp.HeaderLineNumberForFile,
		RowNumberForFirstLineOfData:      resp.RowNumberForFirstLineOfData,
		ArrayDelimiterPerColumn:          convertMapToString(resp.ArrayDelimiterPerColumn),
		RawHeadersForFile:                resp.RawHeadersForFile,
		FieldNameFullConversion:          resp.FieldNameFullConversion,
		IgnoreColumnNames:                resp.IgnoreColumnNames,
		ExcludedRowSignatures:            resp.ExcludedRowSignatures,
		DeletedRowsLineNumberToSignature: resp.DeletedRowsLineNumberToSignature,
		OtherNames:                       resp.OtherNames,
		ParentID:                         resp.ParentID,
		StartsAtParentRowNumber:          resp.StartsAtParentRowNumber,
		Signature:                        resp.Signature,
	}

	status := &FileStatus{
		ID:                             resp.ID,
		Key:                            resp.Key,
		FileSize:                       resp.FileSize,
		IsBackupEncrypted:              resp.IsBackupEncrypted,
		IsEverLoaded:                   resp.IsEverLoaded,
		IsUploadedViaSignedURL:         resp.IsUploadedViaSignedURL,
		IsDelta:                        resp.IsDelta != nil && *resp.IsDelta,
		DeltaCursor:                    resp.DeltaCursor,
		RoutingPending:                 resp.RoutingPending != nil && *resp.RoutingPending,
		RoutingPeriodKey:               resp.RoutingPeriodKey,
		ContradictedExternalFixesCount: resp.ContradictedExternalFixesCount,
		DuplicateOfID:                  resp.DuplicateOfID,
		CleanRowsCount:                 resp.CleanRowsCount,
		BadRowsCount:                   resp.BadRowsCount,
		DeletedLinesCount:              resp.DeletedLinesCount,
		BackupSettingsID:               resp.BackupSettingsID,
		CreatedAt:                      formatTime(resp.CreatedAt),
		Hold: FileHold{
			Decision:  resp.HoldDecision,
			DecidedBy: resp.HoldDecidedByName,
		},
		Trash: NewFileTrash(resp.Trash),
	}

	if resp.HoldDecidedAt != nil {
		at := formatTime(*resp.HoldDecidedAt)
		status.Hold.DecidedAt = &at
	}

	if resp.CompressionTypeOfBackupData != nil {
		value := string(*resp.CompressionTypeOfBackupData)
		status.CompressionTypeOfBackupData = &value
	}

	if resp.WhodunitID != nil {
		status.WhodunitID = stringPtr(resp.WhodunitID)
	}

	if thread := resp.SubmissionCommentThread; thread != nil {
		status.SubmissionCommentThread = &FileCommentThread{ID: thread.ID.String(), DatasetID: thread.DatasetID}
	}

	manifest := &FileManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "File",
		Metadata: pkgmanifest.Metadata{
			Name:        resp.FileName,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec:   spec,
		Status: status,
	}

	// Drop empty annotations map to keep output tidy
	if len(manifest.Metadata.Annotations) == 0 {
		manifest.Metadata.Annotations = nil
	}

	return manifest
}

func convertMapToString(src *map[string]interface{}) *map[string]string {
	if src == nil || len(*src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(*src))
	for key, val := range *src {
		dst[key] = fmt.Sprint(val)
	}

	return &dst
}

func stringPtr(id *openapi_types.UUID) *string {
	if id == nil {
		return nil
	}
	val := id.String()
	return &val
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
