package ingestion

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// A non-file job has no file_name: the manifest is named after the job and
// keeps spec.file_name an explicit null.
func TestAPIResponseToManifestWithoutFileName(t *testing.T) {
	m := APIResponseToManifest(&IngestionJobFull{ID: 77})
	if m.Metadata.Name != "ingestion-job-77" {
		t.Errorf("metadata.name = %q, want ingestion-job-77", m.Metadata.Name)
	}
	out, err := yaml.Marshal(m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(out, &spec); err != nil {
		t.Fatal(err)
	}
	if v, ok := spec["file_name"]; !ok || v != nil {
		t.Errorf("spec.file_name = %v (present %v), want an explicit null", v, ok)
	}
}
