package share

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// maxShareTables mirrors MAX_DATASET_IDS_PER_BULK_GRANT in atlas
// capi/capi/schemas/user_schema.py. Rejecting above it locally turns a
// wire-shaped 422 into a usage error that names the cap.
const (
	maxShareTables   = 200
	outputFormatJSON = "json"
)

const shareAccessNote = `Existing grants keep their current level. Change a level with
qctl table-member set-permission; revoke a grant with qctl table-member remove.`

// NewTableCommand creates the 'share table' command.
func NewTableCommand() *cobra.Command {
	var yes bool
	var toEmail, toUserID, permission string

	cmd := &cobra.Command{
		Use:   "table <id-or-name>... --to <email>",
		Short: "Grant another user access to one or more tables",
		Long: `Grant one active member of your organization access to one or more tables.

--permission chooses owner, editor or read_only (the default). The effective
level also depends on the recipient's organization role. External users get
access through their assigned cloud sources and cannot receive table grants.
Tables the user already has come back as "already had access" and keep their
existing level, even when --permission names a different one.

` + shareAccessNote + `

Unless you are an ops or admin user, you can only share tables you own.
Tables you may not share come back under "refused"; the rest are still granted.

Under -o json or -o yaml, the pre-share summary and confirmation prompt go to
stderr so stdout remains one machine-readable document. --yes skips the prompt
and is required whenever stdin is not an interactive terminal.

Examples:
  # Share one table
  qctl share table orders --to alice@example.com

  # Share several at once
  qctl share table orders shipments 42 --to alice@example.com

  # Address the user by id instead of email, without a prompt
  qctl share table orders --to-id 11111111-2222-3333-4444-555555555555 --yes

  # Machine-readable result, including which tables were refused and why
  qctl share table orders shipments --to alice@example.com --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("table"), "table", outputFormatJSON, "yaml"),
		Args:        cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShareTable(cmd, args, shareOptions{toEmail: toEmail, toUserID: toUserID, permission: permission, yes: yes})
		},
	}

	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().StringVar(&toEmail, "to", "", "Email address of the user to grant access to")
	cmd.Flags().StringVar(&toUserID, "to-id", "", "UUID of the user to grant access to")
	cmd.Flags().StringVar(&permission, "permission", "read_only", "Grant level: owner|editor|read_only (existing grants keep their level)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	return cmd
}

// shareOptions carries the command's own flags into runShareTable, which lives
// outside NewTableCommand so the constructor is a declaration rather than a
// closure holding the whole flow.
type shareOptions struct {
	toEmail    string
	toUserID   string
	permission string
	yes        bool
}

// structuredOutput reports the --output value and whether it is one of the
// machine-readable ones, i.e. whether stdout is a document rather than prose.
func structuredOutput(cmd *cobra.Command) (string, bool) {
	format, _ := cmd.Flags().GetString("output")
	return format, format == outputFormatJSON || format == "yaml"
}

// validateShareFlags rejects what the user typed, before any credential read or
// request. Every failure here exits 2.
func validateShareFlags(cmd *cobra.Command, args []string) error {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"to", "to-id"}); err != nil {
		return err
	}
	if err := cmdutil.RequireOneOfFlags(cmd, []string{"to", "to-id"}); err != nil {
		return err
	}
	if toID, _ := cmd.Flags().GetString("to-id"); toID != "" {
		if _, err := cmdutil.ParseUUIDArg(toID, "--to-id"); err != nil {
			return err
		}
	}
	if _, err := datasets.ParsePermissionLevel(permissionFlag(cmd)); err != nil {
		return err
	}
	if len(args) > maxShareTables {
		return qerrors.NewBadUsagef(
			"cannot share more than %d tables in one request (got %d); split the list",
			maxShareTables, len(args))
	}
	return nil
}

// resolveShareTargets turns every selector into an id, keeping the selector as
// the id's label. The default states filter is correct here: a deleted table
// cannot be shared.
func resolveShareTargets(dsClient *datasets.Client, accessToken string, args []string) ([]int, map[int]string, error) {
	ids := make([]int, 0, len(args))
	names := make(map[int]string, len(args))
	for _, arg := range args {
		id, err := dsClient.ResolveID(accessToken, arg)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := names[id]; exists {
			continue
		}
		ids = append(ids, id)
		names[id] = arg
	}
	return ids, names, nil
}

func runShareTable(cmd *cobra.Command, args []string, opts shareOptions) error {
	if err := validateShareFlags(cmd, args); err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	// Resolve every selector up front so the banner, and every id in the
	// result, can be shown with its name.
	ids, names, err := resolveShareTargets(dsClient, ctx.Credential.AccessToken, args)
	if err != nil {
		return err
	}

	recipient := opts.toEmail
	if recipient == "" {
		recipient = opts.toUserID
	}

	outputFormat, structured := structuredOutput(cmd)
	banner := cmd.OutOrStdout()
	if structured {
		banner = cmd.ErrOrStderr()
	}
	printShareContext(banner, recipient, opts.permission, ids, names)

	if err := confirmShare(cmd, opts.yes); err != nil {
		return err
	}

	level, err := datasets.ParsePermissionLevel(opts.permission)
	if err != nil {
		return err
	}
	result, err := dsClient.ShareDatasets(ctx.Credential.AccessToken, datasets.ShareDatasetsRequest{
		Email: opts.toEmail, UserID: opts.toUserID, DatasetIDs: ids,
		PermissionLevel: level,
	})
	if err != nil {
		return err
	}

	if structured {
		if err := output.WriteRaw(cmd.OutOrStdout(), outputFormat, result); err != nil {
			return err
		}
	} else {
		printShareResult(banner, result, names)
	}

	// A partial is a 2xx, so nothing above this point failed -- but some tables
	// were refused, and exit 0 would hide that from a script. The message names
	// what DID land, because a nonzero exit otherwise reads as "nothing
	// happened".
	if len(result.Failed) > 0 {
		committed := len(result.Granted) + len(result.AlreadyHadAccess)
		tableNoun, refusedVerb := "tables", "were"
		if len(ids) == 1 {
			tableNoun, refusedVerb = "table", "was"
		}
		grantNoun, committedVerb := "grants", "were"
		if committed == 1 {
			grantNoun, committedVerb = "grant", "was"
		}
		return qerrors.New(1, fmt.Sprintf(
			"%d of %d %s %s refused; the other %d %s %s committed",
			len(result.Failed), len(ids), tableNoun, refusedVerb,
			committed, grantNoun, committedVerb))
	}
	return nil
}

// printShareContext displays what is about to be granted, to whom. The writer
// is the caller's choice because the destination differs by output format:
// stdout for the human rendering, stderr when stdout is carrying a payload.
func printShareContext(w io.Writer, recipient, permission string, ids []int, names map[int]string) {
	fmt.Fprintf(w, "Grant to     : %s\n", recipient)
	fmt.Fprintf(w, "Tables       : %s\n", strings.Join(labelIDs(ids, names), ", "))
	fmt.Fprintf(w, "New grants   : %s\n", permission)
	fmt.Fprintln(w)
	fmt.Fprintln(w, shareAccessNote)
	fmt.Fprintln(w)
}

// printShareResult renders the three id lists with their names attached, so a
// reader never has to look up an integer. It takes the same writer the banner
// used, so the two halves of the human rendering cannot drift apart.
func printShareResult(w io.Writer, result *datasets.ShareDatasetsResult, names map[int]string) {
	if len(result.Granted) > 0 {
		fmt.Fprintf(w, "Granted           : %s\n", strings.Join(labelIDs(result.Granted, names), ", "))
	}
	if len(result.AlreadyHadAccess) > 0 {
		fmt.Fprintf(w, "Already had access (level unchanged): %s\n", strings.Join(labelIDs(result.AlreadyHadAccess, names), ", "))
	}
	// The reason and message come from the server; the label does not (names
	// holds the selectors the caller typed, see resolveShareTargets).
	for _, f := range result.Failed {
		fmt.Fprintf(w, "Refused           : %s — %s: %s\n",
			label(f.DatasetID, names), terminal.Sanitize(string(f.Reason)), terminal.Sanitize(f.Msg))
	}
}

func labelIDs(ids []int, names map[int]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, label(id, names))
	}
	return out
}

func label(id int, names map[int]string) string {
	if name, ok := names[id]; ok && name != fmt.Sprintf("%d", id) {
		return fmt.Sprintf("%s (id=%d)", name, id)
	}
	return fmt.Sprintf("id=%d", id)
}

func permissionFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("permission")
	return value
}

func confirmShare(cmd *cobra.Command, yes bool) error {
	if yes {
		return nil
	}
	confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Grant this access?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("share cancelled")
	}
	return nil
}
