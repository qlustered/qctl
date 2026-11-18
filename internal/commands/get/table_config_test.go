package get

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/datasets"
	"gopkg.in/yaml.v3"
)

func runGetTableConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runGetTableCmd(t, NewTableConfigCommand(), "table-config", args...)
}

func TestGetTableConfig_Default(t *testing.T) {
	out, err := runGetTableConfig(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Table:            trips (id: 12)",
		"Migration policy: ask_user",
		"SETTINGS MODEL",
		"array_delimiters:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NOT ANOMALIES PER COLUMN") || strings.Contains(out, "N/A") {
		t.Errorf("default output must omit the potentially large not-anomaly ledger; got:\n%s", out)
	}
}

func TestGetTableConfig_Structured(t *testing.T) {
	out, err := runGetTableConfig(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var c datasets.TableConfig
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("not a TableConfig: %v\n%s", err, out)
	}
	if c.SettingsModel == nil {
		t.Errorf("structured output must carry the settings model; got %+v", c)
	}
	if c.NotAnomaliesPerColumn != nil || strings.Contains(out, "not_anomalies_per_column") {
		t.Errorf("structured output must omit not-anomaly values unless requested; got %s", out)
	}
	if strings.Contains(out, "apiVersion") {
		t.Errorf("table-config is not a manifest; got:\n%s", out)
	}
}

// The text view and -o yaml print a 7-digit casting default as an integer.
func TestGetTableConfig_SettingsModelKeepsIntegers(t *testing.T) {
	for _, args := range [][]string{{"12"}, {"12", "-o", "yaml"}} {
		out, err := runGetTableCmdServing(t, getTableCastingDefaultJSON, NewTableConfigCommand(), "table-config", args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(out, "fare: 1234567\n") {
			t.Errorf("%v should print fare: 1234567, got:\n%s", args, out)
		}
	}
}

func TestGetTableConfig_ShowNotAnomalies(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		out, err := runGetTableConfig(t, "12", "--show-not-anomalies")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"NOT ANOMALIES PER COLUMN",
			"fare:",
			"- 12.5",
			"status:",
			"- N/A",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q; got:\n%s", want, out)
			}
		}
	})

	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, err := runGetTableConfig(t, "12", "--show-not-anomalies", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var c datasets.TableConfig
			if format == "json" {
				err = json.Unmarshal([]byte(out), &c)
			} else {
				err = yaml.Unmarshal([]byte(out), &c)
			}
			if err != nil {
				t.Fatalf("not a TableConfig: %v\n%s", err, out)
			}
			if c.NotAnomaliesPerColumn == nil || (*c.NotAnomaliesPerColumn)["fare"] == nil {
				t.Errorf("not-anomaly values missing from %s output: %s", format, out)
			}
		})
	}
}

// A table name is resolved the same way every other table-addressing command
// resolves one.
func TestGetTableConfig_ByName(t *testing.T) {
	out, err := runGetTableConfig(t, "trips")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Table:            trips (id: 12)") {
		t.Errorf("name lookup failed; got:\n%s", out)
	}
}

// --table is a name and never an ID, which is the whole point of the pair.
func TestGetTableConfig_ByFlagPair(t *testing.T) {
	for _, args := range [][]string{{"--table", "trips"}, {"--table-id", "12"}} {
		out, err := runGetTableConfig(t, args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(out, "Table:            trips (id: 12)") {
			t.Errorf("%v did not resolve the table; got:\n%s", args, out)
		}
	}
}
