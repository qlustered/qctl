package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// exportDetailJSON is a revision read as the server sends it: table 7 built
// from raw_orders (1, the spine) and raw_customers (2).
const exportDetailJSON = `{
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "state": "enabled",
  "spec_sha256": "ab12",
  "created_at": "2026-09-22T10:00:00Z",
  "created_by_user_id": null,
  "is_rerunnable": true,
  "revision": {
    "release": "v3",
    "description": "orders with their customers",
    "sources": {
      "orders": {"dataset_id": 1, "join_fields": {}, "arity": null},
      "customers": {"dataset_id": 2, "arity": "zero_or_one", "join_fields": {
        "customer_id": "customer_id",
        "region": {"spine_fields": ["region"], "source_fields": ["region_code"], "spine_expr": "lower(${0})"},
        "tier": {"spine_fields": [], "source_fields": ["tier"], "constant": [1, 2.5, 9223372036854775807, "gold", true, {"n": 0}, 2.0, 1e20]}
      }}
    },
    "output": {
      "order_id": {"source": "orders", "field": "order_id"},
      "cust_name": {"source": "customers", "field": "name"}
    }
  },
  "group": {
    "name": "orders_enriched", "slug": "orders-enriched", "spine_dataset_id": 1,
    "natural_key_columns": ["order_id"], "natural_key_normalization": {"order_id": "trim_lower"},
    "identity_hash_version": 1, "replaced_group_id": null, "is_retired": false
  }
}`

func exportDetail(t *testing.T) *RevisionDetail {
	t.Helper()
	var detail RevisionDetail
	if err := json.Unmarshal([]byte(exportDetailJSON), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return &detail
}

func exportTable() LiveTable {
	return LiveTable{
		ID:               7,
		Name:             "orders_enriched",
		Database:         "analytics",
		Schema:           "public",
		TableName:        "orders_enriched",
		Destination:      "warehouse",
		BackupSettingsID: 4,
		BackupSettings:   diffPtr("nightly"),
	}
}

func exportNames() map[int]string {
	return map[int]string{1: "raw_orders", 2: "raw_customers"}
}

func export(t *testing.T, detail *RevisionDetail) *LinkedTableManifestWithStatus {
	t.Helper()
	m, err := ExportManifest(exportTable(), detail, exportNames(), false)
	if err != nil {
		t.Fatalf("ExportManifest: %v", err)
	}
	return m
}

func exportYAML(t *testing.T, m *LinkedTableManifestWithStatus) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder := jsonnum.NewYAMLEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(m); err != nil {
		t.Fatalf("encode yaml: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close yaml: %v", err)
	}
	return buf.Bytes()
}

func TestExportManifest_Fields(t *testing.T) {
	m := export(t, exportDetail(t))
	spec := m.Spec

	if m.Kind != "LinkedTable" || m.Metadata.Name != "orders_enriched" || m.Metadata.ID != "" {
		t.Errorf("kind/metadata = %q %+v", m.Kind, m.Metadata)
	}
	if spec.Destination != "warehouse" || spec.BackupSettings == nil || *spec.BackupSettings != "nightly" {
		t.Errorf("destination/backup_settings = %q %v", spec.Destination, spec.BackupSettings)
	}
	if spec.Table.Database != "analytics" || spec.Table.Schema == nil || *spec.Table.Schema != "public" || spec.Table.Name != "orders_enriched" {
		t.Errorf("table = %+v", spec.Table)
	}
	if spec.Spine.Source != "orders" || strings.Join(spec.Spine.NaturalKeyColumns, ",") != "order_id" ||
		spec.Spine.NaturalKeyNormalization["order_id"] != "trim_lower" ||
		spec.Spine.IdentityHashVersion == nil || *spec.Spine.IdentityHashVersion != 1 {
		t.Errorf("spine = %+v", spec.Spine)
	}
	if got := spec.Sources["orders"]; got.Dataset != "raw_orders" || got.Arity != nil || got.JoinFields != nil {
		t.Errorf("sources.orders = %+v", got)
	}
	customers := spec.Sources["customers"]
	if customers.Dataset != "raw_customers" || customers.Arity == nil || *customers.Arity != "zero_or_one" {
		t.Errorf("sources.customers = %+v", customers)
	}
	if got := customers.JoinFields["customer_id"]; got.Clause != nil || got.Shorthand != "customer_id" {
		t.Errorf("join_fields.customer_id = %+v", got)
	}
	if got := customers.JoinFields["region"].Clause; got == nil || got.SpineExpr == nil || *got.SpineExpr != "lower(${0})" {
		t.Errorf("join_fields.region = %+v", got)
	}
	if spec.Release != nil {
		t.Errorf("spec.release = %q, want it left out", *spec.Release)
	}
	if spec.DesiredState == nil || *spec.DesiredState != "enabled" {
		t.Errorf("spec.desired_state = %v, want enabled", spec.DesiredState)
	}
	if spec.Description == nil || *spec.Description != "orders with their customers" {
		t.Errorf("spec.description = %v", spec.Description)
	}
	if spec.DryRun != nil {
		t.Errorf("spec.dry_run = %+v, want it left out", spec.DryRun)
	}
	status := m.Status
	if status.TableID != 7 || status.Slug != "orders-enriched" || status.Release != "v3" || status.State != "enabled" ||
		status.BackupSettingsID != 4 || status.RevisionID.String() != "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33" ||
		status.CompositeSpecGroupID.String() != "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10" {
		t.Errorf("status = %+v", status)
	}
}

// Applying an unedited export must not enable a revision that is not enabled.
func TestExportManifest_DesiredStateIsDraftUnlessEnabled(t *testing.T) {
	for state, want := range map[string]string{"enabled": "enabled", "draft": "draft", "in_review": "draft", "disabled": "draft"} {
		detail := exportDetail(t)
		detail.State = state
		m := export(t, detail)
		if got := *m.Spec.DesiredState; got != want {
			t.Errorf("state %s: desired_state = %s, want %s", state, got, want)
		}
		if m.Status.State != state {
			t.Errorf("state %s: status.state = %s", state, m.Status.State)
		}
	}
}

func TestExportManifest_PendingAutoEnablePreservesDesiredState(t *testing.T) {
	detail := exportDetail(t)
	detail.State = "draft"
	m, err := ExportManifest(exportTable(), detail, exportNames(), true)
	if err != nil {
		t.Fatal(err)
	}
	if *m.Spec.DesiredState != "enabled" || m.Status.State != "draft" {
		t.Errorf("desired_state/status.state = %s/%s, want enabled/draft", *m.Spec.DesiredState, m.Status.State)
	}
}

func TestExportManifest_NoBackupSettingsNameLeavesItOut(t *testing.T) {
	table := exportTable()
	table.BackupSettings = nil
	m, err := ExportManifest(table, exportDetail(t), exportNames(), false)
	if err != nil {
		t.Fatalf("ExportManifest: %v", err)
	}
	if m.Spec.BackupSettings != nil {
		t.Errorf("spec.backup_settings = %q, want it left out", *m.Spec.BackupSettings)
	}
	if strings.Contains(string(exportYAML(t, m)), "backup_settings:") {
		t.Errorf("yaml still names spec.backup_settings:\n%s", exportYAML(t, m))
	}
	if m.Status.BackupSettingsID != 4 {
		t.Errorf("status.backup_settings_id = %d, want 4", m.Status.BackupSettingsID)
	}
}

// The export, written as the command writes it, loads as apply loads a
// manifest and compares equal to the revision it came from.
func TestExportManifest_RoundTripsThroughLoadAndDiff(t *testing.T) {
	detail := exportDetail(t)
	table := exportTable()
	table.Name = "Monthly orders · Nov 2026"
	m, err := ExportManifest(table, detail, exportNames(), false)
	if err != nil {
		t.Fatal(err)
	}

	encodedJSON, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode json: %v", err)
	}
	for format, data := range map[string][]byte{"yaml": exportYAML(t, m), "json": encodedJSON} {
		loaded, err := LoadManifest(data)
		if err != nil {
			t.Fatalf("%s: LoadManifest: %v\n%s", format, err, data)
		}
		ids := map[string]int{"raw_orders": 1, "raw_customers": 2}
		changes, err := UpdateChanges(loaded, detail.Revision, detail.State, ids, exportNames(), nil)
		if err != nil {
			t.Fatalf("%s: UpdateChanges: %v", format, err)
		}
		assertChangeLines(t, changeLines(changes), nil)

		request, err := loaded.ToAPIRequest()
		if err != nil {
			t.Fatalf("%s: ToAPIRequest: %v", format, err)
		}
		if request.Metadata.Name != detail.Group.Name {
			t.Errorf("%s: request metadata.name = %q, want group name %q", format, request.Metadata.Name, detail.Group.Name)
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("%s: encode request: %v", format, err)
		}
		if want := `"constant":[1,2.5,9223372036854775807,"gold",true,{"n":0},2.0,100000000000000000000.0]`; !strings.Contains(string(body), want) {
			t.Errorf("%s: request body lacks %s:\n%s", format, want, body)
		}
		if !strings.Contains(string(body), `"release":null`) {
			t.Errorf("%s: request body sends a release:\n%s", format, body)
		}
	}
}

func TestExportManifest_YAMLShape(t *testing.T) {
	out := string(exportYAML(t, export(t, exportDetail(t))))
	for _, want := range []string{
		"kind: LinkedTable\n",
		"        customer_id: customer_id\n",
		"          source_fields:\n            - region_code\n",
		"          constant:\n            - 1\n            - 2.5\n            - 9223372036854775807\n",
		"            - \"n\": 0\n            - 2.0\n            - 100000000000000000000.0\n",
		"status:\n  table_id: 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml lacks %q:\n%s", want, out)
		}
	}
	// A clause the server stored with spine_fields: [] writes none.
	if strings.Contains(out, "spine_fields: []") {
		t.Errorf("yaml writes an empty spine_fields:\n%s", out)
	}
}

// yaml.v3 alone writes a json.Number as a quoted string.
func TestJoinField_MarshalYAMLWritesNumbersAsNumbers(t *testing.T) {
	field, err := joinFieldFromJSON(json.RawMessage(`{"source_fields":["tier"],"constant":[18446744073709551615]}`))
	if err != nil {
		t.Fatalf("joinFieldFromJSON: %v", err)
	}
	var buf bytes.Buffer
	encoder := jsonnum.NewYAMLEncoder(&buf)
	if err := encoder.Encode(map[string]JoinField{"tier": field}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "- 18446744073709551615\n") {
		t.Errorf("constant is not written as an integer:\n%s", out)
	}
}

// Marshaling must not replace the json.Numbers the export holds.
func TestJoinField_MarshalYAMLLeavesTheClauseAlone(t *testing.T) {
	m := export(t, exportDetail(t))
	exportYAML(t, m)
	constant := m.Spec.Sources["customers"].JoinFields["tier"].Clause.Constant.([]interface{})
	if _, ok := constant[2].(json.Number); !ok {
		t.Errorf("constant[2] is %T after encoding, want json.Number", constant[2])
	}
}

func TestExportManifest_SpineMustBeExactlyOneSource(t *testing.T) {
	for name, spine := range map[string]int{"none": 99, "two": 2} {
		detail := exportDetail(t)
		detail.Group.SpineDatasetID = spine
		if spine == 2 {
			detail.Revision.Sources["customers_again"] = RevisionSource{DatasetID: 2}
		}
		_, err := ExportManifest(exportTable(), detail, exportNames(), false)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitAPIIncompatible {
			t.Errorf("%s: exit code %d (err %v), want %d", name, code, err, qerrors.ExitAPIIncompatible)
		}
	}
}

func TestExportManifest_UnknownClauseKeyIsAnError(t *testing.T) {
	detail := exportDetail(t)
	detail.Revision.Sources["customers"].JoinFields["region"] = json.RawMessage(`{"source_fields":["region_code"],"collation":"C"}`)
	_, err := ExportManifest(exportTable(), detail, exportNames(), false)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitAPIIncompatible {
		t.Fatalf("exit code %d (err %v), want %d", code, err, qerrors.ExitAPIIncompatible)
	}
	if !strings.Contains(err.Error(), "sources.customers.join_fields.region") {
		t.Errorf("error does not name the join field: %v", err)
	}
}

func TestSourceDatasetIDs(t *testing.T) {
	detail := exportDetail(t)
	detail.Revision.Sources["customers_again"] = RevisionSource{DatasetID: 2}
	if got := SourceDatasetIDs(detail); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("SourceDatasetIDs = %v, want [1 2]", got)
	}
}

func TestLoadManifest_StatusIsIgnoredButStrict(t *testing.T) {
	base := string(exportYAML(t, export(t, exportDetail(t))))
	if _, err := LoadManifest([]byte(base)); err != nil {
		t.Fatalf("export with status: %v", err)
	}
	for name, data := range map[string]string{
		"unknown status key":    base + "  owner: someone\n",
		"unknown top-level key": base + "statuss: {}\n",
	} {
		_, err := LoadManifest([]byte(data))
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit code %d (err %v), want %d", name, code, err, qerrors.ExitBadUsage)
		}
	}
}

func TestSummaryOf(t *testing.T) {
	got := *SummaryOf(export(t, exportDetail(t)))
	want := Summary{Name: "orders_enriched", TableID: 7, Release: "v3", State: "enabled", Spine: "orders", Sources: 2, OutputColumns: 2, Destination: "warehouse"}
	if got != want {
		t.Errorf("SummaryOf = %+v, want %+v", got, want)
	}
}
