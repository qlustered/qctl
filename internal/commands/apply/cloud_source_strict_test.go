package apply

import (
	"io"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cloud_sources"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// csStrictLive is sampleCloudSourceFull with a CSV delimiter and a
// null_values override set, so a manifest that loses either key would clear
// it.
func csStrictLive() *cloud_sources.CloudSourceFull {
	live := sampleCloudSourceFull()
	live.CsvDelimiter = csConfirmPtr(";")
	live.NullValuesOverride = &[]string{"NA"}
	return live
}

// csStrictApply runs the command words via, followed by the path of
// manifestYAML and --yes, against s and returns the command's error.
func csStrictApply(t *testing.T, s *cloudSourceServer, via []string, manifestYAML string) error {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	args := append(append([]string{}, via...), s.env.CreateFile("cs.yaml", manifestYAML), "--yes")
	root.SetArgs(args)
	return root.Execute()
}

// A key the CloudSource manifest does not declare is refused with exit 2 and
// nothing is written, through `apply cloud-source -f` and `apply -f` alike.
// Each bad manifest is the matching one with one key misspelled or misplaced;
// read leniently, it would leave that key's live value out of the manifest and
// clear it. --yes is given so that a lenient read reaches the PATCH instead of
// stopping at the confirmation.
func TestApplyCloudSourceRefusesAnUnknownKey(t *testing.T) {
	const matching = `  csv_delimiter: ";"
  settings_overrides:
    null_values: ["NA"]
`
	cases := []struct {
		name string
		key  string
		spec string
	}{
		{"unknown spec key", "csv_delimeter", `  csv_delimeter: ";"
  settings_overrides:
    null_values: ["NA"]
`},
		{"unknown settings_overrides key", "null_value", `  csv_delimiter: ";"
  settings_overrides:
    null_value: ["NA"]
`},
		{"wire-name override at the top of spec", "null_values_override", `  csv_delimiter: ";"
  null_values_override: ["NA"]
`},
	}

	for _, via := range [][]string{
		{"apply", "cloud-source", "-f"},
		{"apply", "-f"},
	} {
		entry := strings.Join(via[:len(via)-1], " ")

		t.Run(entry+"/control", func(t *testing.T) {
			server := newCloudSourceServer(t, csStrictLive())
			forbidCloudSourcePrompt(t)
			if err := csStrictApply(t, server, via, s3OrdersManifest(matching)); err != nil {
				t.Fatalf("the matching manifest was refused: %v", err)
			}
			if writes := server.writes(); len(writes) > 0 {
				t.Fatalf("the matching manifest sent %v", writes)
			}
		})

		for _, tc := range cases {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				server := newCloudSourceServer(t, csStrictLive())
				forbidCloudSourcePrompt(t)
				err := csStrictApply(t, server, via, s3OrdersManifest(tc.spec))
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
				}
				if writes := server.writes(); len(writes) > 0 {
					t.Errorf("a manifest with an unknown key sent %v", writes)
				}
				if err != nil && !strings.Contains(err.Error(), tc.key) {
					t.Errorf("the refusal does not name %s: %v", tc.key, err)
				}
			})
		}
	}
}
