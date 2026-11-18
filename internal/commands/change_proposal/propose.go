package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// AddAddressedFlags registers --also-alert and --also-warning.
func AddAddressedFlags(cmd *cobra.Command) {
	cmd.Flags().IntSlice("also-alert", nil, "Another alert of the same table the resolution also resolves (repeatable)")
	cmd.Flags().IntSlice("also-warning", nil, "Another warning of the same table the resolution also resolves (repeatable)")
}

// AddProposeFlags registers the flags `alert propose` and `warning propose`
// add to the resolve flag matrix.
func AddProposeFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("draft", false, "Save a draft instead of submitting the proposal for review")
	AddAddressedFlags(cmd)
	addDependsOnFlag(cmd)
	addNoteFlag(cmd)
}

// AddRuleProposeFlags registers the flags `table-rule propose` shares with
// `alert propose` and `warning propose`: --draft, --also-alert,
// --also-warning, --depends-on and --note.
func AddRuleProposeFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("draft", false, "Save a draft instead of submitting the proposal for review")
	cmd.Flags().IntSlice("also-alert", nil, "An alert this rule raised that the params change also addresses (repeatable)")
	cmd.Flags().IntSlice("also-warning", nil,
		"A warning this rule raised on a quarantined row that the params change also addresses (repeatable)")
	addDependsOnFlag(cmd)
	addNoteFlag(cmd)
}

// addNoteFlag registers --note.
func addNoteFlag(cmd *cobra.Command) {
	cmd.Flags().String("note", "", "Your rationale for the change, shown to the approver")
}

// readNote reads --note: nil when not given; a blank one is bad usage
// (exit 2). The note is sent with surrounding whitespace trimmed.
func readNote(cmd *cobra.Command) (*string, error) {
	if !cmd.Flags().Changed("note") {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString("note")
	note := strings.TrimSpace(raw)
	if note == "" {
		return nil, qerrors.NewBadUsage("--note is blank; give the rationale, or leave --note out")
	}
	return &note, nil
}

// addDependsOnFlag registers --depends-on.
func addDependsOnFlag(cmd *cobra.Command) {
	cmd.Flags().String("depends-on", "",
		"A live proposal of the same table that must be executed or obsolete before this one can be approved")
}

// readDependsOn reads --depends-on: nil when not given; anything but a UUID
// is bad usage (exit 2).
func readDependsOn(cmd *cobra.Command) (*uuid.UUID, error) {
	if !cmd.Flags().Changed("depends-on") {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString("depends-on")
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid --depends-on %q: expected a change proposal UUID", raw)
	}
	return &id, nil
}

// Addressed is the --also-alert and --also-warning lists.
type Addressed struct {
	Alerts   []int
	Warnings []int
	// Set reports whether either list is being changed.
	Set bool
}

// ReadAddressed reads and checks --also-alert and --also-warning; a
// non-positive id is bad usage (exit 2).
func ReadAddressed(cmd *cobra.Command) (Addressed, error) {
	var a Addressed
	a.Alerts, _ = cmd.Flags().GetIntSlice("also-alert")
	a.Warnings, _ = cmd.Flags().GetIntSlice("also-warning")
	a.Set = cmd.Flags().Changed("also-alert") || cmd.Flags().Changed("also-warning")
	for _, f := range []struct {
		name string
		ids  []int
	}{{"--also-alert", a.Alerts}, {"--also-warning", a.Warnings}} {
		for _, id := range f.ids {
			if id <= 0 {
				return a, qerrors.NewBadUsagef("invalid %s %d: expected a positive id", f.name, id)
			}
		}
	}
	return a, nil
}

// ProposeOptions is what `alert propose` and `warning propose` send besides
// the payload.
type ProposeOptions struct {
	Draft     bool
	Addressed Addressed
	DependsOn *uuid.UUID
	Note      *string
}

// ReadProposeOptions reads --draft, --also-alert, --also-warning,
// --depends-on and --note. Under --dry-run, which sends nothing, any of them
// is bad usage (exit 2).
func ReadProposeOptions(cmd *cobra.Command, dryRun bool) (ProposeOptions, error) {
	var o ProposeOptions
	o.Draft, _ = cmd.Flags().GetBool("draft")
	addressed, err := ReadAddressed(cmd)
	if err != nil {
		return o, err
	}
	o.Addressed = addressed
	if o.DependsOn, err = readDependsOn(cmd); err != nil {
		return o, err
	}
	if o.Note, err = readNote(cmd); err != nil {
		return o, err
	}
	if dryRun && (o.Draft || o.Addressed.Set || o.DependsOn != nil || o.Note != nil) {
		return o, qerrors.NewBadUsage("--dry-run prints a manifest and sends nothing; --draft, --also-alert, --also-warning, --depends-on and --note apply when you propose it with -f")
	}
	return o, nil
}

// ActionDocument builds the document for --action on target and checks it
// against the action's field contract: an action that needs request fields is
// bad usage (exit 2) naming them.
func ActionDocument(op change_proposals.Operation, targetID int, action string) (*change_proposals.Document, error) {
	var doc *change_proposals.Document
	var err error
	if op == api.ChangeReviewOperationAlertResolve {
		spec := alerts.AlertResolutionSpec{Action: action}
		doc, err = change_proposals.AlertDocument(targetID, spec), spec.Validate()
	} else {
		spec := warnings.WarningResolutionSpec{Action: action}
		doc, err = change_proposals.WarningDocument(targetID, spec), spec.Validate()
	}
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// CheckTarget reads the document's target issue and checks that it offers
// the document's action (for an alert's column mappings, that it awaits
// them), then an apply_migration's key choice against the table's key step
// (alerts.KeyChoicePreflight, warning on warn and naming the key fields as
// keyForm). fromFlag is true when the action came from --action, which
// refuses a warning action that needs input.
func CheckTarget(ctx *cmdutil.CommandContext, warn io.Writer, doc *change_proposals.Document, fromFlag bool,
	keyForm alerts.KeyChoiceForm,
) error {
	token := ctx.Credential.AccessToken
	if doc.Alert != nil {
		alert, err := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetAlert(token, doc.TargetID)
		if err != nil {
			return fmt.Errorf("failed to get alert %d: %w", doc.TargetID, err)
		}
		if err := doc.Alert.ValidateAlert(alert); err != nil {
			return err
		}
		_, err = alerts.KeyChoicePreflight(ctx, warn, alert.DatasetID, *doc.Alert, keyForm)
		return err
	}
	warning, err := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetWarning(token, doc.TargetID)
	if err != nil {
		return fmt.Errorf("failed to get warning %d: %w", doc.TargetID, err)
	}
	if fromFlag {
		return warnings.ValidateAction(warning, doc.Warning.Action)
	}
	return warnings.ValidateOffered(warning, doc.Warning.Action)
}

// Create checks doc against its target and creates the proposal.
func (o ProposeOptions) Create(cmd *cobra.Command, ctx *cmdutil.CommandContext, doc *change_proposals.Document, fromFlag bool) (*change_proposals.Proposal, error) {
	keyForm := alerts.KeyChoiceManifest
	if fromFlag {
		keyForm = alerts.KeyChoiceFlags
	}
	if err := CheckTarget(ctx, cmd.ErrOrStderr(), doc, fromFlag, keyForm); err != nil {
		return nil, err
	}
	body, err := doc.CreateRequest(o.Addressed.Alerts, o.Addressed.Warnings, !o.Draft)
	if err != nil {
		return nil, err
	}
	body.DependsOnID = o.DependsOn
	body.Note = o.Note
	proposal, err := newClient(ctx).Create(cmd.Context(), ctx.Credential.AccessToken, body)
	if err != nil {
		return nil, change_proposals.Explain(err)
	}
	return proposal, nil
}

// CreateRuleParams creates a dataset_rule_patch proposal that replaces
// ruleID's params with params.
func (o ProposeOptions) CreateRuleParams(cmd *cobra.Command, ctx *cmdutil.CommandContext, ruleID uuid.UUID, params map[string]interface{}) (*change_proposals.Proposal, error) {
	body := change_proposals.RuleParamsRequest(ruleID, params, o.Addressed.Alerts, o.Addressed.Warnings, !o.Draft)
	body.DependsOnID = o.DependsOn
	body.Note = o.Note
	proposal, err := newClient(ctx).Create(cmd.Context(), ctx.Credential.AccessToken, body)
	if err != nil {
		return nil, change_proposals.Explain(err)
	}
	return proposal, nil
}

// ProposeAction proposes --action on one target and writes the result.
func (o ProposeOptions) ProposeAction(cmd *cobra.Command, op change_proposals.Operation, targetID int, action string) error {
	doc, err := ActionDocument(op, targetID, action)
	if err != nil {
		return err
	}
	return o.ProposeDocument(cmd, doc)
}

// ProposeDocument proposes a validated document and writes the result.
func (o ProposeOptions) ProposeDocument(cmd *cobra.Command, doc *change_proposals.Document) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	proposal, err := o.Create(cmd, ctx, doc, true)
	if err != nil {
		return err
	}
	return WriteResult(cmd, proposal, "created")
}

// ProposeFile creates one proposal per document of path, in file order. Each
// document must be of kind; the --also-* lists name the issues of a single
// proposal, so they are bad usage (exit 2) with a file of several documents.
func (o ProposeOptions) ProposeFile(cmd *cobra.Command, path, kind string) error {
	docs, err := readDocuments(path)
	if err != nil {
		return err
	}
	if len(docs) > 1 && o.Addressed.Set {
		return qerrors.NewBadUsagef(
			"--also-alert and --also-warning name the issues of one proposal; %s holds %d documents", path, len(docs))
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	w := cmdutil.NewResultWriter(cmd)
	var created []any
	var names []string
	var last *change_proposals.Proposal
	runErr := pkgmanifest.Run(w.Human, docs, failFast, func(_ int, raw []byte) (string, error) {
		doc, err := change_proposals.ParseDocument(raw)
		if err != nil {
			return "", err
		}
		if doc.Kind() != kind {
			return "", qerrors.NewBadUsagef("kind %s belongs to '%s', not to '%s -f'",
				doc.Kind(), proposeCommandFor(doc.Kind()), cmd.CommandPath())
		}
		proposal, err := o.Create(cmd, ctx, doc, false)
		if err != nil {
			return "", err
		}
		created = append(created, proposal)
		names = append(names, proposalName(proposal))
		last = proposal
		return fmt.Sprintf("%s created for %s (state: %s)",
			proposalName(proposal), doc.Target(), terminal.Sanitize(string(proposal.State))), nil
	})
	if len(docs) == 1 && last != nil {
		writeNext(w.Human, last)
	}
	writeErr := w.WriteRun(len(docs), created, names...)
	if runErr != nil {
		return runErr
	}
	return writeErr
}

func proposeCommandFor(kind string) string {
	if kind == alerts.AlertResolutionKind {
		return "qctl alert propose -f"
	}
	return "qctl warning propose -f"
}

// readDocuments reads and splits a manifest file. An unreadable file exits 1;
// an unparseable or empty one is bad usage (exit 2).
func readDocuments(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
	}
	if len(docs) == 0 {
		return nil, qerrors.NewBadUsagef("no YAML documents found in %s", path)
	}
	return docs, nil
}

func newClient(ctx *cmdutil.CommandContext) *change_proposals.Client {
	return change_proposals.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
}

// proposalName is the -o name form of a proposal.
func proposalName(p *change_proposals.Proposal) string {
	return "change-proposal/" + p.ID.String()
}

// WriteResult prints "change-proposal/<id> <verb> (state: ...)", a Next line
// per step the proposal now allows you and the describe hint, and under
// -o json|yaml|name the proposal or its name.
func WriteResult(cmd *cobra.Command, p *change_proposals.Proposal, verb string) error {
	w := cmdutil.NewResultWriter(cmd)
	writeLead(w.Human, p, verb)
	writeNext(w.Human, p)
	return w.Write(p, proposalName(p))
}

func writeLead(out io.Writer, p *change_proposals.Proposal, verb string) {
	fmt.Fprintf(out, "%s %s (state: %s, version %d)\n",
		proposalName(p), verb, terminal.Sanitize(string(p.State)), p.VersionID)
}

func writeNext(out io.Writer, p *change_proposals.Proposal) {
	for _, action := range p.AllowedActions {
		if command, ok := change_proposals.NextCommand(action, p); ok {
			fmt.Fprintf(out, "Next: %s\n", command)
		}
	}
	fmt.Fprintf(out, "Next: qctl describe change-proposal %s\n", p.ID)
}
