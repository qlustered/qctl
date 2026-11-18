//nolint:revive // Package name intentionally matches the comment-thread command directory.
package comment_thread

import (
	"fmt"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// The backend's own caps, mirrored here so an over-cap request is a usage
// error the user can read instead of a 422 they have to decode. The body cap
// is cmdutil.MaxCommentBodyChars, shared with every comment-bearing verb.
const (
	maxTargetRows  = 500  // CommentThreadCreateSchema.targets max_length
	maxTargetCells = 2000 // MAX_THREAD_TARGET_CELLS, the aggregate bound
	maxTitleChars  = 500  // CommentThreadCreateSchema.title max_length
)

func newCreateCommand() *cobra.Command {
	var (
		rows     []int
		fields   []string
		body     string
		bodyFile string
		title    string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Open a comment thread on rows or cells of a table",
		Long: `Open a comment thread on one table's rows or cells, together with its first comment.

Targets are the cross product of --row and --field: every named field on every
named row. With no --field the rows are targeted whole. The wire also allows a
different field set per row; qctl does not expose that — open one thread per
shape instead.

Row ids come from the data itself ('qctl get data <table>'), and they are NOT
stable: keyless sync, composite recompute and append-mode re-upload all re-mint
them, after which a backend sweep resolves the thread with reason "row_gone".

A target that does not resolve is dropped rather than failing the request: the
thread is created from the rest and the dropped cells are reported on stderr.
A request whose targets ALL fail creates nothing.

-o json|yaml prints the server's response, {thread, dropped_targets}, and
moves the result line to stderr; -o name prints comment-thread/<uuid>.

Examples:
  qctl comment-thread create --table orders --row 1042 --field amount --body "why negative?"
  qctl comment-thread create --table orders --row 1042 --row 1043 --body "duplicate?"
  qctl comment-thread create --table-id 42 --row 1042 --field amount --field currency \
      --title "FX mismatch" --body-file notes.md
  cat notes.md | qctl comment-thread create --table orders --row 1042 --body-file -`,
		Annotations: cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			text, err := cmdutil.ReadBodyFlags(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if err := validateBody(text, title); err != nil {
				return err
			}
			// Before Bootstrap and the table lookup, both of which cost a
			// request: an over-cap target set is decidable locally.
			if err := validateTargetCaps(rows, fields); err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}
			client := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			result, err := client.Create(ctx.Credential.AccessToken, datasetID, comment_threads.CreateOptions{
				Targets: buildTargets(rows, fields),
				Body:    text,
				Title:   title,
			})
			if err != nil {
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintln(out.Human, createdLine(&result.Thread))
			reportDroppedTargets(cmd, result)
			return out.Write(result, "comment-thread/"+result.Thread.ID.String())
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().IntSliceVar(&rows, "row", nil,
		"Row id this thread is about (repeatable; required; max 500)")
	cmd.Flags().StringArrayVar(&fields, "field", nil,
		"Field to target on every --row (repeatable; omit to target the whole rows)")
	cmd.Flags().StringVar(&body, "body", "", "First comment's body (markdown, 1-10000 chars)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the first comment's body from a file (\"-\" = stdin)")
	cmd.Flags().StringVar(&title, "title", "", "Thread title (max 500 chars)")
	return cmd
}

// validateBody adds create's own cap: the thread title, which no other verb has.
func validateBody(text, title string) error {
	if err := cmdutil.ValidateCommentBody(text); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(title); n > maxTitleChars {
		return qerrors.NewBadUsagef("--title is %d characters, over the %d-character limit", n, maxTitleChars)
	}
	return nil
}

// validateTargetCaps refuses an over-cap target set client-side. The row count
// and the flattened cell count are two separate backend bounds, and a
// mis-typed loop is exactly how a request arrives over either of them.
func validateTargetCaps(rows []int, fields []string) error {
	switch {
	case len(rows) == 0:
		return qerrors.NewBadUsage("--row is required: name at least one row this thread is about")
	case len(rows) > maxTargetRows:
		return qerrors.NewBadUsagef("--row was given %d times, over the %d-row limit", len(rows), maxTargetRows)
	}
	// Row ids are database ids, so a non-positive one cannot match anything.
	// Left to the backend it comes back as a row_not_found dropped target, or
	// an all-fail 422 — neither of which reads as "you typed the flag wrong".
	for _, row := range rows {
		if row <= 0 {
			return qerrors.NewBadUsagef("--row must be a positive row id, got %d", row)
		}
	}
	// Whole-row targets are not cells, so a fieldless request contributes
	// nothing to this bound — which is why it is len(rows)*len(fields) and not
	// a max(1, ...) of it.
	if cells := len(rows) * len(fields); cells > maxTargetCells {
		return qerrors.NewBadUsagef("%d rows x %d fields is %d cells, over the %d-cell limit",
			len(rows), len(fields), cells, maxTargetCells)
	}
	return nil
}

// buildTargets expands --row x --field into the wire's per-row targets: every
// named field on every named row. With no --field the rows are targeted whole,
// which the wire spells as a null field_names — NOT an empty list.
//
// The wire shape also allows different fields on different rows. qctl does not
// expose that: it has no human use, and one uniform rule beats a second flag.
// A caller who needs it can open one thread per shape.
func buildTargets(rows []int, fields []string) []comment_threads.Target {
	targets := make([]comment_threads.Target, 0, len(rows))
	for _, row := range rows {
		target := comment_threads.Target{RowID: row}
		if len(fields) > 0 {
			names := append([]string(nil), fields...)
			target.FieldNames = &names
		}
		targets = append(targets, target)
	}
	return targets
}

// createdLine names the new thread and, when the response says so, how many
// targets it actually holds.
//
// targets is omitempty, so a response without the key means "not reported",
// not "none" — and a confident "(0 target(s))" about a thread just created
// with targets is a worse answer than saying nothing. The request's own count
// is not a substitute either: it counts ROWS, one entry carrying a whole field
// list, while the response counts row-field pairs.
func createdLine(thread *comment_threads.Thread) string {
	if thread.Targets == nil {
		return fmt.Sprintf("comment-thread/%s created", thread.ID)
	}
	return fmt.Sprintf("comment-thread/%s created (%d target(s))", thread.ID, len(*thread.Targets))
}

// reportDroppedTargets prints the partial-success breadcrumbs. A 201 with
// dropped_targets means the thread exists and the dropped cells are NOT in it,
// so this goes to stderr at exit 0 — the same shape as apply -f's 202 staging
// path, success with breadcrumbs.
func reportDroppedTargets(cmd *cobra.Command, result *comment_threads.CreateResult) {
	if result.DroppedTargets == nil || len(*result.DroppedTargets) == 0 {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nwarning: %d target(s) were dropped and are not part of this thread:\n",
		len(*result.DroppedTargets))
	// The reason and the field name are both the server's, and a field name
	// echoes a column of customer data, so both go through terminal.Sanitize.
	// %q would already escape a control byte on its own; sanitizing first is
	// belt-and-braces and costs nothing on a well-formed name, where Sanitize
	// returns the string unchanged.
	for _, dropped := range *result.DroppedTargets {
		if dropped.FieldName == nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  row %d (%s)\n", dropped.RowID, terminal.Sanitize(dropped.Reason))
			continue
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "  row %d field %q (%s)\n", dropped.RowID,
			terminal.Sanitize(*dropped.FieldName), terminal.Sanitize(dropped.Reason))
	}
}
