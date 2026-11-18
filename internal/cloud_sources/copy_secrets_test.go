package cloud_sources

import (
	"reflect"
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// Each copy secret key is a field of the request and a credential to
// BuildCopyBody, so the two copy routes agree on what a secret is.
func TestCopySecretKeysAreCredentialRequestFields(t *testing.T) {
	keys := CopySecretKeys()
	if len(keys) == 0 {
		t.Fatal("CopySecretKeys is empty; this check cannot fail as written")
	}
	requestKeys := jsonKeys(reflect.TypeFor[api.DatasetCopyDataSourceRequest]())
	for _, key := range keys {
		if !slices.Contains(requestKeys, key) {
			t.Errorf("%s is not a field of DatasetCopyDataSourceRequest", key)
		}
		if !isCopyCredential(key) {
			t.Errorf("%s is not a credential to BuildCopyBody", key)
		}
	}
}

func TestCopySecretKeysReturnsACopy(t *testing.T) {
	keys := CopySecretKeys()
	keys[0] = "changed"
	if CopySecretKeys()[0] == "changed" {
		t.Error("changing the returned slice changed the next call's")
	}
}

// Each key --secrets-file accepts is a field of the copy request, a credential
// BuildCopyBody strips from the template, and a field BuildCopyBody encodes.
func TestCopyRequestSecretKeysAreCredentialRequestFields(t *testing.T) {
	keys := CopyRequestSecretKeys()
	if len(keys) == 0 {
		t.Fatal("CopyRequestSecretKeys is empty; this check cannot fail as written")
	}
	requestKeys := jsonKeys(reflect.TypeFor[api.DataSourceModelCopyRequestSchema]())
	for _, key := range keys {
		if !slices.Contains(requestKeys, key) {
			t.Errorf("%s is not a field of DataSourceModelCopyRequestSchema", key)
		}
		if !isCopyCredential(key) {
			t.Errorf("%s is not a credential to BuildCopyBody", key)
		}
		if !slices.Contains(fieldKeys, key) {
			t.Errorf("%s is not a CloudSourceFields field, so BuildCopyBody would drop it", key)
		}
	}
}

// The copy request has no Dropbox token field; a copy takes an authorization
// code instead.
func TestCopyRequestHasNoDropboxAccessToken(t *testing.T) {
	requestKeys := jsonKeys(reflect.TypeFor[api.DataSourceModelCopyRequestSchema]())
	if !slices.Contains(requestKeys, dropboxAuthorizationCode) {
		t.Fatalf("DataSourceModelCopyRequestSchema has no %s; this check cannot fail as written", dropboxAuthorizationCode)
	}
	if slices.Contains(requestKeys, legacyDropboxKey) {
		t.Errorf("DataSourceModelCopyRequestSchema has %s; decide whether --secrets-file takes it", legacyDropboxKey)
	}
	if slices.Contains(CopyRequestSecretKeys(), legacyDropboxKey) {
		t.Errorf("CopyRequestSecretKeys holds %s, which the copy request has no field for", legacyDropboxKey)
	}
}

// A regen that adds a credential field to the copy request fails here until
// CopyRequestSecretKeys takes it.
func TestCopyRequestSecretKeysCoverEveryRequestCredential(t *testing.T) {
	var credentials []string
	for _, key := range jsonKeys(reflect.TypeFor[api.DataSourceModelCopyRequestSchema]()) {
		if isCopyCredential(key) {
			credentials = append(credentials, key)
		}
	}
	if !slices.Contains(credentials, simpleURLKey) || !slices.Contains(credentials, s3AccessKey) {
		t.Fatalf("request credentials %v lack simple_url or s3_access_key; this check cannot fail as written", credentials)
	}
	keys := CopyRequestSecretKeys()
	for _, key := range credentials {
		if !slices.Contains(keys, key) {
			t.Errorf("DataSourceModelCopyRequestSchema credential %s is not in CopyRequestSecretKeys", key)
		}
	}
}

func TestCopyRequestSecretKeysReturnsACopy(t *testing.T) {
	keys := CopyRequestSecretKeys()
	keys[0] = "changed"
	if CopyRequestSecretKeys()[0] == "changed" {
		t.Error("changing the returned slice changed the next call's")
	}
}
