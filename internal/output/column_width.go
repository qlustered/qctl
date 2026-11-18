package output

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// terminalMaxColumnWidth is the column cap MaxColumnWidth returns for a
// terminal when --max-column-width was not passed.
const terminalMaxColumnWidth = 80

// isTerminal reports whether w is a terminal. Tests replace it.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// MaxColumnWidth returns cmd's table column cap in terminal cells, 0 meaning no
// limit: the --max-column-width value when the flag was passed, otherwise 80
// when cmd.OutOrStdout() is a terminal and 0 when it is not.
func MaxColumnWidth(cmd *cobra.Command) int {
	if cmd.Flags().Changed("max-column-width") {
		width, _ := cmd.Flags().GetInt("max-column-width")
		return width
	}
	if isTerminal(cmd.OutOrStdout()) {
		return terminalMaxColumnWidth
	}
	return 0
}
