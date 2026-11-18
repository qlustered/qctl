package datasets

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableConfigFromAPIResponse_CarriesTheSettingsModel(t *testing.T) {
	detect := true
	notAnomalies := map[string]any{"status": []any{"N/A"}}
	resp := &DatasetFull{
		ID: 12, Name: "trips",
		MigrationPolicy: "ask_user", DataLoadingProcess: "append_skip_identical_rows",
		BackupSettingsID: 9, BackupKeyFormat: "{source}/{date}",
		AnomalyThreshold: 5, MaxRetryCount: 3, MaxTriesToFixJSON: 2,
		DetectAnomalies:       &detect,
		SettingsModel:         SettingsSchema{ID: 7},
		NotAnomaliesPerColumn: &notAnomalies,
	}

	c, err := TableConfigFromAPIResponse(resp, false)
	if err != nil {
		t.Fatalf("TableConfigFromAPIResponse: %v", err)
	}

	if c.Name != "trips" || c.ID != 12 {
		t.Errorf("identity = %q/%d, want trips/12", c.Name, c.ID)
	}
	if c.MigrationPolicy != "ask_user" || c.BackupSettingsID != 9 {
		t.Errorf("config = %+v, want the table's policy and backup settings", c)
	}
	if c.SettingsModel == nil {
		t.Error("settings model must be carried; it is the reason this command exists")
	}
	if c.NotAnomaliesPerColumn != nil {
		t.Error("not-anomaly values must stay out of the default projection")
	}

	c, err = TableConfigFromAPIResponse(resp, true)
	if err != nil {
		t.Fatalf("TableConfigFromAPIResponse with not-anomalies: %v", err)
	}
	if c.NotAnomaliesPerColumn == nil || (*c.NotAnomaliesPerColumn)["status"] == nil {
		t.Errorf("not-anomaly values missing from explicit projection: %+v", c.NotAnomaliesPerColumn)
	}
}

func TestTableConfigFromAPIResponse_RequestedMissingNotAnomaliesIsExplicit(t *testing.T) {
	c, err := TableConfigFromAPIResponse(&DatasetFull{}, true)
	if err != nil {
		t.Fatalf("TableConfigFromAPIResponse: %v", err)
	}
	if c.NotAnomaliesPerColumn == nil {
		t.Fatal("requested not-anomaly field was omitted")
	}
	if *c.NotAnomaliesPerColumn != nil {
		t.Errorf("missing backend field = %#v, want explicit null", *c.NotAnomaliesPerColumn)
	}
}

func TestRenderTableConfig_Text(t *testing.T) {
	delims := []string{",", "|"}
	c := &TableConfig{
		Name: "trips", ID: 12,
		MigrationPolicy: "ask_user", DataLoadingProcess: "append_skip_identical_rows",
		BackupSettingsID: 9, BackupKeyFormat: "{source}/{date}",
		SettingsModel: &SettingsModel{ArrayDelimiters: &delims},
	}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableConfig(cmd, c); err != nil {
		t.Fatalf("RenderTableConfig: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Table:            trips (id: 12)",
		"Migration policy: ask_user",
		"Loading:          append_skip_identical_rows",
		"Backup:           settings 9, key format {source}/{date}",
		"SETTINGS MODEL",
		"array_delimiters:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRenderTableConfigSanitizesServerText(t *testing.T) {
	columns := []string{"account\u009bnumber"}
	c := &TableConfig{
		Name: "trips\x1b[31m", ID: 12,
		MigrationPolicy: "ask\x00user", DataLoadingProcess: "append\u0085dedupe",
		BackupKeyFormat:            "{source}\x7f/{date}",
		ColumnsForEntityResolution: &columns,
	}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableConfig(cmd, c); err != nil {
		t.Fatalf("RenderTableConfig: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x00') || strings.ContainsRune(out, '\x7f') || strings.ContainsRune(out, '\u0085') || strings.ContainsRune(out, '\u009b') {
		t.Fatalf("config retained a terminal control: %q", out)
	}
	for _, want := range []string{`trips\x1b[31m`, `ask\x00user`, `append\x85dedupe`, `{source}\x7f/{date}`, `account\x9bnumber`} {
		if !strings.Contains(out, want) {
			t.Errorf("config missing visible escape %q: %q", want, out)
		}
	}
}

func TestRenderTableConfig_NoSettingsModel(t *testing.T) {
	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableConfig(cmd, &TableConfig{Name: "trips", ID: 12}); err != nil {
		t.Fatalf("RenderTableConfig: %v", err)
	}
	if !strings.Contains(buf.String(), "SETTINGS MODEL\n  (none)") {
		t.Errorf("a table with no settings model must say so; got:\n%s", buf.String())
	}
}

// TestRenderTableConfig_PrintsEmptySettings pins the decision recorded on
// RenderTableConfig: an emptied setting is printed as [] or {}, not filtered out
// for being empty.
//
// encrypt_columns is the one worth naming. It is NOT nullable on the wire, so
// before the read became faithful it was dropped from this view whenever it was
// empty, while a nullable sibling like datetime_formats printed its [] -- two
// keys the CLI presents identically, behaving differently. Both print now.
// Making this view quieter again means taking that back and disagreeing with
// `get table -o yaml` about the same table.
func TestRenderTableConfig_PrintsEmptySettings(t *testing.T) {
	empty := []string{}
	emptyMap := map[string][]string{}
	c := &TableConfig{
		Name: "trips", ID: 12,
		SettingsModel: &SettingsModel{
			EncryptColumns:       &empty,
			DatetimeFormats:      &empty,
			BooleanTruePerColumn: &emptyMap,
		},
	}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableConfig(cmd, c); err != nil {
		t.Fatalf("RenderTableConfig: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"encrypt_columns: []",
		"datetime_formats: []",
		"boolean_true_per_column: {}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: an emptied setting must render as empty, not vanish; got:\n%s", want, out)
		}
	}
}
