package cmdutil

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// destructiveCommands are the commands that remove, irreversibly lock or
// permanently approve a server resource, data or running work, as paths
// without the binary name.
// A group covers every command under it.
var destructiveCommands = []string{
	"comment-thread delete-comment",
	"data-review close",
	"data-review-group cancel",
	"delete",
	"deliverable-run cancel",
	"deliverable-run mark-delivered",
	"deliverable-run sign-off",
	"deliverable-run unwaive",
	"duplicate-group share",
	"email-intake rotate",
	"email-sender remove",
	"file replace-older",
	"kill",
	"pii-access-request deny",
	"pii-access-request withdraw",
	"pii-grant revoke",
	"queue-message retire",
	"review close",
	"review delete-comment",
	"rows delete-quarantined",
	"table-member leave",
	"table-member remove",
	"table-member set-permission",
	"table-series approve-rules",
	"table-series delete-period",
	"table-series recreate-period",
	"undelete deliverable",
}

// DestructiveCommands returns the destructive command paths, without the
// binary name.
func DestructiveCommands() []string {
	return append([]string(nil), destructiveCommands...)
}

// IsDestructive reports whether cmd is, or sits under, a destructive command
// path.
func IsDestructive(cmd *cobra.Command) bool {
	path := commandPathWithoutRoot(cmd)
	for _, d := range destructiveCommands {
		if path == d || strings.HasPrefix(path, d+" ") {
			return true
		}
	}
	return false
}

// fileTrashActions trash a file, which deletes its loaded rows.
var fileTrashActions = map[string]bool{
	"trash_alert_associated_files": true,
	"trash_file":                   true,
	"reject_held_file":             true,
	"reject_held_email_file":       true,
}

// IsDestructiveAction reports whether an alert or warning action removes
// something: a delete_* or discard_* action, or one in fileTrashActions.
func IsDestructiveAction(action string) bool {
	return strings.HasPrefix(action, "delete_") || strings.HasPrefix(action, "discard_") || fileTrashActions[action]
}

// GuardProtectedContext applies RequireNamedContext to a destructive command.
func GuardProtectedContext(cmd *cobra.Command) error {
	if !IsDestructive(cmd) {
		return nil
	}
	return RequireNamedContext(cmd, "`"+cmd.CommandPath()+"`")
}

// RequireNamedContext refuses what -- a description of the destructive change
// -- unless its server is the current context's own and that context is
// allowlisted or was named with --context. On an interactive stdin it asks
// instead, and a decline is ErrDeclined. A config or server that does not
// resolve is left for the command to report.
func RequireNamedContext(cmd *cobra.Command, what string) error {
	cfg, loadErr := config.Load()
	if loadErr != nil {
		return nil
	}
	serverFlag, _ := cmd.Flags().GetString("server")
	server, source, serverErr := config.SelectServer(cfg, serverFlag)
	if serverErr != nil {
		return nil
	}
	name := terminal.Sanitize(cfg.CurrentContext)
	out := cmd.ErrOrStderr()
	ctx, ctxErr := cfg.GetCurrentContext()
	if ctxErr != nil || !sameServer(server, ctx.Server) {
		target := terminal.Sanitize(server)
		owner := fmt.Sprintf("context %q's server", name)
		if ctxErr != nil {
			owner = "a context's server"
		}
		header := fmt.Sprintf("%s goes to %s, set by %s, not to %s.", what, target, serverSourceLabel(source), owner)
		if CanPrompt() {
			fmt.Fprintln(out, highlight(out, header))
		}
		return ConfirmInTerminal(out, fmt.Sprintf("Run %s against it?", what), header+"\n"+
			fmt.Sprintf("Confirm with the user that this change is meant for %s, then re-run with --context naming a context whose server is %s.", target, target))
	}
	if !ctx.IsProtected() || config.ContextOverride() != "" {
		return nil
	}
	allowlist := "qctl config set-context " + name + " --protected=false"
	if CanPrompt() {
		fmt.Fprintln(out, highlight(out, fmt.Sprintf("Context %q is protected.", name)))
		fmt.Fprintf(out, "If %q is a development context, allowlist it to stop this question: %s\n", name, allowlist)
	}
	return ConfirmInTerminal(out, fmt.Sprintf("Run %s against it?", what), fmt.Sprintf(
		"context %q is protected, so %s must name it with --context.\n"+
			"Confirm with the user that this change is meant for %q, then re-run with --context %s.\n"+
			"If %q is a development context, the user can allowlist it at a terminal: %s",
		name, what, name, name, name, allowlist))
}

// sameServer reports whether a and b are one endpoint after
// config.NormalizeEndpointKey; a URL that does not parse matches nothing.
func sameServer(a, b string) bool {
	ka, errA := config.NormalizeEndpointKey(a)
	kb, errB := config.NormalizeEndpointKey(b)
	return errA == nil && errB == nil && ka == kb
}

func serverSourceLabel(source config.ServerSource) string {
	switch source {
	case config.ServerSourceFlag:
		return "--server"
	case config.ServerSourceEnv:
		return "QCTL_SERVER"
	}
	return "the context"
}

// ConfirmInTerminal asks prompt on out. When stdin is not interactive it
// returns refusal as bad usage instead; no --yes flag answers it. A decline is
// ErrDeclined.
func ConfirmInTerminal(out io.Writer, prompt, refusal string) error {
	if !CanPrompt() {
		return qerrors.NewBadUsage(refusal)
	}
	ok, err := ConfirmYesNo(out, prompt)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeclined
	}
	return nil
}

// highlight renders s bold red when w is a terminal and NO_COLOR is unset.
func highlight(w io.Writer, s string) string {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) || os.Getenv("NO_COLOR") != "" {
		return s
	}
	return "\033[1;31m" + s + "\033[0m"
}

func commandPathWithoutRoot(cmd *cobra.Command) string {
	path := cmd.CommandPath()
	if i := strings.IndexByte(path, ' '); i >= 0 {
		return path[i+1:]
	}
	return ""
}
