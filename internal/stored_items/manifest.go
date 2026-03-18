package stored_items

import (
	"fmt"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
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
	IgnoreFile                       bool               `yaml:"ignore_file" json:"ignore_file"`
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
	DeletedLineSignatures            *[]string          `yaml:"deleted_line_signatures,omitempty" json:"deleted_line_signatures,omitempty"`
	DeletedRowsLineNumberToSignature *map[string]string `yaml:"deleted_rows_line_number_to_signature,omitempty" json:"deleted_rows_line_number_to_signature,omitempty"`
	OtherNames                       *[]string          `yaml:"other_names,omitempty" json:"other_names,omitempty"`
	ParentID                         *int               `yaml:"parent_id,omitempty" json:"parent_id,omitempty"`
	StartsAtParentRowNumber          *int               `yaml:"starts_at_parent_row_number,omitempty" json:"starts_at_parent_row_number,omitempty"`
	Signature                        *string            `yaml:"signature,omitempty" json:"signature,omitempty"`
}

// FileStatus captures runtime information for a stored item.
type FileStatus struct {
	ID                          int     `yaml:"id" json:"id"`
	Key                         string  `yaml:"key" json:"key"`
	FileSize                    *int    `yaml:"file_size,omitempty" json:"file_size,omitempty"`
	CompressionTypeOfBackupData *string `yaml:"compression_type_of_backup_data,omitempty" json:"compression_type_of_backup_data,omitempty"`
	IsBackupEncrypted           *bool   `yaml:"is_backup_encrypted,omitempty" json:"is_backup_encrypted,omitempty"`
	IsEverLoaded                *bool   `yaml:"is_ever_loaded,omitempty" json:"is_ever_loaded,omitempty"`
	IsUploadedViaSignedURL      *bool   `yaml:"is_uploaded_via_signed_url,omitempty" json:"is_uploaded_via_signed_url,omitempty"`
	DuplicateOfID               *int    `yaml:"duplicate_of_id,omitempty" json:"duplicate_of_id,omitempty"`
	CleanRowsCount              *int    `yaml:"clean_rows_count,omitempty" json:"clean_rows_count,omitempty"`
	BadRowsCount                *int    `yaml:"bad_rows_count,omitempty" json:"bad_rows_count,omitempty"`
	IgnoredRowsCount            *int    `yaml:"ignored_rows_count,omitempty" json:"ignored_rows_count,omitempty"`
	BackupSettingsID            *int    `yaml:"backup_settings_id,omitempty" json:"backup_settings_id,omitempty"`
	WhodunitID                  *string `yaml:"whodunit_id,omitempty" json:"whodunit_id,omitempty"`
	CreatedAt                   string  `yaml:"created_at" json:"created_at"`
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
		IgnoreFile:                       resp.IgnoreFile,
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
		DeletedLineSignatures:            resp.DeletedLineSignatures,
		DeletedRowsLineNumberToSignature: resp.DeletedRowsLineNumberToSignature,
		OtherNames:                       resp.OtherNames,
		ParentID:                         resp.ParentID,
		StartsAtParentRowNumber:          resp.StartsAtParentRowNumber,
		Signature:                        resp.Signature,
	}

	status := &FileStatus{
		ID:                     resp.ID,
		Key:                    resp.Key,
		FileSize:               resp.FileSize,
		IsBackupEncrypted:      resp.IsBackupEncrypted,
		IsEverLoaded:           resp.IsEverLoaded,
		IsUploadedViaSignedURL: resp.IsUploadedViaSignedURL,
		DuplicateOfID:          resp.DuplicateOfID,
		CleanRowsCount:         resp.CleanRowsCount,
		BadRowsCount:           resp.BadRowsCount,
		IgnoredRowsCount:       resp.IgnoredRowsCount,
		BackupSettingsID:       resp.BackupSettingsID,
		CreatedAt:              formatTime(resp.CreatedAt),
	}

	if resp.CompressionTypeOfBackupData != nil {
		value := string(*resp.CompressionTypeOfBackupData)
		status.CompressionTypeOfBackupData = &value
	}

	if resp.WhodunitID != nil {
		status.WhodunitID = stringPtr(resp.WhodunitID)
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
