package cloud_sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestCopyTemplateUsesTargetThenSourceAndReturnsRawBody(t *testing.T) {
	c, mock := newCSClient(t)
	const targetDatasetID, sourceID = 42, 7
	path := fmt.Sprintf("/api/orgs/%s/datasets/%d/data-sources/%d/copy", csOrgID, targetDatasetID, sourceID)
	body := []byte(`{"id":null,"created_at":null,"name":"orders-abcd","dataset_id":42,"dataset_name":"orders","settings_model_id":2,"data_source_type":"s3","state":"active","s3_bucket":"raw-orders","s3_secret_key":"•••••••","infer_datetime_override":false,"null_values_override":["NULL"]}`)
	mock.RegisterHandler(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request) {
		writeJSONBytes(w, http.StatusOK, body)
	})

	template, raw, err := c.CopyTemplate("tok", targetDatasetID, sourceID)
	if err != nil {
		t.Fatalf("CopyTemplate: %v", err)
	}
	if template.Name != "orders-abcd" || template.DatasetID != targetDatasetID || template.SettingsModelID != 2 {
		t.Errorf("template = %+v, want name, target dataset and settings model from the response", template)
	}
	if !bytes.Equal(raw, body) {
		t.Errorf("raw body = %q, want exact response bytes %q", raw, body)
	}
	if got, want := mock.Requests(), []string{"GET " + path}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}

	// The endpoint's draft response also decodes into the sparse type used by
	// APIResponseToManifest; that type preserves omitted fields as nil.
	var full CloudSourceFull
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatalf("decode template as CloudSourceFull: %v", err)
	}
	if full.ID != nil || full.CreatedAt != nil || full.Pattern != nil {
		t.Errorf("null or omitted template fields became values: id=%v, created_at=%v, pattern=%v", full.ID, full.CreatedAt, full.Pattern)
	}
	manifest, err := APIResponseToManifest(&full)
	if err != nil {
		t.Fatalf("APIResponseToManifest(template): %v", err)
	}
	if manifest.Metadata.Name != "orders-abcd" || derefInt(manifest.Spec.SettingsModelID) != 2 || derefString(manifest.Spec.S3Bucket) != "raw-orders" {
		t.Errorf("manifest from template = %+v, want template fields retained", manifest)
	}
	if manifest.Spec.S3SecretKey != nil {
		t.Error("manifest retained the template's masked secret")
	}
	overrides := manifest.Spec.SettingsOverrides
	if overrides == nil || overrides.InferDatetime == nil || *overrides.InferDatetime || overrides.NullValues == nil ||
		!reflect.DeepEqual(*overrides.NullValues, []string{"NULL"}) {
		t.Errorf("manifest overrides = %+v, want false infer_datetime and literal NULL marker", overrides)
	}
}

func TestCopyPostsRawBodyToSourceRouteAndReturnsRawBody(t *testing.T) {
	c, mock := newCSClient(t)
	const sourceID = 7
	path := fmt.Sprintf("/api/orgs/%s/data-sources/%d/copy", csOrgID, sourceID)
	requestBody := []byte(`{"name":"orders-copy","dataset_id":42,"settings_model_id":2}`)
	responseBody := []byte(`{"id":8,"version_id":3,"name":"orders-copy","dataset_id":42,"dataset_name":"orders","data_source_type":"s3","state":"draft"}`)
	var received []byte
	mock.RegisterHandler(http.MethodPost, path, func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		writeJSONBytes(w, http.StatusOK, responseBody)
	})

	created, raw, err := c.Copy("tok", sourceID, requestBody)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if created.ID != 8 || created.Name != "orders-copy" || created.DatasetID != 42 {
		t.Errorf("created = %+v, want id 8, name orders-copy and dataset 42", created)
	}
	if !bytes.Equal(received, requestBody) {
		t.Errorf("request body = %q, want exact input bytes %q", received, requestBody)
	}
	if !bytes.Equal(raw, responseBody) {
		t.Errorf("raw body = %q, want exact response bytes %q", raw, responseBody)
	}
	if got, want := mock.Requests(), []string{"POST " + path}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestCopyMethodsMapHTTPStatusExitCodes(t *testing.T) {
	for _, operation := range []string{"template", "copy"} {
		for _, tc := range []struct {
			status int
			want   int
		}{
			{http.StatusBadRequest, qerrors.ExitBadUsage},
			{http.StatusNotFound, qerrors.ExitNotFound},
			{http.StatusForbidden, qerrors.ExitUnauthorized},
			{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		} {
			t.Run(fmt.Sprintf("%s/%d", operation, tc.status), func(t *testing.T) {
				c, mock := newCSClient(t)
				path := fmt.Sprintf("/api/orgs/%s/data-sources/7/copy", csOrgID)
				method := http.MethodPost
				if operation == "template" {
					path = fmt.Sprintf("/api/orgs/%s/datasets/42/data-sources/7/copy", csOrgID)
					method = http.MethodGet
				}
				mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
					writeJSONBytes(w, tc.status, []byte(`{"detail":{"msg":"copy rejected","error_code":"","loc":["body","name"]}}`))
				})

				var err error
				if operation == "template" {
					_, _, err = c.CopyTemplate("tok", 42, 7)
				} else {
					_, _, err = c.Copy("tok", 7, []byte(`{}`))
				}
				if err == nil {
					t.Fatalf("%s succeeded on HTTP %d", operation, tc.status)
				}
				if got := qerrors.GetExitCode(err); got != tc.want {
					t.Errorf("exit code = %d, want %d: %v", got, tc.want, err)
				}
				var opErr *apierror.OperationalError
				if !errors.As(err, &opErr) {
					t.Fatalf("error lost the backend detail: %v", err)
				}
				if opErr.StatusCode != tc.status || opErr.Message != "copy rejected" || opErr.Field != "name" {
					t.Errorf("backend detail = %+v, want status %d, message copy rejected and field name", opErr, tc.status)
				}
				if got, want := mock.Requests(), []string{method + " " + path}; !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestCopyMethodsUnregisteredRoutesReturnNotFound(t *testing.T) {
	c, mock := newCSClient(t)

	_, _, templateErr := c.CopyTemplate("tok", 42, 7)
	if templateErr == nil || qerrors.GetExitCode(templateErr) != qerrors.ExitNotFound {
		t.Fatalf("CopyTemplate on an unregistered route error = %v, want exit %d", templateErr, qerrors.ExitNotFound)
	}
	_, _, copyErr := c.Copy("tok", 7, []byte(`{}`))
	if copyErr == nil || qerrors.GetExitCode(copyErr) != qerrors.ExitNotFound {
		t.Fatalf("Copy on an unregistered route error = %v, want exit %d", copyErr, qerrors.ExitNotFound)
	}

	want := []string{
		fmt.Sprintf("GET /api/orgs/%s/datasets/42/data-sources/7/copy", csOrgID),
		fmt.Sprintf("POST /api/orgs/%s/data-sources/7/copy", csOrgID),
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func writeJSONBytes(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
