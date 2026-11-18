package diff

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// loadPatchManifest reads and strict-checks the one document a diff operates
// on: parseable YAML, the apiVersion this CLI speaks, patch mode, and then
// every manifest-only verdict `qctl apply -f` passes the same manifest through
// before its request (dataset_rules.ReconcileInstanceName and
// dataset_rules.ValidatePatchManifest). Each refusal is
// exit 2 and is reached before auth because none needs the server to decide;
// the revision-dependent checks follow after bootstrap.
func loadPatchManifest(filePath string) (dataset_rules.TableRuleApplyManifest, error) {
	var manifest dataset_rules.TableRuleApplyManifest
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return manifest, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	if err := yaml.Unmarshal(fileBytes, &manifest); err != nil {
		return manifest, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", filePath))
	}
	if err := dataset_rules.ValidateManifestAPIVersion(manifest.APIVersion, filePath); err != nil {
		return manifest, err
	}
	if !manifest.IsPatch() {
		return manifest, qerrors.NewBadUsagef("diff is not yet supported for TableRule/instantiate (patch mode only: set metadata.id): %s", filePath)
	}
	if err := dataset_rules.ReconcileInstanceName(&manifest, filePath); err != nil {
		return manifest, err
	}
	if err := dataset_rules.ValidatePatchManifest(&manifest, filePath); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// prepareDiffRevisionFields runs the same revision-dependent preparation and
// reconciliation with the live rule apply runs, in the same order, so the
// previewed body is the body apply would send.
//
// The revision is the one apply would judge against -- the rebind target when
// the manifest rebinds, else the live rule's -- because both param validation
// and an omitted produced-field schema depend on it. A preview judged against
// a different revision could reject a valid request or bless an invalid one.
//
// The reconcile then runs unconditionally, unlike the resolve: `produced_fields:
// []` needs no revision to resolve against but is exactly the value the
// reconcile has to judge, since clearing a rule's declarations is the change the
// backend accepts and then refuses to promote.
func prepareDiffRevisionFields(token string, rvClient *rule_versions.Client, manifest *dataset_rules.TableRuleApplyManifest, detail *dataset_rules.DatasetRuleDetail, filePath string) error {
	var revDetail *rule_versions.RuleRevisionFull
	if dataset_rules.NeedsRuleRevision(manifest) {
		revID := dataset_rules.PatchRevisionID(manifest, detail)
		var err error
		revDetail, err = rvClient.GetRuleRevisionDetails(token, revID)
		if err != nil {
			return fmt.Errorf("failed to fetch rule revision %s: %w", revID, err)
		}
	}
	if err := dataset_rules.PreparePatchRevisionFields(manifest, revDetail, filePath); err != nil {
		return err
	}
	_, err := dataset_rules.ReconcilePatchWithLive(manifest, detail, filePath)
	return err
}

// diffTableRule previews a TableRule patch: one "field : old -> new" line
// per changed key the manifest sets, then the exact merge-patch body apply
// would send. Returns ErrDiffDifferences (silent exit 1) when differences
// exist, nil (exit 0, no output) when there are none.
func diffTableRule(cmd *cobra.Command, filePath string) error {
	manifest, err := loadPatchManifest(filePath)
	if err != nil {
		return err
	}

	// Auth comes before the patch body is built because a named rebind target
	// must be resolved against the rule-revision catalog and revision-dependent
	// fields need the governing revision's schemas -- exactly as in apply.
	// Building the patch map before those steps would let the preview disagree
	// with apply about both the supported fields and their values.
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create dataset rules client: %w", err)
	}
	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create rule versions client: %w", err)
	}
	if _, err := dataset_rules.ResolvePatchRebind(rvClient, ctx.Credential.AccessToken, &manifest, filePath); err != nil {
		return err
	}

	// A manifest that sets nothing patchable is a verdict on the manifest and
	// is reached before any request. It does not speak for the body after the
	// produced-field reconcile below, which may drop a key it emitted here --
	// that emptiness means "no differences", which is this command's exit 0.
	if len(dataset_rules.BuildPatchMap(&manifest)) == 0 {
		return dataset_rules.NoPatchableFieldsError(filePath)
	}
	detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, manifest.Metadata.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch table rule %s: %w", manifest.Metadata.ID, err)
	}

	if err := prepareDiffRevisionFields(ctx.Credential.AccessToken, rvClient, &manifest, detail, filePath); err != nil {
		return err
	}

	patch := dataset_rules.BuildPatchMap(&manifest)

	var changed []dataset_rules.FieldChange
	for _, c := range dataset_rules.PatchFieldChanges(detail, patch) {
		if c.Changed {
			changed = append(changed, c)
		}
	}
	if len(changed) == 0 {
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprint(out, dataset_rules.FormatFieldChanges(changed, ""))
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to encode patch body: %w", err)
	}
	// See the note in diff/table.go: json.Marshal leaves C1 code points raw.
	fmt.Fprintf(out, "\nmerge patch body:\n%s\n", terminal.Sanitize(string(body)))
	return qerrors.ErrDiffDifferences
}
