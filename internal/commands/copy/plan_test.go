package copycmd

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
)

// A plan cell shows null apart from empty, and never passes a tab, a line
// break or a terminal control through to the table.
func TestPlanValue(t *testing.T) {
	tests := []struct {
		name string
		in   *string
		want string
	}{
		{"null", nil, "(unset)"},
		{"empty", strPtr(""), `""`},
		{"plain", strPtr("*.csv"), "*.csv"},
		{"space", strPtr("in bound/"), `"in bound/"`},
		{"comma", strPtr("a,b"), `"a,b"`},
		{"quote", strPtr(`say "hi"`), `"say \"hi\""`},
		{"tab", strPtr("a\tb"), `"a\tb"`},
		{"line break", strPtr("a\nb"), `"a\nb"`},
		{"escape", strPtr("a\x1b[31mb"), `"a\x1b[31mb"`},
		{"bidi override", strPtr("a\u202eb"), `"a\u202eb"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planValue(tt.in); got != tt.want {
				t.Errorf("planValue = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPlanCellKeepsOneLine(t *testing.T) {
	if got, want := planCell("orders\tEU\nsource\x1b"), `orders EU source\x1b`; got != want {
		t.Errorf("planCell = %q, want %q", got, want)
	}
}

func TestCopyCellMarksSecretsSupplied(t *testing.T) {
	specs := []dataSourceSpec{
		{Entry: 1, ID: 12, Values: map[string]string{"s3_prefix": "eu/"}, Secrets: map[string]string{"s3_secret_key": "hidden"}},
		{Entry: 2, ID: 13, Values: map[string]string{}, Secrets: map[string]string{}},
		{Entry: 3, ID: 12, Values: map[string]string{"name": "second copy"}, Secrets: map[string]string{
			"sftp_ssh_key_passphrase": "hidden", "sftp_ssh_key": "hidden",
		}},
	}
	want := `yes: s3_prefix=eu/, s3_secret_key=(supplied); ` +
		`yes: name="second copy", sftp_ssh_key=(supplied), sftp_ssh_key_passphrase=(supplied)`
	if got := copyCell(12, specs); got != want {
		t.Errorf("copyCell(12) = %s, want %s", got, want)
	}
	if got := copyCell(13, specs); got != "yes" {
		t.Errorf("copyCell(13) = %s, want yes", got)
	}
}

// The LOCATION cell of a simple_url source says the URL is withheld, never
// that it is unset.
func TestCopyPlanWithholdsASimpleURLLocation(t *testing.T) {
	plan := &copyTablePlan{offered: &datasets.CopyOptions{
		DataSources: []api.DatasetCopyDataSourceInfo{simpleURLActive(), s3Active()},
	}}
	var b strings.Builder
	writeCopyPlanDataSources(&b, plan)
	lines := strings.Split(b.String(), "\n")
	if len(lines) < 3 {
		t.Fatalf("plan:\n%s", b.String())
	}
	if !strings.Contains(lines[1], "simple_url=(withheld)") || strings.Contains(lines[1], "simple_url="+unsetValue) {
		t.Errorf("simple_url row = %q, want simple_url=(withheld)", lines[1])
	}
	if !strings.Contains(lines[2], "s3_bucket=acme-drop") {
		t.Errorf("s3 row = %q, want s3_bucket=acme-drop", lines[2])
	}
}

// The SECRETS cell names what a source holds, one cell whatever the names
// contain, or "-" when it holds none.
func TestPlanNames(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{"none", nil, "-"},
		{"empty", []string{}, "-"},
		{"one", []string{"simple_url"}, "simple_url"},
		{"several", []string{"gpg_passphrase", "sftp_password"}, "gpg_passphrase, sftp_password"},
		{"a control character", []string{"a\tb\x1b"}, `a b\x1b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planNames(tt.names); got != tt.want {
				t.Errorf("planNames = %q, want %q", got, tt.want)
			}
		})
	}
}

// maskSecrets masks a copy and leaves the request the POST sends as it was.
func TestMaskSecretsLeavesTheRequestAlone(t *testing.T) {
	sources := []api.DatasetCopyDataSourceRequest{
		newDataSourceRequest(dataSourceSpec{ID: 12, Secrets: map[string]string{"s3_secret_key": "real", "simple_url": "https://x"}}),
		{SourceID: 13},
	}
	request := datasets.CopyRequest{Name: strPtr("copy"), DataSources: &sources}
	masked := maskSecrets(request)
	got := (*masked.DataSources)[0]
	if got.S3SecretKey == nil || *got.S3SecretKey != dryRunSecretMask || got.SimpleURL == nil || *got.SimpleURL != dryRunSecretMask {
		t.Error("the masked copy does not mask both supplied secrets")
	}
	if got.SftpPassword != nil || (*masked.DataSources)[1].S3SecretKey != nil {
		t.Error("the masked copy sets a secret that was not supplied")
	}
	if *sources[0].S3SecretKey != "real" || *sources[0].SimpleURL != "https://x" {
		t.Error("masking changed the request the POST sends")
	}
	if masked := maskSecrets(datasets.CopyRequest{Name: strPtr("copy")}); masked.DataSources != nil {
		t.Error("a request without data sources gained some")
	}
}
