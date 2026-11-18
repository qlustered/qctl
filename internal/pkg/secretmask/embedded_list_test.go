package secretmask_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"gopkg.in/yaml.v3"
)

func TestPrintersPreserveEmbeddedListEnvelope(t *testing.T) {
	type item struct {
		ID       uint64 `json:"id"`
		Password string `json:"password"`
	}
	type envelope struct {
		output.List
		Series *api.TableSeriesTinySchema `json:"series,omitempty"`
	}
	series := &api.TableSeriesTinySchema{
		ID: uuid.MustParse("5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"), Name: "Monthly",
	}
	for _, format := range []string{"json", "yaml"} {
		for _, allow := range []bool{false, true} {
			for _, series := range []*api.TableSeriesTinySchema{nil, series} {
				t.Run(fmt.Sprintf("%s/plaintext=%t/series=%t", format, allow, series != nil), func(t *testing.T) {
					page, limit, total := 1, 20, 1
					data := envelope{
						List: output.NewList([]item{{ID: 9007199254740993, Password: "list-secret"}},
							&page, &limit, &total, nil),
						Series: series,
					}
					password := "***"
					if allow {
						password = "list-secret"
					}
					want := data
					want.Items = []item{{ID: 9007199254740993, Password: password}}
					wantJSON := canonicalJSON(t, want)
					for _, name := range []string{"output", "printer"} {
						var buf bytes.Buffer
						var err error
						if name == "output" {
							err = output.NewPrinter(output.Options{
								Writer: &buf, Format: output.Format(format), AllowPlaintextSecrets: allow,
							}).Print(data)
						} else {
							err = printer.NewPrinter(printer.Options{
								Writer: &buf, Format: printer.Format(format), AllowPlaintextSecrets: allow,
							}).Print(data)
						}
						if err != nil {
							t.Fatalf("%s.Print: %v", name, err)
						}
						var tree any
						if format == "yaml" {
							if err := yaml.Unmarshal(buf.Bytes(), &tree); err != nil {
								t.Fatal(err)
							}
						} else {
							decoder := json.NewDecoder(&buf)
							decoder.UseNumber()
							if err := decoder.Decode(&tree); err != nil {
								t.Fatal(err)
							}
						}
						if got := canonicalJSON(t, tree); got != wantJSON {
							t.Errorf("%s:\n got: %s\nwant: %s", name, got, wantJSON)
						}
					}
				})
			}
		}
	}
}

func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&tree); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
