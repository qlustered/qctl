package inspect

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// printResult (internal/commands/inspect/dry_run_job.go) renders YAML for -o
// yaml and JSON for everything else, default included. There is no table view
// to ask for.
func TestInspectDryRunJobDeclaresJSONAndYAMLOnly(t *testing.T) {
	got := strings.Join(cmdutil.SupportedOutputFormats(NewDryRunJobCommand()), ",")
	if got != "json,yaml" {
		t.Errorf("supports %q, want \"json,yaml\"", got)
	}
	err := cmdutil.ValidateOutputFormatValue("table", cmdutil.SupportedOutputFormats(NewDryRunJobCommand()))
	if err == nil {
		t.Fatal("-o table was accepted; inspect has no table view")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The examples page is printed as JSON by default and as YAML for -o yaml; it
// has no table view either.
func TestInspectCompositeDryRunJobDeclaresJSONAndYAMLOnly(t *testing.T) {
	got := strings.Join(cmdutil.SupportedOutputFormats(NewCompositeDryRunJobCommand()), ",")
	if got != "json,yaml" {
		t.Errorf("supports %q, want \"json,yaml\"", got)
	}
}
