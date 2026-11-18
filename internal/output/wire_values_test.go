package output

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

const (
	wireUUID    = "5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"
	wireMetrics = `{"total_eligible": 1000, "clean": {"rate": 1.0, "count": 1000}, "connection": {"password": "hunter2"}}`
)

type wireRecord struct {
	ID       openapi_types.UUID               `json:"id"`
	OwnerID  *openapi_types.UUID              `json:"owner_id"`
	Metrics  *api.DryRunJobFullSchema_Metrics `json:"metrics"`
	Password string                           `json:"password"`
}

func newWireRecord(t *testing.T) wireRecord {
	t.Helper()
	id := uuid.MustParse(wireUUID)
	var metrics api.DryRunJobFullSchema_Metrics
	if err := metrics.UnmarshalJSON([]byte(wireMetrics)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	return wireRecord{ID: id, OwnerID: &id, Metrics: &metrics, Password: "hunter2"}
}

// decodeTree decodes out as format and re-encodes it through encoding/json,
// so JSON and YAML output compare as one tree.
func decodeTree(t *testing.T, out []byte, format Format) interface{} {
	t.Helper()
	var tree interface{}
	var err error
	if format == FormatYAML {
		err = yaml.Unmarshal(out, &tree)
	} else {
		err = json.Unmarshal(out, &tree)
	}
	if err != nil {
		t.Fatalf("decoding %s: %v\n%s", format, err, out)
	}
	encoded, _ := json.Marshal(tree)
	var normalized interface{}
	_ = json.Unmarshal(encoded, &normalized)
	return normalized
}

func TestPrinter_StructuredFormatsWriteUUIDsAndUnionsAsTheWire(t *testing.T) {
	masked := `{"id": "` + wireUUID + `", "owner_id": "` + wireUUID + `", "password": "***",
		"metrics": {"total_eligible": 1000, "clean": {"rate": 1.0, "count": 1000}, "connection": {"password": "***"}}}`
	plain := `{"id": "` + wireUUID + `", "owner_id": "` + wireUUID + `", "password": "hunter2", "metrics": ` + wireMetrics + `}`

	for _, format := range []Format{FormatJSON, FormatYAML} {
		for _, allow := range []bool{false, true} {
			want := masked
			if allow {
				want = plain
			}
			var buf bytes.Buffer
			printer := NewPrinter(Options{Format: format, Writer: &buf, AllowPlaintextSecrets: allow})
			if err := printer.Print(newWireRecord(t)); err != nil {
				t.Fatalf("%s: Print() error = %v", format, err)
			}
			got := decodeTree(t, buf.Bytes(), format)
			var wantTree interface{}
			if err := json.Unmarshal([]byte(want), &wantTree); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, wantTree) {
				t.Errorf("%s, AllowPlaintextSecrets=%v:\n got: %s\nwant: %s", format, allow, buf.String(), want)
			}
		}
	}
}

func TestPrinter_TableAndNamesPrintAUUIDAsItsText(t *testing.T) {
	type idOnly struct {
		ID      openapi_types.UUID  `json:"id"`
		OwnerID *openapi_types.UUID `json:"owner_id"`
	}
	id := uuid.MustParse(wireUUID)
	data := []idOnly{{ID: id, OwnerID: &id}}

	var table bytes.Buffer
	if err := NewPrinter(Options{Format: FormatTable, Writer: &table, Columns: []string{"id", "owner_id"}}).Print(data); err != nil {
		t.Fatalf("table: Print() error = %v", err)
	}
	if got := strings.Count(table.String(), wireUUID); got != 2 {
		t.Errorf("table should print the UUID text in both columns, got:\n%s", table.String())
	}

	var names bytes.Buffer
	if err := NewPrinter(Options{Format: FormatName, Writer: &names, NameKind: "table-rule"}).Print(data); err != nil {
		t.Fatalf("name: Print() error = %v", err)
	}
	if got, want := strings.TrimSpace(names.String()), "table-rule/"+wireUUID; got != want {
		t.Errorf("-o name = %q, want %q", got, want)
	}
}
