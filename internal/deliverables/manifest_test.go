package deliverables

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

const (
	deliverableManifestHeader = "apiVersion: qluster.ai/v1\nkind: Deliverable\nmetadata:\n  slug: carrier-a-bordereau\nspec:\n"
	deliverableManifestBasics = "  name: Carrier A bordereau\n  filename_pattern: CarrierA_{period}.xlsx\n"
)

func TestParseManifestRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, doc, message string }{
		{"wrong apiVersion", strings.Replace(deliverableManifestHeader, "qluster.ai/v1", "qluster.ai/v2", 1) + deliverableManifestBasics, "apiVersion"},
		{"wrong kind", strings.Replace(deliverableManifestHeader, "Deliverable", "TableKind", 1) + deliverableManifestBasics, "kind: Deliverable"},
		{"missing slug", "apiVersion: qluster.ai/v1\nkind: Deliverable\nmetadata: {}\nspec:\n" + deliverableManifestBasics, "metadata.slug is required"},
		{"missing name", deliverableManifestHeader + "  filename_pattern: a.xlsx\n", "spec.name"},
		{"empty name", deliverableManifestHeader + "  name: \"\"\n  filename_pattern: a.xlsx\n", "spec.name"},
		{"name over 200 runes", deliverableManifestHeader + "  name: " + strings.Repeat("é", 201) + "\n  filename_pattern: a.xlsx\n", "spec.name"},
		{"missing filename_pattern", deliverableManifestHeader + "  name: A\n", "spec.filename_pattern"},
		{"empty filename_pattern", deliverableManifestHeader + "  name: A\n  filename_pattern: \"\"\n", "spec.filename_pattern"},
		{"null receiver_label", deliverableManifestHeader + deliverableManifestBasics + "  receiver_label: null\n", `use "" for none`},
		{"blank period_param", deliverableManifestHeader + deliverableManifestBasics + "  period_param: \" \"\n", "spec.period_param"},
		{"empty period_param", deliverableManifestHeader + deliverableManifestBasics + "  period_param: \"\"\n", "spec.period_param"},
		{"empty release", deliverableManifestHeader + deliverableManifestBasics + "  release: \"\"\n", "spec.release"},
		{"null release", deliverableManifestHeader + deliverableManifestBasics + "  release: null\n", "spec.release"},
		{"release over 100 runes", deliverableManifestHeader + deliverableManifestBasics + "  release: " + strings.Repeat("é", 101) + "\n", "spec.release"},
		{"description without release", deliverableManifestHeader + deliverableManifestBasics + "  description: text\n", "spec.description requires spec.release"},
		{"null description without release", deliverableManifestHeader + deliverableManifestBasics + "  description: null\n", "spec.description requires spec.release"},
		{"template without release", deliverableManifestHeader + deliverableManifestBasics + "  template: ./a.xlsx\n", "spec.template requires spec.release"},
		{"acknowledge_lossy without release", deliverableManifestHeader + deliverableManifestBasics + "  acknowledge_lossy: true\n", "spec.acknowledge_lossy requires spec.release"},
		{"acknowledge_lossy without template", deliverableManifestHeader + deliverableManifestBasics + "  release: v1\n  acknowledge_lossy: true\n", "spec.acknowledge_lossy requires spec.template"},
		{"template not a workbook", deliverableManifestHeader + deliverableManifestBasics + "  release: v1\n  template: ./a.csv\n", "not a workbook template"},
		{"null template", deliverableManifestHeader + deliverableManifestBasics + "  release: v1\n  template: null\n", "not a workbook template"},
		{"unknown spec key", deliverableManifestHeader + deliverableManifestBasics + "  colour: blue\n", "colour"},
		{"unknown status key", deliverableManifestHeader + deliverableManifestBasics + "status:\n  colour: blue\n", "colour"},
		{"sheets without release", deliverableManifestHeader + deliverableManifestBasics + "  sheets: {}\n", "spec.sheets requires spec.release"},
		{"spec.cells", deliverableManifestHeader + deliverableManifestBasics + "  cells:\n    - ref: A1\n", "invalid Deliverable manifest"},
		{"spec.cells beside an unknown key", deliverableManifestHeader + deliverableManifestBasics + "  colour: blue\n  cells: null\n", "invalid Deliverable manifest"},
		{"spec.cells in JSON", `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","cells":[]}}`, "invalid Deliverable manifest"},
		{"spec not a mapping", deliverableManifestHeader + "  - name\n", "invalid Deliverable manifest"},
		{"malformed YAML", deliverableManifestHeader + "  name: [\n", "invalid Deliverable manifest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.doc))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (err %v, manifest %#v), want 2", code, err, m)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error %q does not mention %q", err, tc.message)
			}
		})
	}
}

func TestParseManifestAcceptsValidInput(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"basics only", deliverableManifestHeader + deliverableManifestBasics},
		{"name of 200 runes", deliverableManifestHeader + "  name: " + strings.Repeat("é", 200) + "\n  filename_pattern: a.xlsx\n"},
		{"blank name, filename_pattern and release, as atlas accepts them", deliverableManifestHeader + "  name: \" \"\n  filename_pattern: \" \"\n  release: \"\\t\"\n"},
		{"empty receiver_label", deliverableManifestHeader + deliverableManifestBasics + "  receiver_label: \"\"\n"},
		{"null period_param", deliverableManifestHeader + deliverableManifestBasics + "  period_param: null\n"},
		{"release of 100 runes", deliverableManifestHeader + deliverableManifestBasics + "  release: " + strings.Repeat("é", 100) + "\n"},
		{"release with description", deliverableManifestHeader + deliverableManifestBasics + "  release: v2\n  description: null\n"},
		{"xlsm template acknowledged", deliverableManifestHeader + deliverableManifestBasics + "  release: v2\n  template: ./Carrier.XLSM\n  acknowledge_lossy: false\n"},
		{"status", deliverableManifestHeader + deliverableManifestBasics + "  release: v2\nstatus:\n  id: " + uuid.NewString() + "\n  revision_id: " + uuid.NewString() + "\n  revision_state: draft\n"},
		{"JSON", `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","period_param":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.doc)); err != nil {
				t.Fatalf("ParseManifest: %v (exit %d)", err, qerrors.GetExitCode(err))
			}
		})
	}
}

func TestParseManifestReadsEveryField(t *testing.T) {
	id, revisionID := uuid.New(), uuid.New()
	doc := deliverableManifestHeader + deliverableManifestBasics + "  receiver_label: Carrier A\n  period_param: period\n  release: v2\n" +
		"  description: September layout\n  template: ./carrier-a.xlsx\n  acknowledge_lossy: true\n" +
		"status:\n  id: " + id.String() + "\n  revision_id: " + revisionID.String() + "\n  revision_state: enabled\n"
	m, err := ParseManifest([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	label, period, description := "Carrier A", "period", "September layout"
	want := ManifestSpec{
		Name: "Carrier A bordereau", ReceiverLabel: &label, FilenamePattern: "CarrierA_{period}.xlsx", PeriodParam: &period,
		Release: "v2", Description: &description, Template: "./carrier-a.xlsx", AcknowledgeLossy: true,
	}
	if !reflect.DeepEqual(m.Spec, want) {
		t.Fatalf("spec = %#v, want %#v", m.Spec, want)
	}
	wantStatus := &ManifestStatus{ID: id, RevisionID: &revisionID, RevisionState: "enabled"}
	if m.Metadata.Slug != "carrier-a-bordereau" || !reflect.DeepEqual(m.Status, wantStatus) {
		t.Fatalf("metadata = %#v, status = %#v", m.Metadata, m.Status)
	}
}

func TestManifestHasTellsAbsentFromNullFromSet(t *testing.T) {
	m, err := ParseManifest([]byte(deliverableManifestHeader + deliverableManifestBasics + "  period_param: null\n  receiver_label: Carrier A\n"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"period_param": true, "receiver_label": true, "name": true, "release": false, "description": false} {
		if got := m.Has(key); got != want {
			t.Errorf("Has(%q) = %v, want %v", key, got, want)
		}
	}
	if m.Spec.PeriodParam != nil {
		t.Fatalf("period_param = %q, want nil", *m.Spec.PeriodParam)
	}
	absent, err := ParseManifest([]byte(deliverableManifestHeader + deliverableManifestBasics))
	if err != nil {
		t.Fatal(err)
	}
	if absent.Has("period_param") || absent.Spec.PeriodParam != nil {
		t.Fatal("an absent period_param reads as written")
	}
}

func TestExportRevision(t *testing.T) {
	draft := Revision{ID: uuid.New(), Release: "v1", State: api.DeliverableRevisionStateDraft}
	enabled := Revision{ID: uuid.New(), Release: "v2", State: api.DeliverableRevisionStateEnabled}
	disabled := Revision{ID: uuid.New(), Release: "v0", State: api.DeliverableRevisionStateDisabled}
	for _, tc := range []struct {
		name      string
		revisions []Revision
		want      *Revision
	}{
		{"enabled among several", []Revision{draft, enabled, disabled}, &enabled},
		{"only revision", []Revision{draft}, &draft},
		{"only revision, disabled", []Revision{disabled}, &disabled},
		{"none", nil, nil},
		{"several, none enabled", []Revision{draft, disabled}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExportRevision(tc.revisions)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExportRevision = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFromAPIRoundTripsWithoutARevision(t *testing.T) {
	item := Deliverable{ID: uuid.New(), Slug: "carrier-a-bordereau", Name: "Carrier A bordereau", ReceiverLabel: "", FilenamePattern: "CarrierA_{period}.xlsx"}
	exported := FromAPI(&item, nil)
	data, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"period_param: null", `receiver_label: ""`} {
		if !strings.Contains(string(data), line) {
			t.Fatalf("export lacks %q:\n%s", line, data)
		}
	}
	for _, key := range []string{"release:", "description:", "template:", "acknowledge_lossy:", "revision_id:", "revision_state:"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("export writes %q:\n%s", key, data)
		}
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest(export): %v\n%s", err, data)
	}
	if !reflect.DeepEqual(m, exported) {
		t.Fatalf("round trip = %#v, want %#v", m, exported)
	}
	if !m.Has("period_param") || m.Spec.PeriodParam != nil || m.Has("release") {
		t.Fatalf("Has(period_param) = %v, period_param = %v, Has(release) = %v", m.Has("period_param"), m.Spec.PeriodParam, m.Has("release"))
	}
}

func TestFromAPIRoundTripsWithARevision(t *testing.T) {
	period, description := "period", "September layout"
	item := Deliverable{
		ID: uuid.New(), Slug: "carrier-a-bordereau", Name: "Carrier A bordereau", ReceiverLabel: "Carrier A",
		FilenamePattern: "CarrierA_{period}.xlsx", PeriodParam: &period,
	}
	skeleton := 7
	rev := Revision{ID: uuid.New(), Release: "v2", Description: &description, State: api.DeliverableRevisionStateEnabled, SkeletonID: &skeleton}
	exported := FromAPI(&item, &rev)
	data, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"template:", "acknowledge_lossy:"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("export writes %q:\n%s", key, data)
		}
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest(export): %v\n%s", err, data)
	}
	if !reflect.DeepEqual(m, exported) {
		t.Fatalf("round trip = %#v, want %#v", m, exported)
	}
	if m.Spec.Release != "v2" || *m.Spec.Description != description || *m.Status.RevisionID != rev.ID || m.Status.RevisionState != "enabled" {
		t.Fatalf("round trip lost the revision: %s", data)
	}
	for _, key := range []string{"name", "receiver_label", "filename_pattern", "period_param", "release", "description"} {
		if !exported.Has(key) {
			t.Errorf("FromAPI does not mark %q", key)
		}
	}
	if exported.Has("template") || exported.Has("acknowledge_lossy") {
		t.Fatal("FromAPI marks a key it never writes")
	}
	rev.Description = nil
	if bare := FromAPI(&item, &rev); bare.Has("description") || bare.Spec.Description != nil {
		t.Fatal("FromAPI writes a nil description")
	}
}
