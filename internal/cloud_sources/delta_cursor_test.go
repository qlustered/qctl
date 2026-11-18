package cloud_sources

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

func cursorSource(applied *int) *CloudSourceFull {
	sourceType := DataSourceType("s3")
	state := DataSourceState("active")
	return &CloudSourceFull{
		ID: cloudSourcePtr(7), VersionID: cloudSourcePtr(3), Name: cloudSourcePtr("Airbyte CDC"),
		DatasetID: cloudSourcePtr(42), SettingsModelID: cloudSourcePtr(2),
		DataSourceType: &sourceType, State: &state,
		S3Bucket:           cloudSourcePtr("raw-orders"),
		DeltaAppliedCursor: applied,
	}
}

// delta_applied_cursor is server-managed: APIResponseToManifest puts it under
// status, and that manifest applied back never sends it, on either route.
func TestAppliedCursorIsStatusAndNeverWritten(t *testing.T) {
	existing := cursorSource(cloudSourcePtr(1000))

	m, err := APIResponseToManifest(existing)
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}
	if m.Status == nil || m.Status.DeltaAppliedCursor == nil || *m.Status.DeltaAppliedCursor != 1000 {
		t.Fatalf("status.delta_applied_cursor = %v, want 1000", m.Status)
	}

	// The manifest must still parse strictly, or it could not be applied
	// back.
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "delta_applied_cursor: 1000") {
		t.Fatalf("manifest YAML lacks the cursor:\n%s", raw)
	}
	var parsed CloudSourceManifest
	if err := pkgmanifest.StrictUnmarshal(raw, &parsed); err != nil {
		t.Fatalf("manifest YAML does not parse back: %v", err)
	}

	post, err := createBody(&parsed)
	if err != nil {
		t.Fatalf("createBody: %v", err)
	}
	// The PATCH body carries every change the plan found, plus id and
	// version_id.
	for route, body := range map[string][]byte{
		"POST":  post,
		"PATCH": planAgainstRead(t, existing, &parsed).body,
	} {
		if strings.Contains(string(body), "delta_applied_cursor") {
			t.Errorf("%s body carries delta_applied_cursor: %s", route, body)
		}
	}
}

// A cursor that has never advanced is null on the wire and absent from
// status.
func TestAppliedCursorNullIsOmitted(t *testing.T) {
	m, err := APIResponseToManifest(cursorSource(nil))
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "delta_applied_cursor") {
		t.Errorf("a null cursor reached the manifest:\n%s", raw)
	}
}

// The cursor is not a spec field, so a manifest that tries to write it is
// refused before any request.
func TestAppliedCursorIsRejectedInSpec(t *testing.T) {
	doc := `apiVersion: qctl/v1
kind: CloudSource
metadata:
  name: Airbyte CDC
spec:
  dataset_id: 42
  delta_applied_cursor: 5
`
	var m CloudSourceManifest
	err := pkgmanifest.StrictUnmarshal([]byte(doc), &m)
	if err == nil || !strings.Contains(err.Error(), "delta_applied_cursor") {
		t.Errorf("spec.delta_applied_cursor parsed; err = %v", err)
	}
}

func seriesCursorSource() *CloudSourceFull {
	source := cursorSource(nil)
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	source.TableSeriesID = &series
	source.RoutingMode = cloudSourcePtr("uploader_chooses")
	source.PeriodDeltaCursors = &[]api.PeriodSyncCursorSchema{
		{DatasetID: 42, SettingsModelID: 2, PeriodKey: "2026-06", DeltaAppliedCursor: nil},
		{DatasetID: 43, SettingsModelID: 3, PeriodKey: "2026-07", DeltaAppliedCursor: cloudSourcePtr(0)},
		{DatasetID: 44, SettingsModelID: 4, PeriodKey: "2026-08", DeltaAppliedCursor: cloudSourcePtr(84)},
	}
	return source
}

func TestPeriodCursorsAreStatusAndNeverWritten(t *testing.T) {
	existing := seriesCursorSource()
	m, err := APIResponseToManifest(existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range rtFormats {
		t.Run(format.name, func(t *testing.T) {
			raw := rtEncode(t, format.name, m)
			parsed := rtParse(t, raw)
			if parsed.Status == nil || len(parsed.Status.PeriodDeltaCursors) != 3 {
				t.Fatalf("missing period cursors:\n%s", raw)
			}
			cursors, err := json.Marshal(parsed.Status.PeriodDeltaCursors)
			if err != nil {
				t.Fatal(err)
			}
			var got []api.PeriodSyncCursorSchema
			if err := json.Unmarshal(cursors, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, *existing.PeriodDeltaCursors) {
				t.Errorf("period cursors = %s, want %+v", cursors, *existing.PeriodDeltaCursors)
			}
			post, err := createBody(parsed)
			if err != nil {
				t.Fatal(err)
			}
			parsed.Spec.Schedule = cloudSourcePtr("@daily")
			patch := planAgainstRead(t, existing, parsed).body
			for route, body := range map[string][]byte{"POST": post, "PATCH": patch} {
				members := bodyMembers(t, route, body)
				for _, key := range []string{"period_delta_cursors", "delta_applied_cursor"} {
					if _, ok := members[key]; ok {
						t.Errorf("%s carries %s: %s", route, key, body)
					}
				}
			}
		})
	}
}

func TestPeriodCursorsAreRejectedInSpec(t *testing.T) {
	doc := `apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: series
spec:
  dataset_id: 42
  period_delta_cursors: []
`
	var m CloudSourceManifest
	err := pkgmanifest.StrictUnmarshal([]byte(doc), &m)
	if err == nil || !strings.Contains(err.Error(), "period_delta_cursors") {
		t.Fatalf("spec.period_delta_cursors parsed; err = %v", err)
	}
}

func TestOrdinarySourcePeriodCursorsAreEmpty(t *testing.T) {
	for _, cursors := range []*[]api.PeriodSyncCursorSchema{nil, {}} {
		source := cursorSource(cloudSourcePtr(0))
		source.PeriodDeltaCursors = cursors
		m, err := APIResponseToManifest(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range rtFormats {
			raw := rtEncode(t, format.name, m)
			parsed := rtParse(t, raw)
			if parsed.Status.PeriodDeltaCursors == nil || len(parsed.Status.PeriodDeltaCursors) != 0 {
				t.Errorf("period cursors should be []:\n%s", raw)
			}
			if parsed.Status.DeltaAppliedCursor == nil || *parsed.Status.DeltaAppliedCursor != 0 {
				t.Errorf("scalar zero cursor lost:\n%s", raw)
			}
		}
	}
}
