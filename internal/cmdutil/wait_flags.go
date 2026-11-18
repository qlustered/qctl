package cmdutil

import (
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// DefaultWaitTimeoutSeconds is --wait-timeout's default.
const DefaultWaitTimeoutSeconds = 900

// AddWaitFlags registers --wait and --wait-timeout. what completes the --wait
// help text: "Wait for " + what.
func AddWaitFlags(cmd *cobra.Command, what string) {
	cmd.Flags().Bool("wait", false, "Wait for "+what)
	cmd.Flags().Int("wait-timeout", DefaultWaitTimeoutSeconds, "Seconds --wait waits before giving up")
}

// WaitFlags reads the flags AddWaitFlags registered. It returns (timeout,
// wait, error): wait reports --wait, and timeout is --wait-timeout when it
// does. --wait-timeout without --wait, or below 1, is a bad-usage error.
func WaitFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return 0, false, err
	}
	if !wait && cmd.Flags().Changed("wait-timeout") {
		return 0, false, qerrors.NewBadUsage("--wait-timeout requires --wait")
	}
	seconds, err := PositiveSecondsFlag(cmd, "wait-timeout")
	if err != nil {
		return 0, false, err
	}
	if !wait {
		return 0, false, nil
	}
	return time.Duration(seconds) * time.Second, true, nil
}
