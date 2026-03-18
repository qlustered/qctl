package get

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// watchPoll runs a polling watch loop that redraws output in place on TTY.
// renderFn is called each tick and should return the fully rendered output.
// When output hasn't changed since the last tick, the redraw is skipped.
// On non-TTY (piped output), new output is appended only when it differs.
func watchPoll(w io.Writer, intervalSeconds int, renderFn func() (string, error)) error {
	isTTY := isTerminal(w)

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var prevOutput string
	var prevLines int

	// First fetch
	rendered, err := renderFn()
	if err != nil {
		return err
	}
	fmt.Fprint(w, rendered)
	prevOutput = rendered
	prevLines = strings.Count(rendered, "\n")

	for {
		select {
		case <-ticker.C:
			rendered, err = renderFn()
			if err != nil {
				return err
			}

			// Skip redraw if nothing changed
			if rendered == prevOutput {
				continue
			}

			// On TTY, move cursor up and clear previous output before redrawing
			if isTTY && prevLines > 0 {
				fmt.Fprintf(w, "\033[%dA\033[J", prevLines)
			}

			fmt.Fprint(w, rendered)
			prevOutput = rendered
			prevLines = strings.Count(rendered, "\n")

		case <-ctx.Done():
			return nil
		}
	}
}

// renderToBuffer temporarily redirects cmd output to a buffer, calls fn
// (which should use the cmd's printer), and returns the captured output.
func renderToBuffer(cmd *cobra.Command, fn func() error) (string, error) {
	var buf bytes.Buffer
	origOut := cmd.OutOrStdout()
	cmd.SetOut(&buf)
	defer cmd.SetOut(origOut)

	if err := fn(); err != nil {
		return "", err
	}

	return buf.String(), nil
}
