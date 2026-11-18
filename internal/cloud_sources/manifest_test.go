package cloud_sources

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
)

func cloudSourcePtr[T any](value T) *T {
	return &value
}

func validCloudSourceManifest(sourceType DataSourceType) CloudSourceManifest {
	settingsModelID := 2
	return CloudSourceManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "CloudSource",
		Metadata:   pkgmanifest.Metadata{Name: "source"},
		Spec: CloudSourceSpec{
			DatasetID:       1,
			SettingsModelID: &settingsModelID,
			DataSourceType:  &sourceType,
		},
	}
}

func TestCloudSourceManifestValidatePreservesErrorOrderAndText(t *testing.T) {
	invalidType := DataSourceType("unsupported")
	manifest := CloudSourceManifest{
		APIVersion: "wrong",
		Kind:       "Other",
		Spec: CloudSourceSpec{
			DataSourceType: &invalidType,
		},
	}
	want := []ValidationError{
		{Field: "apiVersion", Message: "must be 'qluster.ai/v1'"},
		{Field: "kind", Message: "must be 'CloudSource'"},
		{Field: "metadata.name", Message: "required field is missing"},
		{Field: "spec.dataset_id", Message: "must be a positive integer"},
		{Field: "spec.data_source_type", Message: "invalid type"},
	}
	if got := manifest.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Validate() = %#v, want %#v", got, want)
	}
}

func TestCloudSourceManifestRejectsRetiredS3CompatibleNames(t *testing.T) {
	for _, retired := range []DataSourceType{"rustfs", "minio"} {
		manifest := validCloudSourceManifest(retired)
		want := []ValidationError{{Field: "spec.data_source_type", Message: "invalid type"}}
		if got := manifest.Validate(); !reflect.DeepEqual(got, want) {
			t.Errorf("Validate(%s) = %#v, want %#v", retired, got, want)
		}
	}
}

func TestCloudSourceManifestValidatePreservesSourceRules(t *testing.T) {
	tests := []struct {
		name       string
		sourceType DataSourceType
		configure  func(*CloudSourceSpec)
		want       []ValidationError
	}{
		{
			name: "s3 bucket", sourceType: DataSourceTypeS3,
			want: []ValidationError{{Field: "spec.s3_bucket", Message: "required for s3/s3_compatible sources"}},
		},
		{
			name: "s3_compatible bucket", sourceType: DataSourceTypeS3Compatible,
			want: []ValidationError{{Field: "spec.s3_bucket", Message: "required for s3/s3_compatible sources"}},
		},
		{
			name: "sftp fields", sourceType: DataSourceTypeSftp,
			want: []ValidationError{
				{Field: "spec.sftp_host", Message: "required for sftp sources"},
				{Field: "spec.sftp_user", Message: "required for sftp sources"},
			},
		},
		{
			name: "sftp password is sufficient", sourceType: DataSourceTypeSftp,
			configure: func(spec *CloudSourceSpec) {
				spec.SftpHost = cloudSourcePtr("host")
				spec.SftpUser = cloudSourcePtr("user")
				spec.SftpPassword = cloudSourcePtr("password")
			},
		},
		{
			name: "sftp ssh key is sufficient", sourceType: DataSourceTypeSftp,
			configure: func(spec *CloudSourceSpec) {
				spec.SftpHost = cloudSourcePtr("host")
				spec.SftpUser = cloudSourcePtr("user")
				spec.SftpSSHKey = cloudSourcePtr("key")
			},
		},
		{
			name: "gs bucket", sourceType: DataSourceTypeGs,
			want: []ValidationError{{Field: "spec.gs_bucket", Message: "required for gs sources"}},
		},
		{name: "dropbox without authorization code", sourceType: DataSourceTypeDropbox},
		{
			name: "simple URL", sourceType: DataSourceTypeSimpleURL,
			want: []ValidationError{{Field: "spec.simple_url", Message: "required for simple_url sources"}},
		},
		{
			name: "simple URL draft", sourceType: DataSourceTypeSimpleURL,
			configure: func(spec *CloudSourceSpec) {
				spec.State = cloudSourcePtr(DataSourceState("draft"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validCloudSourceManifest(tt.sourceType)
			if tt.configure != nil {
				tt.configure(&manifest.Spec)
			}
			if got := manifest.Validate(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Validate() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// Optional does not mean unchecked: a supplied zero or negative is still a
// manifest error, and it must still be reported at the same position in the
// ordered list, so the message a user sees does not move.
func TestCloudSourceManifestRejectsANonPositiveSettingsModelID(t *testing.T) {
	m := validCloudSourceManifest(DataSourceTypeS3)
	m.Spec.S3Bucket = cloudSourcePtr("raw-orders")
	zero := 0
	m.Spec.SettingsModelID = &zero

	want := []ValidationError{{Field: "spec.settings_model_id", Message: "must be a positive integer"}}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Validate() = %#v, want %#v", got, want)
	}
}

// Apply must never post a zero. The command layer resolves the field; a caller
// that skipped that step is a bug in qctl, and it must say so rather than
// creating a source in whatever table settings model 0 resolves to.
//
// "err != nil" alone would be vacuous here, and measurably so: with the guard
// deleted and no handlers registered the transport 404s the existence lookup,
// so the test stayed green while its claim was false. Two things fix that. The
// whole happy path is registered, so the only thing left that can fail is the
// guard; and the control below proves the fixture really does plan and create
// when the field IS resolved, so a red result is the guard rather than a
// broken harness. The assertion is then structural: the refusal reaches the
// server not at all.
func TestApplyRefusesAnUnresolvedSettingsModelID(t *testing.T) {
	c, mock := newCSClient(t)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+csOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[],"page":1,"total_rows":0}`))
	})
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+csOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":42,"name":"source"}`))
	})

	m := validCloudSourceManifest(DataSourceTypeS3)
	m.Spec.S3Bucket = cloudSourcePtr("raw-orders")

	plan, err := c.PrepareApply("tok", &m)
	if err != nil {
		t.Fatalf("control: a resolved manifest must plan cleanly against this harness, got %v", err)
	}
	if _, err := c.ApplyPrepared("tok", plan); err != nil {
		t.Fatalf("control: a resolved manifest must apply cleanly against this harness, got %v", err)
	}
	before := len(mock.Requests())

	m.Spec.SettingsModelID = nil
	if _, err := c.PrepareApply("tok", &m); err == nil {
		t.Fatal("PrepareApply must refuse a manifest whose settings_model_id was never resolved")
	}
	if after := mock.Requests(); len(after) != before {
		t.Fatalf("the refusal must happen before any request; requests grew from %d to %v", before, after[before:])
	}
}

// CheckRenamedKeys may refuse dropbox_access_token only while the manifest
// does not take it: a spec carrying the key must also fail the strict parse.
func TestRenamedKeyIsNotAManifestKey(t *testing.T) {
	var spec CloudSourceSpec
	if err := pkgmanifest.StrictUnmarshal([]byte(legacyDropboxKey+": x\n"), &spec); err == nil {
		t.Errorf("CloudSourceSpec takes spec.%s again; delete CheckRenamedKeys, which would refuse a key the manifest accepts", legacyDropboxKey)
	}

	for _, tc := range []struct {
		name    string
		spec    any
		refused bool
	}{
		{"the legacy key", map[string]any{legacyDropboxKey: "x"}, true},
		{"its replacement", map[string]any{"dropbox_authorization_code": "x"}, false},
		{"no spec", nil, false},
	} {
		if err := CheckRenamedKeys(tc.spec); (err != nil) != tc.refused {
			t.Errorf("CheckRenamedKeys(%s) = %v, want refused %v", tc.name, err, tc.refused)
		}
	}
}

// spec.state takes draft or nothing. Any other value is a manifest error
// naming spec.state, and PrepareApply refuses it with exit 2 before any
// request.
func TestCloudSourceManifestAcceptsOnlyADraftState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state *DataSourceState
		ok    bool
	}{
		{"absent", nil, true},
		{"draft", cloudSourcePtr(DataSourceState("draft")), true},
		{"active", cloudSourcePtr(DataSourceState("active")), false},
		{"disabled", cloudSourcePtr(DataSourceState("disabled")), false},
		{"deleted", cloudSourcePtr(DataSourceState("deleted")), false},
		{"empty", cloudSourcePtr(DataSourceState("")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validCloudSourceManifest(DataSourceTypeS3)
			m.Spec.S3Bucket = cloudSourcePtr("raw-orders")
			m.Spec.State = tc.state

			errs := m.Validate()
			if tc.ok {
				if len(errs) != 0 {
					t.Fatalf("Validate() = %#v, want none", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != "spec.state" {
				t.Fatalf("Validate() = %#v, want one spec.state error", errs)
			}

			c, mock, _ := httpServer(t, nil, http.StatusOK, `{"id":8,"name":"source"}`)
			_, err := c.PrepareApply("tok", &m)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("PrepareApply exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			if err != nil && !strings.Contains(err.Error(), "spec.state") {
				t.Errorf("PrepareApply error does not name spec.state: %v", err)
			}
			if sent := mock.Requests(); len(sent) != 0 {
				t.Errorf("an invalid spec.state sent %v", sent)
			}
		})
	}
}

// spec.state is a manifest key, in YAML and JSON alike.
func TestStrictParseAcceptsSpecState(t *testing.T) {
	for name, doc := range map[string]string{
		"yaml": "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: source\nspec:\n  dataset_id: 1\n  data_source_type: s3\n  s3_bucket: raw-orders\n  state: draft\n",
		"json": `{"apiVersion": "qluster.ai/v1", "kind": "CloudSource", "metadata": {"name": "source"},
 "spec": {"dataset_id": 1, "data_source_type": "s3", "s3_bucket": "raw-orders", "state": "draft"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var m CloudSourceManifest
			if err := pkgmanifest.StrictUnmarshal([]byte(doc), &m); err != nil {
				t.Fatalf("StrictUnmarshal refuses spec.state: %v", err)
			}
			if !m.Spec.CreatesDraft() {
				t.Errorf("spec.state = %v, want draft", m.Spec.State)
			}
		})
	}
}
