package describe

import (
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Every describe command declares its output formats.
func TestEveryDescribeCommandDeclaresItsFormats(t *testing.T) {
	for _, c := range NewCommand().Commands() {
		want := "table,json,yaml"
		if c.Name() == "table-series" {
			want += ",name"
		}
		if got := c.Annotations[cmdutil.AnnotationOutputFormats]; got != want {
			t.Errorf("describe %s declares %q, want %q", c.Name(), got, want)
		}
	}
}

func TestDescribeRejectsNameWithExitTwo(t *testing.T) {
	cmd := NewDatasetCommand()
	err := cmdutil.ValidateOutputFormatValue("name", cmdutil.SupportedOutputFormats(cmd))
	if err == nil {
		t.Fatal("describe table -o name was accepted; it renders the text summary, not a name list")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}
