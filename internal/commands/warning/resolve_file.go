package warning

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/resolveexplain"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// resolveInput is the one place the flag matrix is decided.
type resolveInput struct {
	file      string
	warningID int
	action    string
	dryRun    bool
	verb      string // the command's name: resolve or propose
	// rowIDs is --row-id, de-duplicated in order; nil when it was not given.
	rowIDs []int
	// set is --set, nil when it was not given.
	set map[string]string
	// reason is --reason, nil when it was not given.
	reason *string
	// valueFlags is whether the command registers --row-id, --set and --reason.
	valueFlags bool
}

// valueFlag names the first value flag given, "" when none is.
func (in resolveInput) valueFlag() string {
	switch {
	case in.rowIDs != nil:
		return "--row-id"
	case in.set != nil:
		return "--set"
	case in.reason != nil:
		return "--reason"
	}
	return ""
}

// spec is the resolution the flags name.
func (in resolveInput) spec() warnings.WarningResolutionSpec {
	return warnings.WarningResolutionSpec{
		Action:           in.action,
		IDs:              in.rowIDs,
		FieldNameToValue: in.set,
		Reason:           in.reason,
	}
}

// resolveInputs enforces the matrix. Every failure is exit 2 and names the fix.
// It runs BEFORE cmdutil.Bootstrap on purpose: a wrong combination of flags must
// cost nothing, not a token lookup.
func resolveInputs(cmd *cobra.Command, args []string) (resolveInput, error) {
	in := resolveInput{verb: cmd.Name()}
	in.file, _ = cmd.Flags().GetString("filename")
	in.action, _ = cmd.Flags().GetString("action")
	in.dryRun, _ = cmd.Flags().GetBool("dry-run")
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	if err := in.readValueFlags(cmd); err != nil {
		return in, err
	}

	switch {
	case in.file != "" && len(args) == 1:
		return in, qerrors.NewBadUsage(
			"cannot use -f together with a warning id: with -f the id comes from each document's metadata.id")
	case in.file == "" && len(args) == 0:
		return in, qerrors.NewBadUsage(
			"give a warning id or a manifest:\n" +
				"  qctl warning " + in.verb + " <warning-id> --action <action>\n" +
				"  qctl warning " + in.verb + " -f <file.yaml>")
	}

	if in.file != "" {
		return in, in.checkFileFlags()
	}
	if flag := in.valueFlag(); flag != "" && in.dryRun {
		return in, qerrors.NewBadUsagef(
			"%s cannot be combined with --dry-run: set spec.%s in the manifest it writes", flag, valueFlagField(flag))
	}

	if failFast {
		return in, qerrors.NewBadUsage("--fail-fast applies only to -f, which names the documents it would stop at")
	}
	if format, _ := cmd.Flags().GetString("output"); in.dryRun && (format == string(output.FormatJSON) || format == string(output.FormatName)) {
		return in, qerrors.NewBadUsagef(
			"--dry-run writes a YAML manifest; it cannot be combined with -o %s (use -o yaml or drop -o)", format)
	}
	return in.withWarningID(args[0])
}

// withWarningID reads value as the warning id and checks what the flag path
// needs of --action and the value flags.
func (in resolveInput) withWarningID(value string) (resolveInput, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return in, qerrors.NewBadUsagef("invalid warning id %q: expected a number", value)
	}
	in.warningID = id
	if in.action == "" {
		return in, qerrors.NewBadUsagef(
			"--action is required: qctl warning %s %d --action <action>\n"+
				"list the actions this warning offers with: qctl describe warning %d", in.verb, id, id)
	}
	if in.dryRun {
		return in, nil
	}
	return in, in.checkValueFlags()
}

// checkFileFlags refuses the flags -f takes from each document instead.
func (in resolveInput) checkFileFlags() error {
	if in.action != "" {
		return qerrors.NewBadUsage(
			"--action cannot be combined with -f: the action is spec.action in each document")
	}
	if in.dryRun {
		return qerrors.NewBadUsage(
			"--dry-run generates a manifest from --action; it cannot be combined with -f")
	}
	if flag := in.valueFlag(); flag != "" {
		return qerrors.NewBadUsagef(
			"%s cannot be combined with -f: set spec.%s in each document", flag, valueFlagField(flag))
	}
	return nil
}

// readValueFlags reads --row-id, --set and --reason into in. A command that
// does not register them reads none.
func (in *resolveInput) readValueFlags(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("row-id") == nil {
		return nil
	}
	in.valueFlags = true
	if cmd.Flags().Changed("row-id") {
		ids, _ := cmd.Flags().GetIntSlice("row-id")
		seen := make(map[int]bool, len(ids))
		in.rowIDs = make([]int, 0, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return qerrors.NewBadUsagef("invalid --row-id %d: expected a positive row id", id)
			}
			if !seen[id] {
				seen[id] = true
				in.rowIDs = append(in.rowIDs, id)
			}
		}
	}
	if cmd.Flags().Changed("set") {
		pairs, _ := cmd.Flags().GetStringArray("set")
		set, err := parseSetPairs(pairs)
		if err != nil {
			return err
		}
		in.set = set
	}
	if cmd.Flags().Changed("reason") {
		reason, _ := cmd.Flags().GetString("reason")
		if reason == "" {
			return qerrors.NewBadUsage("--reason cannot be empty: omit it to record no reason")
		}
		in.reason = &reason
	}
	return nil
}

// parseSetPairs reads --set's column=value pairs. The value may be empty and
// may contain '='; the column may not be empty or repeated.
func parseSetPairs(pairs []string) (map[string]string, error) {
	set := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		column, value, ok := strings.Cut(pair, "=")
		if !ok || column == "" {
			return nil, qerrors.NewBadUsagef("invalid --set %q: expected <column>=<value>", pair)
		}
		if _, dup := set[column]; dup {
			return nil, qerrors.NewBadUsagef("--set names column %q twice: give each column once", column)
		}
		set[column] = value
	}
	return set, nil
}

// checkValueFlags refuses a value flag the action does not read, a value flag
// on an action the flags cannot drive, and a missing flag the action requires.
func (in resolveInput) checkValueFlags() error {
	if !in.valueFlags {
		return nil
	}
	fields := api.WarningActionFields[in.action]
	given := map[string]bool{"--row-id": in.rowIDs != nil, "--set": in.set != nil, "--reason": in.reason != nil}
	for _, flag := range []string{"--row-id", "--set", "--reason"} {
		if given[flag] && !fields.Accepts(valueFlagField(flag)) {
			return qerrors.NewBadUsagef("%s sets %s, and action %q does not read it",
				flag, valueFlagField(flag), in.action)
		}
	}
	if !fields.NeedsInput() {
		return nil
	}
	if !warnings.FlagsCanSupply(in.action) {
		if flag := in.valueFlag(); flag != "" {
			return qerrors.NewBadUsagef(
				"action %q needs request fields (%s) that the flags cannot supply — pass them in a manifest:\n"+
					"  qctl warning %s %d --action %s --dry-run > fix.yaml   # edit it\n"+
					"  qctl warning %s -f fix.yaml",
				in.action, strings.Join(fields.InputNames(), ", "), in.verb, in.warningID, in.action, in.verb)
		}
		return nil
	}
	for _, name := range fields.Required {
		if flag := warnings.FlagFor(name); !given[flag] {
			return qerrors.NewBadUsagef("action %q needs %s:\n  %s",
				in.action, flag, warnings.FlagForm(in.warningID, in.action))
		}
	}
	return nil
}

// valueFlagField is the request field a value flag sets.
func valueFlagField(flag string) string {
	switch flag {
	case "--row-id":
		return "ids"
	case "--set":
		return "field_name_to_value"
	}
	return "reason"
}

// runResolveFile applies every document of path in file order.
func runResolveFile(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		// Exit 1, not 2: an unreadable path is an I/O failure, not a manifest
		// qctl parsed and rejected.
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
	}
	if len(docs) == 0 {
		return qerrors.NewBadUsagef("no YAML documents found in %s", path)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	failFast, _ := cmd.Flags().GetBool("fail-fast")

	w := cmdutil.NewResultWriter(cmd)
	var resolved []any
	var names []string
	runErr := pkgmanifest.Run(w.Human, docs, failFast, func(_ int, doc []byte) (string, error) {
		line, result, err := applyResolutionDocument(cmd, ctx, client, doc)
		if err == nil {
			resolved = append(resolved, result)
			names = append(names, warningName(result.ID))
		}
		return line, err
	})
	writeErr := w.WriteRun(len(docs), resolved, names...)
	if runErr != nil {
		return runErr
	}
	return writeErr
}

// applyResolutionDocument runs one document end to end and returns the outcome
// line and the result document.
func applyResolutionDocument(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *warnings.Client, doc []byte) (string, resolvedWarning, error) {
	res, err := warnings.ParseResolution(doc)
	if err != nil {
		return "", resolvedWarning{}, err
	}
	// apiVersion before kind, and an absent one refused like a wrong one — see
	// the alert side's copy for why, and internal/commands/apply for the five
	// siblings this is spelled identically to.
	if res.APIVersion != pkgmanifest.APIVersionV1 {
		return "", resolvedWarning{}, qerrors.NewBadUsagef(
			"expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, res.APIVersion)
	}
	if res.Kind != warnings.WarningResolutionKind {
		// alerts.AlertResolutionKind, not the literal; see the alert side's
		// mirror of this check.
		if res.Kind == alerts.AlertResolutionKind {
			return "", resolvedWarning{}, qerrors.NewBadUsagef(
				"kind %s belongs to 'qctl alert resolve -f', not to 'qctl warning resolve -f'",
				alerts.AlertResolutionKind)
		}
		return "", resolvedWarning{}, qerrors.NewBadUsagef(
			"kind %q is not accepted here: 'qctl warning resolve -f' takes %s", res.Kind, warnings.WarningResolutionKind)
	}
	warningID, err := res.WarningID()
	if err != nil {
		return "", resolvedWarning{}, err
	}
	if err := res.Spec.Validate(); err != nil {
		return "", resolvedWarning{}, err
	}

	token := ctx.Credential.AccessToken
	warning, err := client.GetWarning(token, warningID)
	if err != nil {
		return "", resolvedWarning{}, fmt.Errorf("failed to get warning %d: %w", warningID, err)
	}
	// ValidateOffered, not ValidateAction: the NeedsInput arm refuses exactly
	// the actions a manifest exists to drive, and the manifest has already been
	// checked against the same api.WarningActionFields contract by Spec.Validate.
	if err := warnings.ValidateOffered(warning, res.Spec.Action); err != nil {
		return "", resolvedWarning{}, err
	}
	if err := warnings.CheckRuleOutputs(warning, res.Spec.Action, res.Spec.FieldNameToValue); err != nil {
		return "", resolvedWarning{}, err
	}
	if err := confirmResolution(cmd, warning, res.Spec.Action); err != nil {
		return "", resolvedWarning{}, err
	}

	response, err := client.Resolve(token, warningID, res.Spec.ToRequest())
	if err != nil {
		return "", resolvedWarning{}, resolveexplain.Warning(err, warning, res.Spec.Action)
	}
	staged := resolveexplain.Staged(ctx, warning.DatasetID, response.DatasetRuleSuggestionID)
	after, readErr := client.GetWarning(token, warningID)
	result := newResolvedWarning(warning, res.Spec.Action, after, readErr, staged)
	return outcomeLine(warningID, res.Spec.Action, after, readErr, staged), result, nil
}

// outcomeLine is the outcome of action sent to warning warningID, from the
// re-read after it, that read's error and the change the resolve staged, nil
// when it staged none.
func outcomeLine(warningID int, action string, after *warnings.WarningFull, readErr error,
	staged *resolveexplain.StagedKeyword,
) string {
	switch {
	case staged != nil:
		rerun := "qctl warning resolve -f <file>   # the same document"
		report := staged.Report("warning", warningID, readErr == nil && isResolved(after), readErr, rerun)
		return strings.TrimRight(report, "\n")
	case readErr != nil:
		return fmt.Sprintf("warning %d — action %s sent", warningID, action)
	case isResolved(after):
		return fmt.Sprintf("warning %d resolved — action %s", warningID, action)
	default:
		return fmt.Sprintf("warning %d — action %s accepted, but the warning is still unresolved", warningID, action)
	}
}

// confirmResolution warns when the resolve obsoletes the warning's change
// proposal, then gates ignore_column_for_future_anomalies on both paths.
//
// Every exit from the prompt passes through asDeclinedDocument, so a decline is
// labelled once no matter which gate raised it — the ignore-column prompt
// included.
func confirmResolution(cmd *cobra.Command, warning *warnings.WarningFull, action string) error {
	fmt.Fprint(cmd.ErrOrStderr(), alerts.HeldByResolveWarning(warning.ChangeProposal, "warning", warning.ID))
	return asDeclinedDocument(promptForResolution(cmd, warning, action))
}

// declinedDocument is a decline at the prompt, carried so that
// pkgmanifest.Run recognises it (errors.Is against pkgmanifest.ErrDeclined)
// while the user still reads cmdutil.ErrDeclined's own message. Wrapping BOTH
// is the point: qctl has one word for a decline — "aborted", shared by all
// thirteen cmdutil.Confirm call sites — and this must not become the one
// command that says something else.
//
// Two sentinels exist because internal/pkg/manifest cannot import cmdutil:
// cmdutil already depends on that package, so the arrow only runs one way and
// the join has to happen here.
//
// The alert side carries the identical three declarations, with the reasoning
// in full. The two resolve packages are deliberate twins.
type declinedDocument struct{ err error }

func (d declinedDocument) Error() string   { return d.err.Error() }
func (d declinedDocument) Unwrap() []error { return []error{d.err, pkgmanifest.ErrDeclined} }

// asDeclinedDocument re-labels a decline and nothing else: a non-interactive
// stdin stays qerrors bad usage (exit 2) and a broken prompt stays its own
// failure, both propagating untouched.
func asDeclinedDocument(err error) error {
	if err != nil && errors.Is(err, cmdutil.ErrDeclined) {
		return declinedDocument{err: err}
	}
	return err
}

// promptForResolution raises the gate for one action, or none.
//
// warnings.ActionPrompts decides, so this gate and the --yes the NEXT ACTIONS
// footer appends cannot disagree. ignore_column_for_future_anomalies is the
// one action it names, which is why the prompt past it is the column ignore's.
func promptForResolution(cmd *cobra.Command, warning *warnings.WarningFull, action string) error {
	if !warnings.ActionPrompts(action) {
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	return confirmIgnoreColumn(cmd.ErrOrStderr(), warning, yes)
}
