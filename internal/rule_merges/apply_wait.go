package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// ApplyWait configures WaitForApply.
type ApplyWait struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Describe is the command that reads the merge back; the wait's banner
	// and its errors name it.
	Describe string
	// Until, when set, receives each poll of an applying merge, and the wait
	// ends at the first one it reports true for. Its error fails that poll.
	Until func(ctx context.Context, m *RuleMerge) (bool, error)
}

// WaitForApply polls merge mergeID into targetDatasetID with Wait inside a
// cmdutil.StartWait on cmd, printing progress to cmd's stderr. It returns the
// merge the wait ended at with the body of the last successful poll (nil when
// none succeeded), and what Waiter.Ended makes of the wait's error.
func (c *Client) WaitForApply(cmd *cobra.Command, accessToken string, targetDatasetID, mergeID int, opts ApplyWait) (*RuleMerge, []byte, error) {
	var lastBody []byte
	var until *RuleMerge
	poll := func(ctx context.Context) (*RuleMerge, error) {
		m, body, err := c.GetContext(ctx, accessToken, targetDatasetID, mergeID)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if opts.Until != nil && m.State == api.DatasetCopyStateApplying {
			done, err := opts.Until(ctx, m)
			if err != nil {
				return nil, err
			}
			if done {
				until = m
			}
		}
		lastBody = body
		return m, nil
	}
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    opts.Timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for rule merge %d to finish applying", mergeID),
		Next:      opts.Describe,
		NextVerb:  "check with",
	})
	defer w.Stop()
	final, err := Wait(ctx, poll, mergeID, WaitOptions{
		Timeout:  opts.Timeout,
		Progress: func(m *RuleMerge) { WriteProgress(cmd.ErrOrStderr(), m) },
		Describe: opts.Describe,
		Until:    func(m *RuleMerge) bool { return until != nil && m == until },
	})
	return final, lastBody, w.Ended(err)
}
