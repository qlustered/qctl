package file

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	// codeReplacementQuestionSettled is the 409 for a file with no open
	// replacement question.
	codeReplacementQuestionSettled = "REPLACEMENT_QUESTION_SETTLED"
	// codeHeldFileSettled is the 409 for an older file already decided.
	codeHeldFileSettled = "HELD_FILE_SETTLED"
)

const replacementLong = `A new file of a cloud source can ask whether it replaces an older file of the
same source that is still blocked and never loaded. 'qctl describe file
<file-id>' shows the question under status.replacement_question, naming the
older file and both periods. An emailed file is also asked by email; the first
answer settles it.

  replace-older   the new file replaces the older one: the older file is
                  ignored as replaced by it and its blocked ingestion jobs
                  are stopped
  keep-both       the older file is left as it is

<file-id> is the NEW file, the one the question is on.

-o json|yaml prints the answered question and -o name prints file/<id>; the
prose moves to stderr.`

func newReplaceOlderCommand() *cobra.Command {
	return newAnswerCommand("replace-older",
		"Answer that a file replaces the older file it asks about, discarding that one",
		"qctl file replace-older 123", api.ReplacementAnswerDiscardOld)
}

func newKeepBothCommand() *cobra.Command {
	return newAnswerCommand("keep-both",
		"Answer that a file does not replace the older file it asks about",
		"qctl file keep-both 123", api.ReplacementAnswerKeepBoth)
}

func newAnswerCommand(verb, short, example string, answer stored_items.ReplacementAnswer) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <file-id>",
		Short: short,
		Long: short + ".\n\n" + replacementLong + `

Examples:
  # read the question file 123 asks
  qctl describe file 123

  ` + example,
		Annotations: cmdutil.WithArgKinds(
			cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml", "name"), "file"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnswer(cmd, args[0], answer)
		},
	}
}

func runAnswer(cmd *cobra.Command, arg string, answer stored_items.ReplacementAnswer) error {
	ids, err := cmdutil.ParseIntArgs([]string{arg}, "file ID")
	if err != nil {
		return err
	}
	fileID := ids[0]
	if fileID <= 0 {
		return qerrors.NewBadUsagef("invalid file ID %d: file IDs are positive integers", fileID)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create stored items client: %w", err)
	}
	question, err := client.AnswerReplacementQuestion(ctx.Credential.AccessToken, fileID, answer)
	if err != nil {
		return explainAnswerRefusal(
			fmt.Errorf("failed to answer the replacement question of file %d: %w", fileID, err), fileID)
	}

	w := cmdutil.NewResultWriter(cmd)
	older := fmt.Sprintf("file %d (%s)", question.OlderStoredItemID, terminal.Sanitize(question.OldFileName))
	name := "file/" + strconv.Itoa(question.NewStoredItemID)
	if answer == api.ReplacementAnswerDiscardOld {
		fmt.Fprintf(w.Human, "%s replaces %s\n", name, older)
		fmt.Fprintln(w.Human, "The older file is ignored as replaced, its blocked ingestion jobs are stopped, and its open "+
			"alerts that link no other kept file are resolved.")
	} else {
		fmt.Fprintf(w.Human, "%s does not replace %s; both files are kept\n", name, older)
	}
	fmt.Fprintf(w.Human, "Next: qctl describe file %d\n", question.OlderStoredItemID)
	return w.Write(question, name)
}

// explainAnswerRefusal appends a Hint: line for the route's two 409 codes,
// keeping the %w chain, and returns any other error unchanged.
func explainAnswerRefusal(err error, fileID int) error {
	var hint string
	switch apierror.ErrorCodeOf(err) {
	case codeReplacementQuestionSettled:
		hint = fmt.Sprintf("file %d has no open replacement question: it never asked one, it was already answered, "+
			"or the older file was since ignored, replaced or loaded. "+
			"'qctl describe file %d' shows status.replacement_question when there is one.", fileID, fileID)
	case codeHeldFileSettled:
		hint = "the older file was already decided (turned down or replaced) and can no longer be discarded as replaced by this one."
	default:
		return err
	}
	return fmt.Errorf("%w\n\nHint: %s", err, hint)
}
