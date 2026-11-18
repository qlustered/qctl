package alert

import (
	"errors"
	"fmt"
	"os"
	"slices"
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

// resolveInput is the one place the flag matrix is decided, so every refusal
// is spelled once and every arm downstream can assume a coherent combination.
type resolveInput struct {
	file    string
	alertID int
	action  string
	dryRun  bool
	verb    string // the command's name: resolve or propose
	// fileID is --file-id, 0 when it was not given or the command has no such
	// flag.
	fileID int
	// keyColumns is --key-columns and noKeyColumns --no-key-columns.
	keyColumns   []string
	noKeyColumns bool
	// comment is --comment, nil when it was not given.
	comment *string
}

// keyFlag names the key flag given, "" when neither is.
func (in resolveInput) keyFlag() string {
	switch {
	case len(in.keyColumns) > 0:
		return "--key-columns"
	case in.noKeyColumns:
		return "--no-key-columns"
	}
	return ""
}

// spec is the resolution the flags name.
func (in resolveInput) spec() alerts.AlertResolutionSpec {
	spec := alerts.AlertResolutionSpec{Action: in.action, KeyColumns: in.keyColumns}
	if in.fileID != 0 {
		spec.StoredItemID = &in.fileID
	}
	if in.noKeyColumns {
		spec.NoKeyColumns = &in.noKeyColumns
	}
	spec.Comment = in.comment
	return spec
}

// resolveInputs enforces the matrix. Every failure is exit 2 and names the fix.
//
// It runs BEFORE cmdutil.Bootstrap on purpose: a wrong combination of flags
// must cost nothing, not a token lookup.
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
			"cannot use -f together with an alert id: with -f the id comes from each document's metadata.id")
	case in.file == "" && len(args) == 0:
		return in, qerrors.NewBadUsage(
			"give an alert id or a manifest:\n" +
				"  qctl alert " + in.verb + " <alert-id> --action <action>\n" +
				"  qctl alert " + in.verb + " -f <file.yaml>")
	}

	if in.file != "" {
		return in, in.checkFileFlags()
	}
	if err := in.checkDryRunFlags(); err != nil {
		return in, err
	}

	if failFast {
		return in, qerrors.NewBadUsage("--fail-fast applies only to -f, which names the documents it would stop at")
	}
	if format, _ := cmd.Flags().GetString("output"); in.dryRun && (format == string(output.FormatJSON) || format == string(output.FormatName)) {
		return in, qerrors.NewBadUsagef(
			"--dry-run writes a YAML manifest; it cannot be combined with -o %s (use -o yaml or drop -o)", format)
	}
	return in.withAlertID(args[0])
}

// readValueFlags reads --file-id, the key flags and --comment into in.
func (in *resolveInput) readValueFlags(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("file-id") != nil {
		id, _, err := cmdutil.PositiveIDFlag(cmd, "file-id")
		if err != nil {
			return err
		}
		in.fileID = id
	}
	if err := in.readKeyFlags(cmd); err != nil {
		return err
	}
	if cmd.Flags().Changed("comment") {
		comment, _ := cmd.Flags().GetString("comment")
		if err := cmdutil.ValidateCommentText("--comment", comment); err != nil {
			return err
		}
		in.comment = &comment
	}
	return nil
}

// checkFileFlags refuses the flags -f takes from each document instead.
func (in resolveInput) checkFileFlags() error {
	switch {
	case in.action != "":
		return qerrors.NewBadUsage(
			"--action cannot be combined with -f: the action is spec.action in each document")
	case in.dryRun:
		return qerrors.NewBadUsage(
			"--dry-run generates a manifest from an alert id; it cannot be combined with -f")
	case in.fileID != 0:
		return qerrors.NewBadUsage(
			"--file-id cannot be combined with -f: the file is spec.stored_item_id in each document")
	case in.keyFlag() != "":
		return qerrors.NewBadUsagef(
			"%s cannot be combined with -f: the key choice is spec.key_columns or spec.no_key_columns in each document",
			in.keyFlag())
	case in.comment != nil:
		return qerrors.NewBadUsage(
			"--comment cannot be combined with -f: the comment is spec.comment in each document")
	}
	return nil
}

// checkDryRunFlags refuses the flags --dry-run takes from the manifest it
// writes instead.
func (in resolveInput) checkDryRunFlags() error {
	switch {
	case !in.dryRun:
		return nil
	case in.fileID != 0:
		return qerrors.NewBadUsage(
			"--file-id cannot be combined with --dry-run: set spec.stored_item_id in the manifest it writes")
	case in.keyFlag() != "":
		return qerrors.NewBadUsagef(
			"%s cannot be combined with --dry-run: set spec.key_columns or spec.no_key_columns in the manifest it writes",
			in.keyFlag())
	case in.comment != nil:
		return qerrors.NewBadUsage(
			"--comment cannot be combined with --dry-run: set spec.comment in the manifest it writes")
	}
	return nil
}

// readKeyFlags reads --key-columns and --no-key-columns, refusing both
// together and an empty --key-columns.
func (in *resolveInput) readKeyFlags(cmd *cobra.Command) error {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"key-columns", "no-key-columns"}); err != nil {
		return err
	}
	in.keyColumns, _ = cmd.Flags().GetStringSlice("key-columns")
	in.noKeyColumns, _ = cmd.Flags().GetBool("no-key-columns")
	if cmd.Flags().Changed("key-columns") && len(in.keyColumns) == 0 {
		return qerrors.NewBadUsage("--key-columns names no column: give one or more, or --no-key-columns for a table with none")
	}
	return nil
}

func (in resolveInput) withAlertID(value string) (resolveInput, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return in, qerrors.NewBadUsagef("invalid alert ID: %s", value)
	}
	in.alertID = id
	if in.action == "" && !in.dryRun {
		return in, qerrors.NewBadUsagef(
			"--action is required: qctl alert %s %d --action <action>\n"+
				"list the actions this alert offers with: qctl describe alert %d", in.verb, id, id)
	}
	if in.fileID != 0 && !api.AlertActionFields[in.action].Accepts("stored_item_id") {
		return in, qerrors.NewBadUsagef(
			"--file-id names one file of the alert, and action %q takes no file (it does not read stored_item_id)",
			in.action)
	}
	fields := api.AlertActionFields[in.action]
	switch {
	case in.comment != nil && !fields.Accepts("comment"):
		return in, qerrors.NewBadUsagef(
			"--comment says why files are trashed, and action %q takes no comment", in.action)
	case in.comment == nil && slices.Contains(fields.Required, "comment"):
		return in, qerrors.NewBadUsagef(
			"action %q needs --comment: why the files are trashed, recorded with each one\n"+
				"  qctl alert %s %d --action %s --comment <text>", in.action, in.verb, id, in.action)
	}
	if flag := in.keyFlag(); flag != "" && !api.AlertActionFields[in.action].Accepts("key_columns") {
		return in, qerrors.NewBadUsagef(
			"%s chooses a table's key columns on apply_migration, and action %q takes no key choice", flag, in.action)
	}
	return in, nil
}

// runResolveFile applies every document of path in file order.
func runResolveFile(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		// Exit 1, not 2: an unreadable path is an I/O failure, not a manifest
		// qctl parsed and rejected. Same call as apply's readManifestDocuments.
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
	client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	failFast, _ := cmd.Flags().GetBool("fail-fast")

	w := cmdutil.NewResultWriter(cmd)
	var resolved []any
	var names []string
	runErr := pkgmanifest.Run(w.Human, docs, failFast, func(_ int, doc []byte) (string, error) {
		line, result, err := applyResolutionDocument(cmd, ctx, client, doc)
		if err == nil {
			resolved = append(resolved, result)
			names = append(names, alertName(result.ID))
		}
		return line, err
	})
	writeErr := w.WriteRun(len(docs), resolved, names...)
	if runErr != nil {
		return runErr
	}
	return writeErr
}

// applyResolutionDocument runs one document end to end: parse, envelope check,
// contract check, the alert's own action list, the prompt, and the call. It
// returns the outcome line and the result document.
func applyResolutionDocument(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *alerts.Client, doc []byte) (string, resolvedAlert, error) {
	res, err := alerts.ParseResolution(doc)
	if err != nil {
		return "", resolvedAlert{}, err
	}
	// apiVersion before kind, and an absent one refused like a wrong one —
	// the check every `qctl apply` manifest path makes, spelled the same way
	// (internal/commands/apply/dataset.go, cloud_source.go, linked_table.go,
	// review_policies.go). The generator emits the field and the guide calls it
	// part of the envelope, so parsing it and never reading it advertised a
	// version qctl would ignore on the day it started to matter.
	if res.APIVersion != pkgmanifest.APIVersionV1 {
		return "", resolvedAlert{}, qerrors.NewBadUsagef(
			"expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, res.APIVersion)
	}
	if res.Kind != alerts.AlertResolutionKind {
		// warnings.WarningResolutionKind, not the literal: a rename must not be
		// able to leave this friendly cross-verb message pointing at a kind that
		// no longer exists. internal/warnings depends on neither this package
		// nor internal/alerts, so there is no cycle.
		if res.Kind == warnings.WarningResolutionKind {
			return "", resolvedAlert{}, qerrors.NewBadUsagef(
				"kind %s belongs to 'qctl warning resolve -f', not to 'qctl alert resolve -f'",
				warnings.WarningResolutionKind)
		}
		return "", resolvedAlert{}, qerrors.NewBadUsagef(
			"kind %q is not accepted here: 'qctl alert resolve -f' takes %s", res.Kind, alerts.AlertResolutionKind)
	}
	alertID, err := res.AlertID()
	if err != nil {
		return "", resolvedAlert{}, err
	}
	if err := res.Spec.Validate(); err != nil {
		return "", resolvedAlert{}, err
	}

	token := ctx.Credential.AccessToken
	alert, err := client.GetAlert(token, alertID)
	if err != nil {
		return "", resolvedAlert{}, fmt.Errorf("failed to get alert %d: %w", alertID, err)
	}
	if err := res.Spec.ValidateAlert(alert); err != nil {
		return "", resolvedAlert{}, err
	}
	if err := res.Spec.ValidateScopes(alert); err != nil {
		return "", resolvedAlert{}, err
	}
	if err := checkDependentAlert(alert); err != nil {
		return "", resolvedAlert{}, err
	}
	if err := confirmResolution(cmd, ctx, alert, res.Spec, alerts.KeyChoiceManifest); err != nil {
		return "", resolvedAlert{}, err
	}

	response, err := client.Resolve(token, alertID, res.Spec.ToRequest())
	if err != nil {
		return "", resolvedAlert{}, resolveexplain.Alert(ctx, err, alert, res.Spec.Action)
	}
	staged := resolveexplain.Staged(ctx, alert.DatasetID, response.DatasetRuleSuggestionID)
	after, readErr := client.GetAlert(token, alertID)
	result := newResolvedAlert(alert, res.Spec.Action, after, readErr, staged)
	if staged != nil {
		rerun := "qctl alert resolve -f <file>   # the same document"
		report := staged.Report("alert", alertID, readErr == nil && isResolved(after), readErr, rerun)
		return strings.TrimRight(report, "\n"), result, nil
	}
	note := strings.TrimRight(anomalyResolutionNote(alert, res.Spec.Action), "\n")
	return outcomeLine(alertID, res.Spec, after, readErr) + note, result, nil
}

// outcomeLine is the one-line outcome of spec sent to alert alertID, from the
// re-read after it and that read's error.
func outcomeLine(alertID int, spec alerts.AlertResolutionSpec, after *alerts.AlertFull, readErr error) string {
	description := resolutionDescription(spec.Action)
	switch {
	case readErr != nil:
		return fmt.Sprintf("alert %d — %s sent", alertID, description)
	case after.Resolved != nil && *after.Resolved:
		return fmt.Sprintf("alert %d resolved — %s", alertID, description)
	case spec.StoredItemID != nil:
		return fmt.Sprintf("alert %d — %s accepted for file %d; %s", alertID, description, *spec.StoredItemID, oneFileStillOpen)
	default:
		return fmt.Sprintf("alert %d — %s accepted, but the alert is still unresolved", alertID, description)
	}
}

func resolutionDescription(action string) string {
	if action == "" {
		return "field_names_resolution"
	}
	return "action " + action
}

// confirmResolution warns when the resolve obsoletes the alert's change
// proposal, then runs the protected-context gate for a destructive action,
// then the action's confirmation. An apply_migration first runs the key
// pre-flight (alerts.KeyChoicePreflight), naming the key fields as keyForm.
func confirmResolution(cmd *cobra.Command, ctx *cmdutil.CommandContext, alert *alerts.AlertFull, spec alerts.AlertResolutionSpec,
	keyForm alerts.KeyChoiceForm,
) error {
	migration, err := alerts.KeyChoicePreflight(ctx, cmd.ErrOrStderr(), alert.DatasetID, spec, keyForm)
	if err != nil {
		return err
	}
	action := spec.Action
	fmt.Fprint(cmd.ErrOrStderr(), alerts.HeldByResolveWarning(alert.ChangeProposal, "alert", alert.ID))
	if cmdutil.IsDestructiveAction(action) {
		what := fmt.Sprintf("`%s` with action %s", cmd.CommandPath(), action)
		if err := cmdutil.RequireNamedContext(cmd, what); err != nil {
			return asDeclinedDocument(err)
		}
	}
	return asDeclinedDocument(promptForResolution(cmd, ctx, alert, spec, migration))
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
// The warning side carries the identical three declarations. The two resolve
// packages are deliberate twins (see confirmResolution),
// and a shared copy would have to live in cmdutil, which would make the
// generic prompt helper depend on the manifest package for two callers.
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

// promptForResolution raises the gate for one action, or none. migration is
// the alert's table read for the key pre-flight, nil when it made none.
func promptForResolution(cmd *cobra.Command, ctx *cmdutil.CommandContext, alert *alerts.AlertFull, spec alerts.AlertResolutionSpec,
	migration *alerts.MigrationRead,
) error {
	action := spec.Action
	if !alerts.ActionPrompts(action) {
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if action == string(api.AlertActionIgnoreColumnForFutureAnomalies) {
		return confirmIgnoreColumn(cmd.ErrOrStderr(), alert, yes)
	}
	if migration == nil {
		migration = alerts.ReadMigration(ctx, alert.DatasetID)
	}
	gate, err := loadSuggestionGate(cmdutil.NewResultWriter(cmd).Human, ctx, alert, migration)
	if err != nil {
		return err
	}
	if err := gate.CheckAccepts(spec.AcceptSuggestionIDs); err != nil {
		return err
	}
	fmt.Fprint(cmd.ErrOrStderr(), migrationRuleWarning(ctx, alert, migration))
	cmdutil.WarnRunningMigrationJobs(cmd, ctx, alert.DatasetID)
	return confirmMigration(cmd, gate.WithDecisions(spec.AcceptSuggestionIDs, spec.DismissSuggestionIDs), alert, yes,
		spec.KeyOutcome(migration.Detail))
}
