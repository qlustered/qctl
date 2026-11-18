package copycmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// copyErrorContext is the context datasets.Client.Copy gives its errors.
const copyErrorContext = "copy table failed"

// locatedErrorRequest copies source 12 twice and source 15 once; the second
// copy of 12 carries a secret.
func locatedErrorRequest() datasets.CopyRequest {
	secret := "s3cret-value-never-shown"
	return datasets.CopyRequest{DataSources: &[]api.DatasetCopyDataSourceRequest{
		{SourceID: 12},
		{SourceID: 15},
		{SourceID: 12, S3SecretKey: &secret},
	}}
}

// A 400 the server located at one data_sources entry names that entry of
// --data-sources-file, counted from 1, and the field as the file spells it,
// and still exits 2.
func TestLocateDataSourceError_NamesTheFileEntry(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"connection check, no field",
			`{"detail":{"msg":"Could not connect to the copied cloud source. Check its connection settings and supplied secrets.","loc":["body","data_sources","1"],"error_id":""}}`,
			"copy table failed: --data-sources-file entry 2 (source_id 15): Could not connect to the copied cloud source. Check its connection settings and supplied secrets."},
		{"schedule",
			`{"detail":{"msg":"Invalid Cron Value","loc":["body","data_sources","0","schedule"]}}`,
			"copy table failed: --data-sources-file entry 1 (source_id 12): Invalid Cron Value\nField: schedule"},
		{"the URL is simple_url in the file",
			`{"detail":{"msg":"Invalid Simple URL","loc":["body","data_sources","2","custom_api_file_url"]}}`,
			"copy table failed: --data-sources-file entry 3 (source_id 12): Invalid Simple URL\nField: simple_url"},
		{"dropbox code",
			`{"detail":{"msg":"Could not authorize the copied Dropbox source.","loc":["body","data_sources","0","dropbox_authorization_code"]}}`,
			"copy table failed: --data-sources-file entry 1 (source_id 12): Could not authorize the copied Dropbox source.\nField: dropbox_authorization_code"},
		{"duplicate files",
			`{"detail":{"msg":"Another active source already reads simple_url the supplied URL with the same excel_sheet_name and archive_pattern. Change at least one of them so this source does not read the same files.","loc":["body","data_sources","2","excel_sheet_name"]}}`,
			"copy table failed: --data-sources-file entry 3 (source_id 12): Another active source already reads simple_url the supplied URL with the same excel_sheet_name and archive_pattern. Change at least one of them so this source does not read the same files.\nField: excel_sheet_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := comments.HandleError(400, []byte(tt.body), copyErrorContext)
			got := locateDataSourceError(original, locatedErrorRequest())
			if got.Error() != tt.want {
				t.Errorf("error =\n%s\nwant\n%s", got.Error(), tt.want)
			}
			if code := qerrors.GetExitCode(got); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			var apiErr *comments.APIError
			if !errors.As(got, &apiErr) || apiErr.StatusCode != 400 {
				t.Errorf("the *comments.APIError is not in the chain: %#v", got)
			}
			if strings.Contains(got.Error(), "s3cret") {
				t.Errorf("error carries a secret value: %s", got.Error())
			}
		})
	}
}

// A 5xx the server located at one entry names that entry too, and keeps its
// exit code and its retry hint.
func TestLocateDataSourceError_NamesTheFileEntryOfA5xx(t *testing.T) {
	body := `{"detail":{"msg":"Could not encrypt secrets for the copied cloud source.","loc":["body","data_sources","2"]}}`
	original := comments.HandleError(500, []byte(body), copyErrorContext)
	got := locateDataSourceError(original, locatedErrorRequest())
	want := "copy table failed: --data-sources-file entry 3 (source_id 12): Could not encrypt secrets for the copied cloud source.\nThis is a transient server failure; retry this command."
	if got.Error() != want {
		t.Errorf("error =\n%s\nwant\n%s", got.Error(), want)
	}
	if code, wantCode := qerrors.GetExitCode(got), qerrors.GetExitCode(original); code != wantCode {
		t.Errorf("exit code = %d, want %d", code, wantCode)
	}
}

// Without the rewording the same 400 names the entry by its 0-based request
// index, which is what the rewording replaces.
func TestLocateDataSourceError_ControlRendersTheRequestIndex(t *testing.T) {
	body := `{"detail":{"msg":"Invalid Cron Value","loc":["body","data_sources","0","schedule"]}}`
	if got, want := comments.HandleError(400, []byte(body), copyErrorContext).Error(),
		"copy table failed: Invalid Cron Value\nField: data_sources.0.schedule"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// Any error other than a 400 at [body, data_sources, "N", FIELD?] naming an
// entry of the request comes back as given.
func TestLocateDataSourceError_PassesOtherErrorsThrough(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"403 at the list", 403, `{"detail":{"msg":"You do not have the source:create permission.","loc":["body","data_sources"]}}`},
		{"no loc", 400, `{"detail":{"msg":"The table name is taken."}}`},
		{"another loc", 400, `{"detail":{"msg":"The table name is taken.","loc":["body","table_name"]}}`},
		{"numeric index", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources",0,"schedule"]}}`},
		{"index past the request", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","3"]}}`},
		{"negative index", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","-1"]}}`},
		{"padded index", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","01"]}}`},
		{"index not a number", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","first"]}}`},
		{"deeper loc", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","0","schedule","cron"]}}`},
		{"empty field", 400, `{"detail":{"msg":"Invalid.","loc":["body","data_sources","0",""]}}`},
		{"list-shaped detail", 422, `{"detail":[{"loc":["body","data_sources",0,"s3_secret_key"],"msg":"Input should be a valid string","type":"string_type"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := comments.HandleError(tt.status, []byte(tt.body), copyErrorContext)
			if got := locateDataSourceError(original, locatedErrorRequest()); got != original {
				t.Errorf("error = %q, want it as given: %q", got, original)
			}
		})
	}
	t.Run("no data sources requested", func(t *testing.T) {
		body := `{"detail":{"msg":"Invalid Cron Value","loc":["body","data_sources","0","schedule"]}}`
		original := comments.HandleError(400, []byte(body), copyErrorContext)
		if got := locateDataSourceError(original, datasets.CopyRequest{}); got != original {
			t.Errorf("error = %q, want it as given: %q", got, original)
		}
	})
	t.Run("not an API error", func(t *testing.T) {
		original := errors.New("dial tcp: connection refused")
		if got := locateDataSourceError(original, locatedErrorRequest()); got != original {
			t.Errorf("error = %q, want it as given", got)
		}
	})
}
