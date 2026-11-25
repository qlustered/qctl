package apply

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonschemautil"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// getRulesOrgContext retrieves the current organization ID and name for rule operations.
func getRulesOrgContext(ctx *cmdutil.CommandContext) (string, string) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = unknownOrganizationName
	}
	return orgID, orgName
}

// ruleMappedColumns returns the input and role fields, excluding the wildcard.
func ruleMappedColumns(rev *rule_versions.RuleRevisionFull) []string {
	seen := make(map[string]bool)
	var cols []string
	for _, group := range [][]string{rev.InputColumns, rev.ValidatesColumns, rev.CorrectsColumns, rev.EnrichesColumns} {
		for _, col := range group {
			if col == "*" || seen[col] {
				continue
			}
			seen[col] = true
			cols = append(cols, col)
		}
	}
	return cols
}

// requiredRuleColumns excludes optional fields from the binding slots.
func requiredRuleColumns(columns, optionalColumns []string) []string {
	if len(optionalColumns) == 0 {
		return columns
	}
	optional := make(map[string]bool, len(optionalColumns))
	for _, col := range optionalColumns {
		optional[col] = true
	}
	required := make([]string, 0, len(columns))
	for _, col := range columns {
		if !optional[col] {
			required = append(required, col)
		}
	}
	return required
}

// optionalColumnsOf returns a revision's optional binding slots. The generated
// field is a pointer because a spec may omit it entirely.
func optionalColumnsOf(rev *rule_versions.RuleRevisionFull) []string {
	if rev.OptionalColumns == nil {
		return nil
	}
	return *rev.OptionalColumns
}

// validateColumnMappingKeys rejects a column_mapping key that names no input
// or validates/corrects/enriches field of rev.
func validateColumnMappingKeys(provided map[string]string, rev *rule_versions.RuleRevisionFull) error {
	columns := ruleMappedColumns(rev)
	allowed := make(map[string]bool, len(columns))
	for _, col := range columns {
		allowed[col] = true
	}
	var unknown []string
	for key := range provided {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return qerrors.NewBadUsagef("column_mapping contains keys that are not input columns or validates/corrects/enriches columns of this rule revision: [%s] (allowed keys: [%s])",
		strings.Join(unknown, ", "), strings.Join(sortedKeys(allowed), ", "))
}

// joinOrNone renders a column list, or "none" for an empty list.
func joinOrNone(cols []string) string {
	if len(cols) == 0 {
		return "none"
	}
	return strings.Join(cols, ", ")
}

// resolveShorthandColumn binds a revision's single required field to column.
func resolveShorthandColumn(column string, provided map[string]string, rev *rule_versions.RuleRevisionFull) (map[string]string, error) {
	if len(provided) > 0 {
		return nil, qerrors.NewBadUsage("spec.column and spec.column_mapping are mutually exclusive; set one or the other")
	}
	required := requiredRuleColumns(ruleMappedColumns(rev), optionalColumnsOf(rev))
	if len(required) != 1 {
		return nil, qerrors.NewBadUsagef("spec.column needs a rule with exactly one required field (input or validates/corrects/enriches); %s requires %d (%s) -- use column_mapping instead",
			rev.Slug, len(required), joinOrNone(required))
	}
	return map[string]string{required[0]: column}, nil
}

// formatColumnMapping renders sorted "input -> column" pairs.
func formatColumnMapping(mapping map[string]string) string {
	if len(mapping) == 0 {
		return "(none)"
	}
	pairs := make([]string, 0, len(mapping))
	for _, k := range sortedKeys(mapping) {
		pairs = append(pairs, fmt.Sprintf("%s -> %s", k, mapping[k]))
	}
	return strings.Join(pairs, ", ")
}

// patchRuleRevision fetches the revision a patch manifest has to be judged
// against -- the rebind target when it rebinds, else the rule's current one --
// and only when the manifest sets something that needs judging. A manifest
// that sets neither spec.params nor spec.produced_fields never pays for it.
//
// One fetch serves both: the params describe the revision the rule is moving
// TO, so the target's param_schema wins over the live rule's, and so does its
// enriched_field_schemas for a produced field the target declares.
func patchRuleRevision(rvClient *rule_versions.Client, token string, manifest *dataset_rules.TableRuleApplyManifest, detail *dataset_rules.DatasetRuleDetail) (*rule_versions.RuleRevisionFull, error) {
	if !dataset_rules.NeedsRuleRevision(manifest) {
		return nil, nil
	}
	revID := dataset_rules.PatchRevisionID(manifest, detail)
	revDetail, err := rvClient.GetRuleRevisionDetails(token, revID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rule revision %s: %w", revID, err)
	}
	return revDetail, nil
}

// applyTableRuleYAML is the entry point for the generic "apply -f" dispatcher.
// It checks the manifest with loadTableRuleManifest, bootstraps auth context,
// and delegates to applyTableRuleManifest.
func applyTableRuleYAML(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	manifest, err := loadTableRuleManifest(doc)
	if err != nil {
		return nil, err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}

	orgID, orgName := getRulesOrgContext(ctx)

	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create dataset rules client: %w", err)
	}

	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create rule versions client: %w", err)
	}

	opts, err := tableRuleOptions(cmd, ctx)
	if err != nil {
		return nil, err
	}
	result, err := applyTableRuleManifest(humanOut(cmd), cmd.ErrOrStderr(), client, rvClient, ctx.Credential.AccessToken, doc, manifest, orgID, orgName, ctx.ServerURL, opts)
	switch item := result.(type) {
	case *dataset_rules.DatasetRuleDetail:
		return namedApplyResult(item, "table-rule", item.ID), err
	case *dataset_rules.Suggestion:
		return namedApplyResult(item, "rule-suggestion", item.ID), err
	case *dataset_rules.InstantiateSuggestion:
		return namedApplyResult(item, "rule-suggestion", item.ID), err
	case nil:
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("unexpected table rule apply result %T", result)
}

// loadTableRuleManifest parses one TableRule document and makes every verdict
// on it that needs no request: the apiVersion, the kind, the instance name,
// the mode, and the mode's own manifest-only rules.
func loadTableRuleManifest(doc manifestDoc) (*dataset_rules.TableRuleApplyManifest, error) {
	var manifest dataset_rules.TableRuleApplyManifest
	if err := yaml.Unmarshal(doc.Data, &manifest); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", doc.Source))
	}

	if err := dataset_rules.ValidateManifestAPIVersion(manifest.APIVersion, doc.Source); err != nil {
		return nil, err
	}
	if manifest.Kind != "TableRule" {
		return nil, qerrors.NewBadUsagef("unsupported kind %q in %s (expected: TableRule)", manifest.Kind, doc.Source)
	}
	// Before either mode reads spec.instance_name: `get table-rule -o yaml`
	// writes the name as metadata.instance_name.
	if err := dataset_rules.ReconcileInstanceName(&manifest, doc.Source); err != nil {
		return nil, err
	}

	switch {
	case manifest.IsPatch():
		// Every pre-request verdict on the manifest, in one call `qctl diff -f`
		// makes too -- so the preview cannot bless a manifest this rejects.
		if err := dataset_rules.ValidatePatchManifest(&manifest, doc.Source); err != nil {
			return nil, err
		}
	case manifest.IsInstantiate():
		if err := manifest.Spec.RowFilter.Validate(true, "spec.row_filter in "+doc.Source); err != nil {
			return nil, err
		}
		if manifest.Spec.DatasetID == 0 {
			return nil, qerrors.NewBadUsagef("spec.dataset_id is required for instantiate in %s", doc.Source)
		}
		// The patch path reaches this through ValidatePatchManifest, which
		// instantiate never calls; both modes send spec.severity, so both check it
		// before any request goes out.
		if err := dataset_rules.ValidateManifestSeverity(&manifest, doc.Source); err != nil {
			return nil, err
		}
		if err := dataset_rules.ValidateManifestParams(&manifest, doc.Source); err != nil {
			return nil, err
		}
	default:
		return nil, qerrors.NewBadUsagef("cannot determine operation for %s: provide metadata.id for patch, or spec.rule_revision_id / spec.rule for instantiate", doc.Source)
	}
	return &manifest, nil
}

// applyTableRuleManifest applies a manifest loadTableRuleManifest accepted and
// returns its -o json|yaml document. promptOut receives prompts, duplicate
// warnings and --wait progress.
func applyTableRuleManifest(out, promptOut io.Writer, client *dataset_rules.Client, rvClient *rule_versions.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, orgID, orgName, serverURL string, opts tableRuleApplyOptions) (any, error) {
	if manifest.IsPatch() {
		result, err := applyTableRulePatch(out, promptOut, client, rvClient, token, doc, orgID, orgName, manifest, opts.yes)
		if err != nil {
			return nil, err
		}
		if opts.wait != nil {
			writeNothingToWaitFor(promptOut, fmt.Sprintf("%s patches table rule %s; --wait waits only for the validation of a rule a document creates",
				doc.Source, terminal.Sanitize(manifest.Metadata.ID)))
		}
		return result, nil
	}
	return applyTableRuleInstantiate(out, promptOut, client, rvClient, token, doc, orgID, orgName, serverURL, manifest, opts)
}

// preparePatchBody turns a resolved patch manifest into the merge-patch body,
// doing every check that has to happen before the request and returning the
// live rule alongside, which the confirmation block renders old -> new against.
//
// The manifest-sets-nothing rejection stays first, so it is still reached
// before any request. What it cannot speak for is the body AFTER the reconcile
// below, which may drop a key: that emptiness means "unchanged", and belongs to
// the caller, to which the third result names the fields the reconcile dropped.
func preparePatchBody(client *dataset_rules.Client, rvClient *rule_versions.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest) (*dataset_rules.DatasetRuleDetail, map[string]interface{}, []string, error) {
	if len(dataset_rules.BuildPatchMap(manifest)) == 0 {
		return nil, nil, nil, dataset_rules.NoPatchableFieldsError(doc.Source)
	}

	// Fetch the current rule unconditionally, though what it powers is
	// conditional: the produced-field and params checks need it only when the
	// patch sets those, and the old -> new confirmation rendering needs it
	// only when !yes. Under --yes with neither it powers nothing -- one
	// wasted GET per document in a batch.
	detail, err := client.GetDatasetRuleDetail(token, manifest.Metadata.ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch existing table rule: %w", err)
	}

	// One revision fetch serves both checks below, and only happens when the
	// manifest sets something they judge. The resolve above guarantees
	// spec.rule_revision_id is a full UUID here.
	revDetail, err := patchRuleRevision(rvClient, token, manifest, detail)
	if err != nil {
		return nil, nil, nil, err
	}

	// Revision-dependent fields are prepared BEFORE the patch body is built:
	// produced fields may need a schema filled in, and params must satisfy the
	// governing revision's param_schema. The reconcile then compares the
	// resolved declarations against the live ones, which is what refuses a drop
	// the backend would accept and fail to promote, and drops a pure
	// restatement -- of the declarations or of the rule's name -- from the body.
	if err := dataset_rules.PreparePatchRevisionFields(manifest, revDetail, doc.Source); err != nil {
		return nil, nil, nil, err
	}
	restated, err := dataset_rules.ReconcilePatchWithLive(manifest, detail, doc.Source)
	if err != nil {
		return nil, nil, nil, err
	}

	return detail, dataset_rules.BuildPatchMap(manifest), restated, nil
}

// confirmTableRulePatch prints the pre-flight block and asks. It returns
// errDocumentDeclined when the caller says no.
//
// The pre-flight block is the prompt's context, not a result: with --yes there
// is no question, and every value in it is something the caller just wrote in
// the manifest.
func confirmTableRulePatch(out, promptOut io.Writer, doc manifestDoc, orgID, orgName string, manifest *dataset_rules.TableRuleApplyManifest, detail *dataset_rules.DatasetRuleDetail, patch map[string]interface{}, resolvedFrom string, yes bool) error {
	if yes {
		return nil
	}
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), orgID)
	fmt.Fprintf(out, "Table Rule ID: %s\n", manifest.Metadata.ID)
	fmt.Fprintf(out, "Dataset ID   : %d\n", manifest.Spec.DatasetID)
	fmt.Fprintf(out, "File         : %s\n", doc.Source)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Changes to apply:")
	changes := dataset_rules.PatchFieldChanges(detail, patch)
	if resolvedFrom != "" {
		for i := range changes {
			if changes[i].Field == "rule_revision_id" {
				changes[i].New += fmt.Sprintf(" (resolved from %q)", resolvedFrom)
				break
			}
		}
	}
	fmt.Fprint(out, dataset_rules.FormatFieldChanges(changes, "  "))
	fmt.Fprintln(out)

	if !cmdutil.CanPrompt() {
		return qerrors.NewBadUsagef("confirmation required for %s: re-run with --yes (-y) to apply without prompting", doc.Source)
	}
	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Apply these changes to the table rule?")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintf(out, "Skipping %s\n\n", doc.Source)
		// A decline is a skip, not a success: returning nil would let the
		// runner count an un-applied document in "N succeeded".
		return errDocumentDeclined
	}
	fmt.Fprintf(out, "Patching table rule %s...\n", manifest.Metadata.ID)
	return nil
}

// applyTableRulePatch patches an existing table rule and returns the rule as
// the 200 answered it, the live rule when nothing was sent, or the staged
// suggestion of a 202.
func applyTableRulePatch(out, promptOut io.Writer, client *dataset_rules.Client, rvClient *rule_versions.Client, token string, doc manifestDoc, orgID, orgName string, manifest *dataset_rules.TableRuleApplyManifest, yes bool) (any, error) {
	// The rebind. dataset_rules.ResolvePatchRebind is a no-op when the manifest
	// names no rule, and otherwise enforces the spec.rule /
	// spec.rule_revision_id exclusion and the release-needs-rule pairing and
	// rewrites spec.rule_revision_id in place with the resolved UUID, so
	// BuildPatchMap below sees one spelling. Sending that key starts a
	// revalidation candidate (202) rather than editing the live rule -- the
	// live rule keeps its current revision until the candidate's dry run
	// passes. `qctl diff -f` calls the same resolver, so both commands agree.
	resolvedFrom, err := dataset_rules.ResolvePatchRebind(rvClient, token, manifest, doc.Source)
	if err != nil {
		return nil, err
	}

	detail, patch, restated, err := preparePatchBody(client, rvClient, token, doc, manifest)
	if err != nil {
		return nil, err
	}

	// An empty body here is not the "manifest sets nothing" case -- that one is
	// rejected inside preparePatchBody, before any request. It is a manifest
	// whose every patchable key restated the live rule -- its name, or the
	// produced fields it already declares -- which the reconcile drops, so
	// every one of them is in restated. Nothing to send is nothing to report.
	if len(patch) == 0 {
		verb := "matches"
		if len(restated) > 1 {
			verb = "match"
		}
		fmt.Fprintf(out, "Table rule %s unchanged: %s already %s the live rule\n\n", manifest.Metadata.ID, strings.Join(restated, " and "), verb)
		return detail, nil
	}

	if err := confirmTableRulePatch(out, promptOut, doc, orgID, orgName, manifest, detail, patch, resolvedFrom, yes); err != nil {
		return nil, err
	}

	// Patch rule
	outcome, err := client.PatchDatasetRule(token, manifest.Spec.DatasetID, manifest.Metadata.ID, patch)
	if err != nil {
		err = explainRuleChangeReview(explainRebindError(err, doc), manifest.Metadata.ID, manifest.Spec.DatasetID, patch)
		return nil, fmt.Errorf("table rule patch failed for %s: %w", doc.Source, err)
	}

	if outcome.Staged != nil {
		renderStagedOutcome(out, patch, outcome.Staged, manifest.Spec.DatasetID, detail)
		if _, set := patch["row_filter"]; set {
			if !rule_suggestions.IsCandidate(outcome.Staged) {
				if after, readErr := client.GetDatasetRuleDetail(token, detail.ID.String()); readErr == nil {
					offerCleanRowRecheck(out, client, token, detail, after, patch)
				}
			} else {
				fmt.Fprintf(out, "After the filter is applied and the rule enabled: qctl table-rule recheck-clean-rows %s --table-id %d --dry-run\n", detail.ID.String(), detail.DatasetID)
			}
		}
		return outcome.Staged, nil
	}

	renderUpdatedOutcome(out, outcome.Applied)
	offerCleanRowRecheck(out, client, token, detail, outcome.Applied, patch)
	return outcome.Applied, nil
}

// renderUpdatedOutcome prints a 200: the patched rule and its follow-up.
func renderUpdatedOutcome(out io.Writer, applied *dataset_rules.DatasetRuleDetail) {
	fmt.Fprintf(out, "Table rule updated: %s (%s)\n", terminal.Sanitize(applied.InstanceName), applied.ID.String())
	renderPendingMigrationFollowUp(out, applied)
	fmt.Fprintln(out)
}

// renderPendingMigrationFollowUp prints the patched rule's draft and next steps.
func renderPendingMigrationFollowUp(out io.Writer, applied *dataset_rules.DatasetRuleDetail) {
	if applied.PendingMigrationModelID == nil {
		return
	}
	migrationID := *applied.PendingMigrationModelID

	// Every column the rule declares, labelled for what it is. Not "new
	// columns": the merge stamps the whole declaration set into the draft, so
	// a column that is already in the table's schema can be among them, and
	// the response does not say which of them the draft is carrying.
	if produced := dataset_rules.ProducedFieldsFromResponse(applied.ProducedFields); len(produced) > 0 {
		fmt.Fprintf(out, "  produced    : %s\n", terminal.Sanitize(dataset_rules.FormatProducedFields(produced)))
	}
	fmt.Fprintf(out, "  state       : %s\n", terminal.Sanitize(string(applied.State)))
	fmt.Fprintf(out, "  pending     : draft migration %d carries this rule's new columns into the table's schema\n", migrationID)
	fmt.Fprintf(out, "  next        : %s\n", pendingMigrationNextStep(applied))
	fmt.Fprintln(out)

	renderBreadcrumbs(out, []breadcrumb{
		{fmt.Sprintf("qctl describe table-schema --table-id %d", applied.DatasetID), "the draft carrying the columns"},
		{fmt.Sprintf("qctl get alerts --table-id %d --issue-type needs_review_migration", applied.DatasetID), "the review for the draft"},
		{"qctl guide unblock-table", "how this resolves"},
	})
}

// pendingMigrationNextStep describes the next step for a rule with a draft.
func pendingMigrationNextStep(rule *dataset_rules.DatasetRuleDetail) string {
	switch rule.State {
	case api.DatasetRuleStatePendingValidation:
		if rule.ValidationState == nil || *rule.ValidationState != api.ValidationEvidenceKindPassed || rule.ValidationBlocksEnable {
			return "wait for validation to pass before applying the migration; check the rule's state afterward"
		}
		return "apply the migration, then check the rule's state"
	case api.DatasetRuleStateDisabled:
		return "applying that migration does NOT resume a paused rule -- re-apply with spec.state: enabled once validation passes and the columns exist"
	default:
		return "the rule cannot use those columns until that migration is applied"
	}
}

// renderInstantiateConfirmation prints the pre-flight summary for a table
// rule instantiate — organization, rule revision, dataset, the resolved
// instance name, each rule field's binding, and the source file — ahead of the
// confirmation prompt. resolvedFrom is the identifier the manifest actually
// wrote when spec.rule_revision_id was not already a full UUID, so the prompt
// shows both what the user typed and what it resolved to; "" when nothing was
// resolved.
//
// Each bound column carries the accepted field types revDetail declares for
// its field.
func renderInstantiateConfirmation(out io.Writer, orgID, orgName string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull, bindings *instantiateBindings, resolvedFrom string) {
	fmt.Fprintf(out, "Organization     : %s (%s)\n", terminal.Sanitize(orgName), orgID)
	revision := manifest.Spec.RuleRevisionID
	if resolvedFrom != "" {
		revision = fmt.Sprintf("%s (resolved from %q)", revision, resolvedFrom)
	}
	fmt.Fprintf(out, "Rule Revision ID : %s\n", revision)
	fmt.Fprintf(out, "Dataset ID       : %d\n", manifest.Spec.DatasetID)
	instanceName := "(auto-generated)"
	if manifest.Spec.InstanceName != nil && *manifest.Spec.InstanceName != "" {
		instanceName = *manifest.Spec.InstanceName
	}
	fmt.Fprintf(out, "Instance Name    : %s\n", terminal.Sanitize(instanceName))
	fmt.Fprintf(out, "Column Mapping   : %s\n",
		terminal.Sanitize(formatBindingEntries(bindings.confirmationEntries(revDetail), rule_versions.AcceptedColumnTypeText(revDetail))))
	// Only for a rule that declares any: the line would read "(none)" on every
	// validation rule, which is most of them.
	if manifest.Spec.RowFilter != nil {
		fmt.Fprintf(out, "Row Filter : %s\n", terminal.Sanitize(manifest.Spec.RowFilter.DescriptionText()))
	}
	if manifest.Spec.ProducedFields != nil && len(*manifest.Spec.ProducedFields) > 0 {
		fmt.Fprintf(out, "Produced Columns : %s\n", terminal.Sanitize(dataset_rules.FormatProducedFields(*manifest.Spec.ProducedFields)))
	}
	if manifest.Spec.UpgradedFrom != "" {
		fmt.Fprintf(out, "Upgrade From     : %s\n", terminal.Sanitize(manifest.Spec.UpgradedFrom))
	}
	fmt.Fprintf(out, "File             : %s\n", doc.Source)
	fmt.Fprintln(out)
}

// resolveUpgradeSource turns the apply manifest's create-time predecessor
// reference into the UUID the instantiate route accepts and validates the
// relationship before the mutating request. Atlas performs the same checks,
// but doing them here keeps an accidental cross-table or cross-family pairing
// out of the confirmation prompt and out of the POST entirely.
//
// A predecessor outside the active organization is indistinguishable from a
// missing one by design: GetDatasetRuleDetail is organization-scoped and its
// 404/exit-3 error is preserved through the %w chain below.
func resolveUpgradeSource(serverURL string, client *dataset_rules.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull) (*uuid.UUID, error) {
	input := manifest.Spec.UpgradedFrom
	if input == "" {
		return nil, nil
	}

	resolved := input
	aliasID, wasAlias, err := cmdutil.ResolveTableRuleAlias(&cmdutil.CommandContext{ServerURL: serverURL}, input)
	if err != nil {
		return nil, qerrors.NewBadUsagef("spec.upgradedFrom %q in %s cannot be resolved: %v", input, doc.Source, err)
	}
	if wasAlias {
		resolved = aliasID
	}

	predecessorID, err := uuid.Parse(resolved)
	if err != nil {
		return nil, qerrors.NewBadUsagef("spec.upgradedFrom %q in %s must be a table-rule UUID or r-* alias", input, doc.Source)
	}
	predecessor, err := client.GetDatasetRuleDetail(token, predecessorID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch upgrade predecessor %s: %w", predecessorID, err)
	}
	if predecessor.DatasetID != manifest.Spec.DatasetID {
		return nil, qerrors.NewBadUsagef(
			"spec.upgradedFrom %q in %s belongs to dataset %d, not spec.dataset_id %d",
			input, doc.Source, predecessor.DatasetID, manifest.Spec.DatasetID,
		)
	}
	if predecessor.RuleRevision.FamilyID != revDetail.FamilyID {
		return nil, qerrors.NewBadUsagef(
			"spec.upgradedFrom %q in %s belongs to rule family %s, not the new revision's family %s",
			input, doc.Source, predecessor.RuleRevision.FamilyID, revDetail.FamilyID,
		)
	}
	return &predecessorID, nil
}

// prepareInstantiateRule resolves and reads the new revision with its binding
// preview for the table, then validates the optional predecessor against that
// revision. With a predecessor, the revision is read again with a preview that
// inherits the predecessor's bindings.
func prepareInstantiateRule(client *dataset_rules.Client, rvClient *rule_versions.Client, token, serverURL string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest) (string, *rule_versions.RuleRevisionFull, *uuid.UUID, error) {
	resolvedFrom, err := dataset_rules.ResolveManifestRule(rvClient, token, manifest, doc.Source)
	if err != nil {
		return "", nil, nil, err
	}

	revDetail, err := rvClient.GetRuleRevisionBindingPreview(token, manifest.Spec.RuleRevisionID, manifest.Spec.DatasetID, nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to fetch rule revision %s: %w", manifest.Spec.RuleRevisionID, err)
	}

	upgradedFromID, err := resolveUpgradeSource(serverURL, client, token, doc, manifest, revDetail)
	if err != nil {
		return "", nil, nil, err
	}
	if upgradedFromID != nil {
		revDetail, err = rvClient.GetRuleRevisionBindingPreview(token, manifest.Spec.RuleRevisionID, manifest.Spec.DatasetID, upgradedFromID)
		if err != nil {
			return "", nil, nil, fmt.Errorf("failed to fetch rule revision %s with the bindings of %s: %w", manifest.Spec.RuleRevisionID, upgradedFromID, err)
		}
	}
	return resolvedFrom, revDetail, upgradedFromID, nil
}

// validateInstantiateParams checks spec.params, when set, against the
// revision's param_schema.
func validateInstantiateParams(manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull) error {
	if manifest.Spec.Params == nil {
		return nil
	}
	return jsonschemautil.ValidateParams(*manifest.Spec.Params, revDetail.ParamSchema)
}

// applyTableRuleInstantiate instantiates a new table rule from a rule revision
// and returns the created rule (its last read when --wait polled it) or the
// staged suggestion of a 202.
func applyTableRuleInstantiate(out, promptOut io.Writer, client *dataset_rules.Client, rvClient *rule_versions.Client, token string, doc manifestDoc, orgID, orgName, serverURL string, manifest *dataset_rules.TableRuleApplyManifest, opts tableRuleApplyOptions) (any, error) {
	yes := opts.yes
	resolvedFrom, revDetail, upgradedFromID, err := prepareInstantiateRule(client, rvClient, token, serverURL, doc, manifest)
	if err != nil {
		return nil, err
	}

	// Produced fields resolve against the same revision: an entry may omit
	// field_schema, and the revision's own declaration for that output field
	// is what fills it in. Before the column-mapping resolve, because a
	// declaration and a mapping entry for the same rule field contradict each
	// other and the manifest's own spelling is what that verdict is about.
	if err := dataset_rules.ResolveProducedFields(manifest, revDetail, doc.Source); err != nil {
		return nil, err
	}

	colMapping, err := instantiateColumnMapping(manifest, revDetail, doc)
	if err != nil {
		return nil, err
	}
	bindings := newInstantiateBindings(manifest, revDetail, colMapping)
	if err := bindings.refuseUnbound(doc, manifest.Spec.DatasetID); err != nil {
		return nil, err
	}

	if err := validateInstantiateParams(manifest, revDetail); err != nil {
		return nil, err
	}

	// Before the prompt, so --yes cannot skip it.
	forcedRuleID, err := checkTableRuleDuplicates(promptOut, client, opts.suggestions, token, doc, manifest, revDetail, colMapping, bindings.auto(), upgradedFromID)
	if err != nil {
		return nil, err
	}

	// Build instantiate request
	// instance_name is optional: the API auto-generates a name when omitted.
	var mappingBody *map[string]string
	if len(colMapping) > 0 {
		mappingBody = &colMapping
	}
	req := api.InstantiateRuleJSONRequestBody{
		DatasetID:         manifest.Spec.DatasetID,
		InstanceName:      manifest.Spec.InstanceName,
		RuleColumnMapping: mappingBody,
		Params:            manifest.Spec.Params,
		// Omitted rather than defaulted client-side: a nil severity takes the
		// rule revision's declared default_severity on the server
		// (resolve_rule_severity), which qctl has no local copy of.
		Severity:                  manifest.Spec.Severity,
		Force:                     manifest.Spec.Force,
		ProducedFields:            dataset_rules.ProducedFieldRequests(manifest.Spec.ProducedFields),
		RowFilter:                 row_filters.Saved(manifest.Spec.RowFilter),
		UpgradedFromDatasetRuleID: upgradedFromID,
	}

	// The pre-flight block is the prompt's context, not a result: with --yes
	// there is no question, and every value in it is something the caller just
	// wrote in the manifest.
	if !yes {
		renderInstantiateConfirmation(out, orgID, orgName, doc, manifest, revDetail, bindings, resolvedFrom)

		if !cmdutil.CanPrompt() {
			return nil, qerrors.NewBadUsagef("confirmation required for %s: re-run with --yes (-y) to apply without prompting", doc.Source)
		}
		confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Instantiate this rule onto the table?")
		if err != nil {
			return nil, err
		}
		if !confirmed {
			fmt.Fprintf(out, "Skipping %s\n\n", doc.Source)
			// A decline is a skip, not a success: returning nil would let the
			// runner count an un-applied document in "N succeeded".
			return nil, errDocumentDeclined
		}
		fmt.Fprintf(out, "Instantiating rule %s...\n", manifest.Spec.RuleRevisionID)
	}

	// Instantiate rule
	outcome, err := cmdutil.RetryOnThrottle(out, "dry runs", func() (*dataset_rules.InstantiateOutcome, error) {
		return client.InstantiateRule(token, manifest.Spec.RuleRevisionID, req)
	})
	if err != nil {
		if unresolved, ok := unresolvedBindingsOf(err); ok {
			return nil, fmt.Errorf("rule instantiation failed for %s: %w\n\n%s", doc.Source, err, renderUnresolvedBindings(unresolved))
		}
		return nil, fmt.Errorf("rule instantiation failed for %s: %w", doc.Source, err)
	}

	return finishTableRuleInstantiate(out, promptOut, client, token, outcome, manifest.Spec.DatasetID, colMapping, forcedRuleID, upgradedFromID, opts.wait)
}

// finishTableRuleInstantiate renders the response and waits for a new rule's
// validation. sent is the rule_column_mapping the request carried.
func finishTableRuleInstantiate(out, promptOut io.Writer, client *dataset_rules.Client, token string, outcome *dataset_rules.InstantiateOutcome, datasetID int, sent map[string]string, forcedRuleID, upgradedFromID *uuid.UUID, wait *tableRuleWait) (any, error) {
	if outcome.Staged != nil {
		renderStagedInstantiateOutcome(out, outcome.Staged, datasetID, sent, upgradedFromID)
		writeStagedNothingToWaitFor(promptOut, outcome.Staged, wait)
		return outcome.Staged, nil
	}

	if forcedRuleID != nil && outcome.Created.ID == *forcedRuleID {
		renderForcedUpdateOutcome(out, outcome.Created, sent, upgradedFromID)
		if wait != nil {
			writeNothingToWaitFor(promptOut, "spec.force updated the existing rule in place without a validation dry run")
		}
		return outcome.Created, nil
	}
	renderCreatedOutcome(out, outcome.Created, sent, upgradedFromID)
	return waitForCreatedTableRule(out, promptOut, client, token, outcome.Created, wait)
}

// renderForcedUpdateOutcome prints a forced update's rule, bindings, state and
// readback. sent is the rule_column_mapping the request carried.
func renderForcedUpdateOutcome(out io.Writer, rule *dataset_rules.DatasetRuleDetail, sent map[string]string, upgradedFromID *uuid.UUID) {
	fmt.Fprintf(out, "Table rule updated: %s (%s)\n", terminal.Sanitize(rule.InstanceName), rule.ID)
	renderBindingsLine(out, "  bindings    : ", appliedBindingEntries(sent, rule.FieldBindings, rule.ColumnMappingDict, producedFieldNames(rule.ProducedFields)))
	renderCreatedUpgradeFollowUp(out, upgradedFromID, rule)
	if produced := dataset_rules.ProducedFieldsFromResponse(rule.ProducedFields); len(produced) > 0 {
		fmt.Fprintf(out, "  produced    : %s\n", terminal.Sanitize(dataset_rules.FormatProducedFields(produced)))
	}
	fmt.Fprintf(out, "  state       : %s -- spec.force updated this rule in place without a validation dry run\n", terminal.Sanitize(string(rule.State)))
	fmt.Fprintln(out)
	renderBreadcrumbs(out, []breadcrumb{
		{fmt.Sprintf("qctl get table-rules %d --columns instance_name,state,severity", rule.DatasetID), "where it stands"},
	})
	fmt.Fprintln(out)
}

// renderCreatedOutcome prints a 201: the created rule, its bindings, where it
// stands, and how to read it back. sent is the rule_column_mapping the request
// carried.
func renderCreatedOutcome(out io.Writer, created *dataset_rules.DatasetRuleDetail, sent map[string]string, upgradedFromID *uuid.UUID) {
	fmt.Fprintf(out, "Table rule created: %s (%s)\n", terminal.Sanitize(created.InstanceName), created.ID.String())
	renderBindingsLine(out, "  bindings    : ", appliedBindingEntries(sent, created.FieldBindings, created.ColumnMappingDict, producedFieldNames(created.ProducedFields)))
	renderCreatedUpgradeFollowUp(out, upgradedFromID, created)
	renderCreatedState(out, created, upgradedFromID != nil)
	fmt.Fprintln(out)
}

// renderCreatedUpgradeFollowUp reports the predecessor's state at creation.
func renderCreatedUpgradeFollowUp(out io.Writer, upgradedFromID *uuid.UUID, created *dataset_rules.DatasetRuleDetail) {
	if upgradedFromID == nil {
		return
	}
	switch created.State {
	case api.DatasetRuleStateEnabled:
		fmt.Fprintf(out, "  predecessor : %s retired after the replacement became enabled\n", upgradedFromID)
	case api.DatasetRuleStatePendingValidation:
		fmt.Fprintf(out, "  predecessor : %s remains active until this replacement is enabled\n", upgradedFromID)
	default:
		fmt.Fprintf(out, "  predecessor : %s retirement is deferred; check both rules before disabling either one\n", upgradedFromID)
	}
}

// instantiateColumnMapping is the rule_column_mapping a create sends:
// spec.column expanded, or spec.column_mapping, nil when empty. A key naming
// no field of revDetail is an exit-2 error.
func instantiateColumnMapping(manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull, doc manifestDoc) (map[string]string, error) {
	specMapping := manifest.Spec.ColumnMapping
	if manifest.Spec.Column != "" {
		expanded, err := resolveShorthandColumn(manifest.Spec.Column, manifest.Spec.ColumnMapping, revDetail)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", doc.Source, err)
		}
		specMapping = expanded
	}
	if len(specMapping) == 0 {
		return nil, nil
	}
	if err := validateColumnMappingKeys(specMapping, revDetail); err != nil {
		return nil, fmt.Errorf("%s: %w", doc.Source, err)
	}
	return specMapping, nil
}

// renderBindingsLine prints entries after label, or nothing for no entries.
func renderBindingsLine(out io.Writer, label string, entries []bindingEntry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(out, "%s%s\n", label, terminal.Sanitize(formatBindingEntries(entries, nil)))
}

// renderCreatedState prints a created rule's produced columns, state,
// validation dry run and a note on the rows it has not checked, then the
// follow-up commands. upgrade reports whether the create named a predecessor.
func renderCreatedState(out io.Writer, created *dataset_rules.DatasetRuleDetail, upgrade bool) {
	produced := dataset_rules.ProducedFieldsFromResponse(created.ProducedFields)
	if len(produced) > 0 {
		fmt.Fprintf(out, "  produced    : %s\n", terminal.Sanitize(dataset_rules.FormatProducedFields(produced)))
	}
	fmt.Fprintf(out, "  state       : %s\n", createdStateLine(created, len(produced) > 0))
	awaitsColumns := len(produced) > 0 && created.State == api.DatasetRuleStatePendingValidation
	if awaitsColumns {
		fmt.Fprintln(out, "  next        : passing validation enables the rule if the columns already exist; otherwise apply the draft migration it opens")
	}
	if id := created.ValidationDryRunJobID; id != nil {
		fmt.Fprintf(out, "  dry run     : %d (qctl describe dry-run-job %d)\n", *id, *id)
	}
	fmt.Fprintf(out, "  existing    : %s\n", existingRowsNote(created, upgrade))
	fmt.Fprintln(out)

	breadcrumbs := []breadcrumb{
		{fmt.Sprintf("qctl get table-rules %d --columns instance_name,state,severity", created.DatasetID), "where it stands"},
	}
	if awaitsColumns {
		breadcrumbs = append(breadcrumbs,
			breadcrumb{fmt.Sprintf("qctl get alerts --table-id %d --issue-type needs_review_migration", created.DatasetID), "check for a schema review"},
			breadcrumb{"qctl guide instantiate-rule", "how this resolves"},
		)
	}
	renderBreadcrumbs(out, breadcrumbs)
}

// createdStateLine is the value of a 201's state line: the rule's state and
// validation state, then what that pair means where the pair alone says.
func createdStateLine(created *dataset_rules.DatasetRuleDetail, produces bool) string {
	line := string(created.State)
	var validation api.ValidationEvidenceKind
	if created.ValidationState != nil {
		validation = *created.ValidationState
		line += " (validation: " + string(validation) + ")"
	}
	line = terminal.Sanitize(line)

	switch created.State {
	case api.DatasetRuleStateEnabled:
		switch {
		case produces:
			return line + " -- these columns are already in the table's schema"
		case validation == api.ValidationEvidenceKindNotNeeded:
			return line + " -- active now; this rule needs no validation dry run"
		case validation == api.ValidationEvidenceKindNone:
			return line + " -- active now; no validation dry run has run for it"
		}
		return line + " -- active now"
	case api.DatasetRuleStatePendingValidation:
		switch validation {
		case "", api.ValidationEvidenceKindQueued, api.ValidationEvidenceKindInFlight:
			if produces {
				return line + " -- waiting for validation"
			}
			return line + " -- its validation dry run decides: enabled if it passes, validation_failed if it fails"
		case api.ValidationEvidenceKindNone:
			return line + " -- no validation dry run is queued: the table has no active data source to validate against"
		}
	}
	return line
}

// existingRowsNote is a 201's line on the rows the table already holds.
// upgrade reports whether the create named a predecessor.
func existingRowsNote(created *dataset_rules.DatasetRuleDetail, upgrade bool) string {
	unchecked := "rows already in the table are not checked by a new rule"
	if upgrade {
		switch created.State {
		case api.DatasetRuleStateEnabled:
			unchecked += ", except the quarantined rows the predecessor flagged: retiring it marked them for reprocess"
		case api.DatasetRuleStatePendingValidation:
			unchecked += ", except the quarantined rows the predecessor flagged: retiring it marks them for reprocess when this rule is enabled"
		}
	}
	when := ""
	if created.State != api.DatasetRuleStateEnabled {
		when = "once it is enabled, "
	}
	return fmt.Sprintf("%s; %sqctl rows reprocess-clean %d --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined %d --all re-runs the quarantined rows; both run every enabled rule",
		unchecked, when, created.DatasetID, created.DatasetID)
}

// renderStagedOutcome prints a 202. There are two kinds, and the response
// says which: a revalidation candidate (the request changed the rule's
// revision, mapping or produced fields, so it is validated on a snapshot
// before it reaches the live rule) or a draft-migration staging (params/state
// held for review). Both are successes, exit 0, with resolution breadcrumbs.
//
// Which fields were applied and which were staged is therefore read off the
// answer, not guessed from the request keys -- see
// dataset_rules.SplitPatchOutcome.
func renderStagedOutcome(out io.Writer, patch map[string]interface{}, s *dataset_rules.Suggestion, datasetID int, live ...*dataset_rules.DatasetRuleDetail) {
	isCandidate := rule_suggestions.IsCandidate(s)
	appliedNow, staged := dataset_rules.SplitPatchOutcome(patch, isCandidate, live...)

	if isCandidate {
		renderCandidateOutcome(out, appliedNow, staged, s, datasetID)
		return
	}

	if len(appliedNow) > 0 {
		fmt.Fprintf(out, "tablerule %q partially applied; params/state staged for review\n", s.InstanceName)
		fmt.Fprintf(out, "  applied now : %s\n", strings.Join(appliedNow, ", "))
	} else {
		fmt.Fprintf(out, "tablerule %q staged for review\n", s.InstanceName)
	}
	fmt.Fprintf(out, "  staged      : %s (suggestion %s, state: %s)\n", strings.Join(staged, ", "), s.ID, terminal.Sanitize(string(s.State)))
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  qctl get rule-suggestions --table-id %d            # list pending changes\n", datasetID)
	fmt.Fprintf(out, "  qctl suggestion accept %s --table-id %d   # apply the staged change\n", s.ID, datasetID)
	fmt.Fprintf(out, "  qctl guide manage-suggestions                   # how staging works\n")
}

// renderCandidateOutcome prints the revalidation-candidate 202. The live rule
// is untouched apart from a rename, and `suggestion accept` is refused for a
// candidate -- the backend promotes it itself when its dry run passes -- so
// dismiss is the only lever offered. The staged fields are labelled "queued"
// when the response says the validation is waiting for its dry run to be
// staged, and "validating" otherwise.
func renderCandidateOutcome(out io.Writer, appliedNow, staged []string, s *dataset_rules.Suggestion, datasetID int) {
	fmt.Fprintf(out, "tablerule %q staged as a revalidation candidate\n", s.InstanceName)
	label := "validating"
	if s.ValidationQueued != nil && *s.ValidationQueued {
		label = "queued"
	}
	fmt.Fprintf(out, "  %-11s : %s (suggestion %s, state: %s)\n", label, strings.Join(staged, ", "), s.ID, terminal.Sanitize(string(s.State)))
	if s.ValidationDryRunJobID != nil {
		fmt.Fprintf(out, "  dry run job : %d (qctl describe dry-run-job %d)\n", *s.ValidationDryRunJobID, *s.ValidationDryRunJobID)
	}
	if len(appliedNow) > 0 {
		fmt.Fprintf(out, "  applied now : %s\n", strings.Join(appliedNow, ", "))
	}
	fmt.Fprintf(out, "  live rule   : unchanged until its validation dry run passes\n")
	fmt.Fprintln(out)
	renderBreadcrumbs(out, []breadcrumb{
		{fmt.Sprintf("qctl describe rule-suggestion %s --table-id %d", s.ID, datasetID), "watch it"},
		{fmt.Sprintf("qctl suggestion dismiss %s --table-id %d", s.ID, datasetID), "cancel the repair"},
		{"qctl guide manage-suggestions", "how this resolves"},
	})
}

// breadcrumb is one resolution command and the note explaining it.
type breadcrumb struct{ cmd, note string }

// renderBreadcrumbs prints a block of "  <cmd>   # <note>" lines with the
// comment column computed from the widest command rather than hardcoded.
// Width is computed, not hardcoded: the suggestion id and the dataset id are
// both variable-length, and a skewed comment column reads as a typo.
func renderBreadcrumbs(out io.Writer, breadcrumbs []breadcrumb) {
	width := 0
	for _, b := range breadcrumbs {
		if len(b.cmd) > width {
			width = len(b.cmd)
		}
	}
	for _, b := range breadcrumbs {
		fmt.Fprintf(out, "  %-*s   # %s\n", width, b.cmd, b.note)
	}
}

// renderStagedInstantiate prints the 202 result of an instantiate: the backend
// staged the whole instantiation as a rule suggestion because a draft
// migration is open and this rule would touch the schema. Nothing was created,
// so unlike renderStagedOutcome there is no "applied now" line. Reported as a
// success, with the resolution commands (kubectl-style breadcrumbs).
func renderStagedInstantiate(out io.Writer, s *dataset_rules.InstantiateSuggestion, datasetID int) {
	renderStagedInstantiateOutcome(out, s, datasetID, nil, nil)
}

// renderStagedInstantiateOutcome is renderStagedInstantiate with the bindings
// of the staged create, sent being the rule_column_mapping the request
// carried, and the predecessor's fate.
func renderStagedInstantiateOutcome(out io.Writer, s *dataset_rules.InstantiateSuggestion, datasetID int, sent map[string]string, upgradedFromID *uuid.UUID) {
	fmt.Fprintf(out, "tablerule %q staged for review\n", s.InstanceName)
	fmt.Fprintf(out, "  staged : the whole instantiation (suggestion %s, state: %s)\n", s.ID, terminal.Sanitize(string(s.State)))
	produced := map[string]bool{}
	if s.ProducedFields != nil {
		for _, field := range *s.ProducedFields {
			if name, ok := field["rule_field_name"].(string); ok {
				produced[name] = true
			}
		}
	}
	renderBindingsLine(out, "  bindings : ", appliedBindingEntries(sent, s.FieldBindings, s.RuleColumnMapping, produced))
	if upgradedFromID != nil {
		fmt.Fprintf(out, "  predecessor : %s remains active until the suggestion is accepted and the replacement is enabled\n", upgradedFromID)
	}
	fmt.Fprintln(out)

	renderBreadcrumbs(out, []breadcrumb{
		{fmt.Sprintf("qctl get rule-suggestions --table-id %d", datasetID), "list pending changes"},
		{fmt.Sprintf("qctl suggestion accept %s --table-id %d", s.ID, datasetID), "apply the staged change"},
		{"qctl guide instantiate-rule", "how staging works"},
	})
}

func offerCleanRowRecheck(out io.Writer, client *dataset_rules.Client, token string, before, after *dataset_rules.DatasetRuleDetail, patch map[string]interface{}) {
	if _, set := patch["row_filter"]; !set || after.State != api.DatasetRuleStateEnabled {
		return
	}
	if row_filters.SameRows(row_filters.FromAPI(before.RowFilter), row_filters.FromAPI(after.RowFilter)) {
		return
	}
	count, err := client.CleanRowRecheckCount(token, after.DatasetID, after.ID.String())
	switch {
	case err != nil:
		fmt.Fprintf(out, "Re-check preview unavailable: %s\n", terminal.Sanitize(err.Error()))
	case count.Count == 0 && !count.Capped && count.PredicateErrors == 0:
		return
	default:
		qualifier := ""
		if count.Capped {
			qualifier = "at least "
		}
		fmt.Fprintf(out, "Clean rows eligible for re-check: %s%d", qualifier, count.Count)
		if count.PredicateErrors > 0 {
			fmt.Fprintf(out, " (%d predicate failures excluded)", count.PredicateErrors)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "Next: qctl table-rule recheck-clean-rows %s --table-id %d --dry-run\n", after.ID.String(), after.DatasetID)
}
