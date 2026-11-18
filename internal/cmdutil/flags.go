package cmdutil

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// The validators below reject what the *user* typed, so they carry exit code 2
// (ExitBadUsage) rather than the generic 1. A script that cannot tell "you
// passed the wrong flags" from "the API fell over" retries the first case
// forever and gives up on the second; the exit code is the only part of the
// answer a non-interactive caller can branch on. The codes ride an
// errors.Unwrap chain, so call sites may wrap these with fmt.Errorf("...: %w",
// err) freely — but rewriting one with %v or errors.New silently demotes it
// back to 1.

// ValidateConflictingFlags checks if multiple flags from a conflicting set are specified.
// Each set is a slice of flag names that cannot be used together.
// Returns an error if more than one flag from any set is changed.
//
// Example:
//
//	ValidateConflictingFlags(cmd,
//	    []string{"table-id", "table"},        // can't use both
//	    []string{"cloud-source-id", "cloud-source"}, // can't use both
//	)
func ValidateConflictingFlags(cmd *cobra.Command, flagSets ...[]string) error {
	for _, set := range flagSets {
		var changed []string
		for _, flag := range set {
			if cmd.Flags().Changed(flag) {
				changed = append(changed, "--"+flag)
			}
		}
		if len(changed) > 1 {
			return qerrors.NewBadUsagef("cannot use both %s flags", strings.Join(changed, " and "))
		}
	}
	return nil
}

// RequireOneOfFlags checks that at least one flag from the required set is specified.
// Returns an error if none of the flags are changed.
//
// Example:
//
//	RequireOneOfFlags(cmd, []string{"table-id", "table"})
func RequireOneOfFlags(cmd *cobra.Command, flags []string) error {
	for _, flag := range flags {
		if cmd.Flags().Changed(flag) {
			return nil
		}
	}

	formatted := make([]string, len(flags))
	for i, f := range flags {
		formatted[i] = "--" + f
	}
	return qerrors.NewBadUsagef("must specify one of: %s", strings.Join(formatted, " or "))
}

// RejectBlankFlags refuses the first of the named string flags that was given
// an empty or whitespace-only value.
func RejectBlankFlags(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		value, _ := cmd.Flags().GetString(name)
		if cmd.Flags().Changed(name) && strings.TrimSpace(value) == "" {
			return qerrors.NewBadUsagef("--%s cannot be empty or blank", name)
		}
	}
	return nil
}

// PositiveIDFlag reads an optional integer ID flag — a filter, or a reference
// to one resource — that is only meaningful when positive.
//
// It returns (value, set, error). `set` reports that the user actually passed
// the flag, which GetInt alone cannot tell: these flags default to 0, so the
// value 0 means both "not passed" and "passed as zero". Reading them with a
// bare `if id > 0` therefore drops a filter the user typed and answers with an
// unfiltered whole-organization listing at exit code 0.
//
// A flag that was passed with a non-positive value is an error, never a
// dropped filter. When set is true, value is guaranteed to be > 0.
//
// Example:
//
//	tableID, haveTableID, err := PositiveIDFlag(cmd, "table-id")
//	if err != nil {
//	    return err
//	}
//	if haveTableID {
//	    params.DatasetID = &tableID
//	}
func PositiveIDFlag(cmd *cobra.Command, name string) (int, bool, error) {
	if cmd.Flags().Lookup(name) == nil {
		// Without this, a typo'd name reads as "the user did not pass it" and
		// silently reinstates the bug this helper exists to prevent.
		return 0, false, fmt.Errorf("internal error: flag --%s is not defined on %q", name, cmd.CommandPath())
	}
	if !cmd.Flags().Changed(name) {
		return 0, false, nil
	}
	value, err := cmd.Flags().GetInt(name)
	if err != nil {
		return 0, false, fmt.Errorf("failed to read --%s: %w", name, err)
	}
	if value <= 0 {
		return 0, false, qerrors.NewBadUsagef("--%s must be a positive integer, got %d", name, value)
	}
	return value, true, nil
}

// readIntFlag is the shared front half of the numeric validators below: it
// proves the flag exists before reading it. Without the Lookup, a typo'd name
// reads back as the pflag zero value — indistinguishable from "the user typed
// 0" — and silently reinstates the bug the validator exists to prevent.
func readIntFlag(cmd *cobra.Command, name string) (int, error) {
	if cmd.Flags().Lookup(name) == nil {
		return 0, fmt.Errorf("internal error: flag --%s is not defined on %q", name, cmd.CommandPath())
	}
	value, err := cmd.Flags().GetInt(name)
	if err != nil {
		return 0, fmt.Errorf("failed to read --%s: %w", name, err)
	}
	return value, nil
}

// PositiveSecondsFlag reads a poll-interval flag whose value is handed to
// time.NewTicker, which panics on any non-positive duration. Every such flag
// defaults to a positive number, so an unset flag always passes; only a value
// the user typed can fail. A value above maxSecondsFlag fails too: in seconds
// it overflows time.Duration into a negative one.
//
// The check belongs here, not in the ticker helper, because only the command
// knows the flag's name — `get dry-run-jobs` spells this flag --interval while
// the others spell it --watch-interval, and an error that names the wrong flag
// is worse than no error.
//
// Example:
//
//	interval, err := PositiveSecondsFlag(cmd, "watch-interval")
//	if err != nil {
//	    return err
//	}
func PositiveSecondsFlag(cmd *cobra.Command, name string) (int, error) {
	value, err := readIntFlag(cmd, name)
	if err != nil {
		return 0, err
	}
	if value < 1 {
		return 0, qerrors.NewBadUsagef("--%s must be at least 1 second, got %d", name, value)
	}
	if int64(value) > maxSecondsFlag {
		return 0, qerrors.NewBadUsagef("--%s must be at most %d seconds, got %d", name, maxSecondsFlag, value)
	}
	return value, nil
}

// maxSecondsFlag is the largest whole number of seconds a time.Duration holds.
const maxSecondsFlag = math.MaxInt64 / int64(time.Second)

// PositiveCountFlag reads a count flag — --page, --limit — that is
// meaningless at or below zero. Unlike PositiveIDFlag it always returns a
// value: these flags carry a positive default, so an unset flag cannot fail.
//
// The rule exists because the domain clients apply each pagination parameter
// only when it is positive (see internal/alerts/client.go's `if params.Page >
// 0`). A --page 0 therefore reached the server as no page at all and answered
// with the backend's default page at exit code 0 — the user's typed value
// silently replaced by a different one, the same shape PositiveIDFlag exists
// to prevent. Those client-side `> 0` guards are correct and stay: for callers
// that build a params struct directly (internal/cmdutil/resolve.go) 0 really
// does mean "unset".
//
// Example:
//
//	limit, err := PositiveCountFlag(cmd, "limit")
//	if err != nil {
//	    return err
//	}
func PositiveCountFlag(cmd *cobra.Command, name string) (int, error) {
	value, err := readIntFlag(cmd, name)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, qerrors.NewBadUsagef("--%s must be a positive integer, got %d", name, value)
	}
	return value, nil
}

// NonNegativeCountFlag reads a count flag whose zero is a real, documented
// value rather than an accident — `get cloud-sources` and `get files`, whose
// --limit defaults to 0 and means "fetch every page" for their client-side
// auto-pagination, and `queue-message resurrect --backoff`, whose 0 means
// "deliverable immediately". The test is whether the flag's own zero has a
// meaning the help text states; where it does not, zero is a mistake and
// PositiveCountFlag is the right validator.
//
// Stated as a rule rather than a census: this comment used to open "Exactly
// two commands qualify", which was true when it was written and false the
// first time a third flag met the same condition.
//
// Negatives are still refused. Both auto-paginators
// (internal/cloud_sources/client.go, internal/stored_items/client.go) gate
// every use of maxResults behind `maxResults > 0`, so a negative value fell
// through those guards exactly like 0 does. A negative --limit was
// indistinguishable from --limit 0 ("unlimited"): the command fetched and
// printed every page in the organization, over N requests, at exit 0.
func NonNegativeCountFlag(cmd *cobra.Command, name string) (int, error) {
	value, err := readIntFlag(cmd, name)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, qerrors.NewBadUsagef("--%s cannot be negative, got %d", name, value)
	}
	return value, nil
}

// ParseIntArgs parses command arguments as integers.
// Returns a descriptive error if any argument is not a valid integer.
func ParseIntArgs(args []string, itemName string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return nil, qerrors.NewBadUsagef("invalid %s: %s", itemName, arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// maxPeriodKeyRunes is the longest reporting-period key the server accepts.
const maxPeriodKeyRunes = 200

// ValidatePeriodKey refuses, with exit 2, a reporting-period key the server's
// period_key would reject: 1 to 200 characters on one line, without leading
// or trailing whitespace. what names the key in the message ("--period").
func ValidatePeriodKey(what, key string) error {
	n := utf8.RuneCountInString(key)
	if n == 0 || n > maxPeriodKeyRunes {
		return qerrors.NewBadUsagef("%s must be 1 to %d characters, got %d", what, maxPeriodKeyRunes, n)
	}
	first, _ := utf8.DecodeRuneInString(key)
	last, _ := utf8.DecodeLastRuneInString(key)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return qerrors.NewBadUsagef("%s must not start or end with whitespace", what)
	}
	if strings.Contains(key, "\n") {
		return qerrors.NewBadUsagef("%s must be one line", what)
	}
	return nil
}

// ParseUUIDArg parses one command-line value that must be a full UUID. It
// returns bad usage so callers can validate wire-typed selectors before any
// request is made.
func ParseUUIDArg(arg, itemName string) (uuid.UUID, error) {
	id, err := uuid.Parse(arg)
	if err != nil {
		return uuid.Nil, qerrors.NewBadUsagef("invalid %s %q: expected a full UUID", itemName, arg)
	}
	return id, nil
}

// ValidateUUIDArgs is ParseIntArgs for arguments that must be full UUIDs. It
// exists so a typo exits 2 at the command layer, before anything goes over the
// wire. The domain clients parse ids too, but most return a bare fmt.Errorf
// (exit 1) and only after the call that resolves the table has already gone
// over the wire. internal/notifications is the exception: parseNotificationID
// returns qerrors.NewBadUsagef and runs before its first request. Validating
// here is what makes the two agree, whichever kind of client is underneath.
//
// itemName names the thing being addressed ("comment thread id"), so the
// message reads the same as the domain client's unreachable fallback.
func ValidateUUIDArgs(args []string, itemName string) error {
	for _, arg := range args {
		if _, err := ParseUUIDArg(arg, itemName); err != nil {
			return err
		}
	}
	return nil
}

// ReadBodyFlags resolves the --body/--body-file pair that every command
// carrying a long-form text payload registers. Exactly one must be set;
// "--body-file -" reads stdin so agents can pipe heredocs.
//
// It lives here rather than in internal/comments or internal/comment_threads
// because it is command-layer code: it takes a *cobra.Command and returns
// qerrors.NewBadUsage, and both of those domain packages are cobra-free.
//
// The message text is the part that must not be duplicated. Every command that
// spells this pair says the same two things when the user gets it wrong, and a
// second copy is what a future grep for one of those strings would miss.
func ReadBodyFlags(cmd *cobra.Command, body, bodyFile string) (string, error) {
	switch {
	case body != "" && bodyFile != "":
		return "", qerrors.NewBadUsage("--body and --body-file are mutually exclusive")
	case body != "":
		return body, nil
	case bodyFile == "":
		return "", qerrors.NewBadUsage("one of --body or --body-file is required")
	default:
		return ReadTextFile(cmd, bodyFile, "body")
	}
}

// ReadTextFile reads the file a --<what>-file flag names, where "-" means the
// command's stdin, so agents can pipe heredocs. what names the payload in the
// error ("body", "justification").
//
// It is the reading half of ReadBodyFlags, for a text/file flag pair spelled
// other than --body/--body-file: such a command validates its pair with
// ValidateConflictingFlags and RequireOneOfFlags, then reads through here, so
// "-" means stdin on every one of them.
//
// An empty path is bad usage (exit 2): the flag was given with no file, which
// the pair checks cannot see because they only ask whether it was set. A path
// that names a missing or unreadable file stays exit 1 -- the command line was
// well-formed and the environment failed.
func ReadTextFile(cmd *cobra.Command, path, what string) (string, error) {
	if path == "" {
		return "", qerrors.NewBadUsagef(`no %s file given: name a file, or "-" to read stdin`, what)
	}
	if path == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("failed to read %s from stdin: %w", what, err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s file: %w", what, err)
	}
	return string(data), nil
}

// RequireCommentBody rejects a body that is empty or only whitespace. It is
// the one spelling of that message: every comment-bearing verb across the rule
// arc and the comment-thread arc says it, and a second copy is what a future
// grep for the string would miss.
//
// It is deliberately NOT folded into ReadBodyFlags: that helper answers "which
// source did the user name", a question with no opinion about the payload,
// while this one is a rule about comment text specifically.
func RequireCommentBody(text string) error {
	if strings.TrimSpace(text) == "" {
		return qerrors.NewBadUsage("comment body is empty")
	}
	return nil
}

// MaxCommentBodyChars is the backend's cap on every comment body: the
// max_length of both CreateCommentSchema.body and
// CommentThreadCreateSchema.body.
const MaxCommentBodyChars = 10000

// ValidateCommentBody is RequireCommentBody plus MaxCommentBodyChars, so an
// over-cap body is a usage error the user can read instead of a 422 they have
// to decode. Lengths are counted in runes, because that is what Pydantic's
// max_length counts. A verb that appends a --mention cc line runs it again on
// what it actually sends.
func ValidateCommentBody(text string) error {
	if err := RequireCommentBody(text); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(text); n > MaxCommentBodyChars {
		return qerrors.NewBadUsagef("comment body is %d characters, over the %d-character limit", n, MaxCommentBodyChars)
	}
	return nil
}

// ValidateCommentText is ValidateCommentBody for a comment that is not a
// comment-thread body, naming it as name ("--comment", "spec.comment") in the
// message.
func ValidateCommentText(name, text string) error {
	if strings.TrimSpace(text) == "" {
		return qerrors.NewBadUsagef("%s is blank: say why", name)
	}
	if n := utf8.RuneCountInString(text); n > MaxCommentBodyChars {
		return qerrors.NewBadUsagef("%s is %d characters, over the %d-character limit", name, n, MaxCommentBodyChars)
	}
	return nil
}

// CommentThreadNotFoundHint explains an ambiguous thread 404, with one %s for the thread id.
const CommentThreadNotFoundHint = "no thread %s in this table, or it is hidden by your source access; " +
	"if it belongs to a rule review, use 'qctl describe rule-review <review-id>'"

// HintOnNotFound appends a parenthesised hint to a not-found error and returns
// every other error untouched — a hint about the wrong failure is worse than
// none.
//
// The wrap is %w on purpose: qerrors.GetExitCode walks the Unwrap chain, so a
// %v here would keep the message perfect while silently demoting exit 3 to 1.
func HintOnNotFound(err error, hint string) error {
	if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		return err
	}
	return fmt.Errorf("%w\n(%s)", err, hint)
}
