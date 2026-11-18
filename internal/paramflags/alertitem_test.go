package paramflags

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func alertItemCmd(withIsBadData bool) *cobra.Command {
	cmd := &cobra.Command{Use: "data"}
	RegisterAlertItemIDFlag(cmd)
	if withIsBadData {
		cmd.Flags().Bool(isBadDataFlag, false, "")
	}
	return cmd
}

func TestAlertItemIDFilterUnsetIsNotAnError(t *testing.T) {
	id, ok, err := AlertItemIDFilter(alertItemCmd(true))
	if err != nil || ok || id != 0 {
		t.Errorf("unset = (%d, %v, %v), want (0, false, nil)", id, ok, err)
	}
}

func TestAlertItemIDFilterRequiresIsBadData(t *testing.T) {
	cmd := alertItemCmd(true)
	if err := cmd.Flags().Set(AlertItemIDFlag, "8817"); err != nil {
		t.Fatal(err)
	}
	_, _, err := AlertItemIDFilter(cmd)
	if err == nil {
		t.Fatal("expected a refusal without --is-bad-data")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (err: %v)", code, err)
	}

	if err := cmd.Flags().Set(isBadDataFlag, "true"); err != nil {
		t.Fatal(err)
	}
	id, ok, err := AlertItemIDFilter(cmd)
	if err != nil || !ok || id != 8817 {
		t.Errorf("with --is-bad-data = (%d, %v, %v), want (8817, true, nil)", id, ok, err)
	}
}

// A command that never registered --is-bad-data would otherwise read it back as
// false and refuse every --alert-item-id with advice the user cannot act on.
// That is a wiring bug, not a usage error, so it must not exit 2.
func TestAlertItemIDFilterReportsAMissingIsBadDataFlag(t *testing.T) {
	cmd := alertItemCmd(false)
	if err := cmd.Flags().Set(AlertItemIDFlag, "8817"); err != nil {
		t.Fatal(err)
	}
	_, _, err := AlertItemIDFilter(cmd)
	if err == nil {
		t.Fatal("expected an internal error")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("error = %v, want an internal-error report", err)
	}
	if code := qerrors.GetExitCode(err); code == 2 {
		t.Errorf("a wiring bug must not read as a usage error (exit 2): %v", err)
	}
}
