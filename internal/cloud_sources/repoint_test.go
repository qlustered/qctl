package cloud_sources

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestRepointClassifiesEveryPatchField(t *testing.T) {
	const recheck = "Re-check atlas data_source_patch_policy._REPOINT_FIELDS and data_source_files_guard.FILES_GUARD_FIELDS before classifying a new PATCH field."
	want := strings.Fields("archive_pattern data_source_type dropbox_folder excel_sheet_name gs_bucket gs_prefix pattern s3_access_key s3_bucket s3_endpoint_url s3_prefix s3_region_name sftp_folder sftp_host sftp_port sftp_user simple_url")
	var got []string
	for key := range repointKeys {
		got = append(got, key)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("repointKeys = %v, want %v. %s", got, want, recheck)
	}
	other := strings.Fields("broker_due_days chunk_size_to_read_for_header connection_timeout csv_delimiter csv_escapechar csv_quotechar dataset_id delete_source_file_after_backup excluded_row_signatures_override dropbox_authorization_code encoding_probably end_period_key export_request_days id identify_header_by_column_names ignore_column_ids internal_id name preload_row_drop_share raw_headers redirect_post_submit_to routing_column routing_mode routing_pattern s3_default_buffer_size schedule settings_model_id source_time_zone start_period_key state delta_applied_cursor table_series_id version_id")
	other = append(other, CopyRequestSecretKeys()...)
	for _, name := range overrideNames {
		other = append(other, name+overrideSuffix)
	}
	keys := jsonKeys(reflect.TypeFor[api.DataSourceModelPatchRequestSchema]())
	if len(keys) == 0 {
		t.Fatal("PATCH schema has no fields")
	}
	for _, key := range keys {
		if !repointKeys[key] && !slices.Contains(other, key) {
			t.Errorf("unclassified PATCH field %s. %s", key, recheck)
		}
	}
	for _, key := range want {
		if !slices.Contains(keys, key) {
			t.Errorf("repoint field %s is no longer in PATCH. %s", key, recheck)
		}
	}
}

func TestRepointRefusesEveryTriggerWithoutSecrets(t *testing.T) {
	for key := range repointKeys {
		if key == "data_source_type" {
			continue // Changing the type is already refused by planUpdate.
		}
		t.Run(key, func(t *testing.T) {
			live := httpLive()
			c, mock, writes := httpServer(t, live, http.StatusOK, `{}`)
			m := httpManifest()
			value := json.RawMessage(`"changed"`)
			if key == "sftp_port" {
				value = json.RawMessage(`2222`)
			}
			if err := decodeMembers(map[string]json.RawMessage{key: value}, &m.Spec.CloudSourceFields); err != nil {
				t.Fatal(err)
			}
			_, err := c.PrepareApply("tok", m)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", qerrors.GetExitCode(err), err)
			}
			for _, text := range []string{"spec.s3_secret_key", "spec.gpg_passphrase", "${VAR}"} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("error lacks %s: %v", text, err)
				}
			}
			if len(*writes) != 0 || len(mock.Requests()) != 2 {
				t.Fatalf("unexpected requests: %v", mock.Requests())
			}
		})
	}
}

func TestRepointRequiresEveryStoredSecretInEveryNonDraftState(t *testing.T) {
	for _, state := range []string{"active", "disabled", "draft"} {
		t.Run(state, func(t *testing.T) {
			live := httpLive()
			live["state"] = state
			for _, key := range copySecretKeys {
				if key != dropboxAuthorizationCode {
					live[key] = "stored-" + key
				}
			}
			c, mock, writes := httpServer(t, live, http.StatusOK, `{}`)
			m := httpManifest()
			m.Spec.S3Prefix = cloudSourcePtr("other/")
			m.Spec.S3SecretKey = cloudSourcePtr("replacement")
			plan, err := c.PrepareApply("tok", m)
			var diagnostic string
			if state == "draft" {
				if err != nil {
					t.Fatal(err)
				}
				diagnostic = strings.Join(plan.Warnings(), "\n")
				if !strings.Contains(diagnostic, "clears omitted secrets") {
					t.Fatalf("missing draft warning: %s", diagnostic)
				}
			} else {
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d, want 2: %v", qerrors.GetExitCode(err), err)
				}
				diagnostic = err.Error()
			}
			for _, key := range copySecretKeys {
				if key == legacyDropboxKey || key == "s3_secret_key" {
					continue
				}
				if !strings.Contains(diagnostic, "spec."+key) {
					t.Errorf("diagnostic lacks %s: %s", key, diagnostic)
				}
			}
			if strings.Contains(diagnostic, "replacement") || strings.Contains(diagnostic, "stored-") {
				t.Error("diagnostic contains a secret value")
			}
			if len(*writes) != 0 || len(mock.Requests()) != 2 {
				t.Fatalf("unexpected requests: %v", mock.Requests())
			}
		})
	}
}

func TestRepointResuppliesEverySecretIncludingUnchangedValues(t *testing.T) {
	live := httpLive()
	m := httpManifest()
	supplied := map[string]json.RawMessage{}
	want := map[string]string{"id": "7", "version_id": "17", "s3_prefix": `"other/"`}
	for _, key := range copySecretKeys {
		if key == dropboxAuthorizationCode {
			continue
		}
		value := "supplied-" + key
		live[key] = value
		if key == legacyDropboxKey {
			key = dropboxAuthorizationCode
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		supplied[key] = raw
		want[key] = string(raw)
	}
	if err := decodeMembers(supplied, &m.Spec.CloudSourceFields); err != nil {
		t.Fatal(err)
	}
	m.Spec.S3Prefix = cloudSourcePtr("other/")
	c, mock, writes := httpServer(t, live, http.StatusOK, `{}`)
	plan := httpPrepare(t, c, mock, m)
	if len(plan.Warnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", plan.Warnings())
	}
	if _, err := c.ApplyPrepared("tok", plan); err != nil {
		t.Fatal(err)
	}
	httpRequireBody(t, "PATCH", (*writes)[0], want)
}

func TestRepointRejectsEmptyOrMaskedSecrets(t *testing.T) {
	for _, value := range []string{"", secretMask, "prefix\u2022suffix"} {
		t.Run(value, func(t *testing.T) {
			c, _, writes := httpServer(t, httpLive(), http.StatusOK, `{}`)
			m := httpManifest()
			m.Spec.ExcelSheetName = cloudSourcePtr("Sheet2")
			m.Spec.S3SecretKey = &value
			m.Spec.GpgPassphrase = cloudSourcePtr("supplied")
			_, err := c.PrepareApply("tok", m)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.s3_secret_key") {
				t.Fatalf("expected missing s3_secret_key at exit 2: %v", err)
			}
			if len(*writes) != 0 {
				t.Fatal("refused update wrote a request")
			}
		})
	}
}

func TestRepointResendsUnchangedURLWithoutExposingIt(t *testing.T) {
	const url = "https://example.com/file.xlsx?token=private"
	for _, state := range []string{"active", "draft"} {
		t.Run(state, func(t *testing.T) {
			live := httpLive()
			live["state"], live["data_source_type"], live[simpleURLKey] = state, "simple_url", url
			c, mock, writes := httpServer(t, live, http.StatusOK, `{}`)
			m := httpManifest()
			typ := DataSourceTypeSimpleURL
			m.Spec.DataSourceType = &typ
			m.Spec.SimpleURL = cloudSourcePtr(url)
			m.Spec.ExcelSheetName = cloudSourcePtr("Sheet2")
			m.Spec.S3SecretKey = cloudSourcePtr("supplied-s3")
			m.Spec.GpgPassphrase = cloudSourcePtr("supplied-gpg")
			plan := httpPrepare(t, c, mock, m)
			if len(plan.Warnings()) != 0 {
				t.Fatalf("unexpected warnings: %v", plan.Warnings())
			}
			if _, err := c.ApplyPrepared("tok", plan); err != nil {
				t.Fatal(err)
			}
			body := bodyMembers(t, "PATCH", (*writes)[0])
			if string(body[simpleURLKey]) != `"`+url+`"` {
				t.Errorf("unchanged URL was not resupplied: %s", body[simpleURLKey])
			}
			m.Spec.SimpleURL = cloudSourcePtr(url + "-new")
			plan, err := c.PrepareApply("tok", m)
			if err != nil {
				t.Fatal(err)
			}
			if preview := FormatChanges(plan.Changes(), ""); strings.Contains(preview, url) {
				t.Fatalf("preview exposes URL: %s", preview)
			}
		})
	}
}
