package describe

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

func TestDescribeAlertShowsHeldEmailFromAddress(t *testing.T) {
	for name, address := range map[string]*string{
		"present": drPtr("sender@broker.example"),
		"absent":  nil,
		"empty":   drPtr(""),
	} {
		for _, verbosity := range []string{"-v=0", "-v=1"} {
			for _, format := range []string{"yaml", "json", "table"} {
				t.Run(name+"/"+verbosity+"/"+format, func(t *testing.T) {
					a := sampleAlertFull()
					a.IssueType = api.AlertTypeInboundEmailHeld
					// The sender alone must keep the body visible.
					a.Body = &api.AlertBodySchema{FromAddress: address}
					out := runDescribeAlert(t, a, "describe", "alert", "1", verbosity, "-o", format, "--max-column-width", "2000")
					if address == nil || *address == "" {
						if strings.Contains(out, "from_address") {
							t.Errorf("output includes an unset sender:\n%s", out)
						}
						return
					}
					if format == "table" {
						if !strings.Contains(out, `"from_address":"`+*address+`"`) {
							t.Errorf("table lacks the sender:\n%s", out)
						}
						return
					}
					var doc struct {
						Spec struct {
							Body map[string]any `yaml:"body"`
						} `yaml:"spec"`
					}
					if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
						t.Fatal(err)
					}
					if got := doc.Spec.Body["from_address"]; got != *address {
						t.Errorf("spec.body.from_address = %v, want %q", got, *address)
					}
				})
			}
		}
	}
}

// A held file's row_drop_share keeps its precision in each describe view.
func TestDescribeAlertPrintsAHeldFilesRowDropShareAsSent(t *testing.T) {
	share := 0.123456789
	findings := []api.PreloadCheckFinding{{Reason: api.RowDrop, RowCount: drPtr(40), BaselineRowCount: drPtr(1000), RowDropShare: &share}}
	a := sampleAlertFull()
	a.IssueType = api.AlertTypePreloadCheckHeld
	a.Body = &api.AlertBodySchema{Findings: &findings}

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"default yaml": {[]string{"describe", "alert", "1"}, "row_drop_share: 0.123456789\n"},
		"verbose yaml": {[]string{"describe", "alert", "1", "-v"}, "row_drop_share: 0.123456789\n"},
		"json":         {[]string{"describe", "alert", "1", "-o", "json"}, `"row_drop_share": 0.123456789` + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			out := runDescribeAlert(t, a, tc.args...)
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
			if strings.Contains(out, "routed_period_key") {
				t.Errorf("row_drop finding carries period_mismatch keys:\n%s", out)
			}
		})
	}
}
