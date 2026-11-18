package apply

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/pkg/secretmask"
)

// cloudSourceStringFields returns the wire key of every *string field of
// cloud_sources.CloudSourceFields, and the credentials and URL. A
// secret field of any other type fails t.
func cloudSourceStringFields(t *testing.T) (all map[string]int, credentials []string) {
	t.Helper()
	sm := secretmask.New()
	stringPtr := reflect.TypeOf((*string)(nil))
	typ := reflect.TypeOf(cloud_sources.CloudSourceFields{})
	all = map[string]int{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		secret := sm.IsSecret(key) || key == "simple_url"
		if f.Type != stringPtr {
			if secret {
				t.Errorf("credential %s is %s, not *string: processCloudSourceEnv cannot expand it", key, f.Type)
			}
			continue
		}
		all[key] = i
		if secret {
			credentials = append(credentials, key)
		}
	}
	if len(credentials) == 0 {
		t.Fatal("no credential fields found on CloudSourceFields")
	}
	if !slices.Contains(credentials, "s3_secret_key") {
		t.Fatalf("s3_secret_key missing from the credential fields %v", credentials)
	}
	return all, credentials
}

func cloudSourceEnvName(key string) string {
	return "QCTL_TEST_CLOUD_SOURCE_" + strings.ToUpper(key)
}

func TestProcessCloudSourceEnvExpandsEveryCredential(t *testing.T) {
	all, credentials := cloudSourceStringFields(t)

	var m cloud_sources.CloudSourceManifest
	fields := reflect.ValueOf(&m.Spec.CloudSourceFields).Elem()
	for key, i := range all {
		t.Setenv(cloudSourceEnvName(key), "value-of-"+key)
		ref := "${" + cloudSourceEnvName(key) + "}"
		fields.Field(i).Set(reflect.ValueOf(&ref))
	}

	processCloudSourceEnv(&m)

	for key, i := range all {
		got := fields.Field(i).Interface().(*string)
		want := "${" + cloudSourceEnvName(key) + "}"
		if slices.Contains(credentials, key) {
			want = "value-of-" + key
		}
		if got == nil {
			t.Errorf("%s = nil, want %q", key, want)
		} else if *got != want {
			t.Errorf("%s = %q, want %q", key, *got, want)
		}
	}
}

func TestProcessCloudSourceEnvDropsAnEmptyCredential(t *testing.T) {
	all, credentials := cloudSourceStringFields(t)

	for _, tc := range []struct {
		name  string
		value func(key string) string
	}{
		{"unset variable", func(key string) string { return "${" + cloudSourceEnvName(key) + "}" }},
		{"empty string", func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m cloud_sources.CloudSourceManifest
			fields := reflect.ValueOf(&m.Spec.CloudSourceFields).Elem()
			for _, key := range credentials {
				t.Setenv(cloudSourceEnvName(key), "")
				if err := os.Unsetenv(cloudSourceEnvName(key)); err != nil {
					t.Fatal(err)
				}
				value := tc.value(key)
				fields.Field(all[key]).Set(reflect.ValueOf(&value))
			}

			processCloudSourceEnv(&m)

			for _, key := range credentials {
				if got := fields.Field(all[key]).Interface().(*string); got != nil {
					t.Errorf("%s = %q, want nil", key, *got)
				}
			}
		})
	}
}
