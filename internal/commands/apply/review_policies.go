package apply

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	actionCreated = "created"
	actionUpdated = "updated"

	scopeOrganization = "organization"
)

// applyRuleReviewPolicyYAML handles kind: RuleReviewPolicy for the generic dispatcher.
//
// The backend PUT is an idempotent upsert that replaces omitted optional fields
// with backend defaults: the manifest is the complete desired state for its scope.
// The pre-apply GET only decides the created/updated label — a policy exists at
// this scope iff the effective policy's source matches the scope (an inherited
// org policy does not count as existing at dataset scope).
func applyRuleReviewPolicyYAML(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := parseRuleReviewPolicyManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, formatPolicyValidationErrors(errs)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	body, err := m.ToRequestBody()
	if err != nil {
		return nil, err
	}

	var applied *review_policies.RulePolicy
	action := actionCreated
	scopeLabel := scopeOrganization
	if m.Spec.DatasetID > 0 {
		scopeLabel = fmt.Sprintf("table %d", m.Spec.DatasetID)
		eff, err := client.GetDatasetRulePolicy(token, m.Spec.DatasetID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch current rule review policy: %w", err)
		}
		if eff != nil && eff.Source == review_policies.SourceDataset {
			action = actionUpdated
		}
		if applied, err = client.PutDatasetRulePolicy(token, m.Spec.DatasetID, body); err != nil {
			return nil, fmt.Errorf("failed to apply rule review policy: %w", err)
		}
	} else {
		eff, err := client.GetOrgRulePolicy(token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch current rule review policy: %w", err)
		}
		if eff != nil && eff.Source == review_policies.SourceOrganization {
			action = actionUpdated
		}
		if applied, err = client.PutOrgRulePolicy(token, body); err != nil {
			return nil, fmt.Errorf("failed to apply rule review policy: %w", err)
		}
	}

	fmt.Fprintf(humanOut(cmd), "rule-review-policy/%s %s (%s)\n",
		terminal.Sanitize(policyDisplayName(m.Metadata.Name, m.Metadata.ID)), action, scopeLabel)
	return namedApplyResult(applied, "rule-review-policy", applied.ID), nil
}

func parseRuleReviewPolicyManifest(data []byte) (*review_policies.RuleReviewPolicyManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "RuleReviewPolicy" {
		return nil, qerrors.NewBadUsagef("expected kind 'RuleReviewPolicy', got '%s'", generic.Kind)
	}
	var m review_policies.RuleReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// applyDataReviewPolicyYAML handles kind: DataReviewPolicy for the generic dispatcher.
// Same semantics as applyRuleReviewPolicyYAML; the data-policy GET returns the
// raw policy object (with source and nullable id) instead of an effective policy.
func applyDataReviewPolicyYAML(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := parseDataReviewPolicyManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, formatPolicyValidationErrors(errs)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	body, err := m.ToRequestBody()
	if err != nil {
		return nil, err
	}

	var applied *review_policies.DataPolicy
	action := actionCreated
	scopeLabel := scopeOrganization
	if m.Spec.DatasetID > 0 {
		scopeLabel = fmt.Sprintf("table %d", m.Spec.DatasetID)
		current, err := client.GetDatasetDataPolicy(token, m.Spec.DatasetID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch current data review policy: %w", err)
		}
		if dataPolicyExistsAtScope(current, review_policies.SourceDataset) {
			action = actionUpdated
		}
		if applied, err = client.PutDatasetDataPolicy(token, m.Spec.DatasetID, body); err != nil {
			return nil, fmt.Errorf("failed to apply data review policy: %w", err)
		}
	} else {
		current, err := client.GetOrgDataPolicy(token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch current data review policy: %w", err)
		}
		if dataPolicyExistsAtScope(current, review_policies.SourceOrganization) {
			action = actionUpdated
		}
		if applied, err = client.PutOrgDataPolicy(token, body); err != nil {
			return nil, fmt.Errorf("failed to apply data review policy: %w", err)
		}
	}

	fmt.Fprintf(humanOut(cmd), "data-review-policy/%s %s (%s)\n",
		terminal.Sanitize(policyDisplayName(m.Metadata.Name, m.Metadata.ID)), action, scopeLabel)
	return namedApplyResult(applied, "data-review-policy", applied.ID), nil
}

// dataPolicyExistsAtScope reports whether an explicit policy object exists at
// the given scope: it must have an id and its source must match the scope.
func dataPolicyExistsAtScope(p *review_policies.DataPolicy, scope string) bool {
	return p != nil && p.ID != nil && p.Source != nil && *p.Source == scope
}

func parseDataReviewPolicyManifest(data []byte) (*review_policies.DataReviewPolicyManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "DataReviewPolicy" {
		return nil, qerrors.NewBadUsagef("expected kind 'DataReviewPolicy', got '%s'", generic.Kind)
	}
	var m review_policies.DataReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func formatPolicyValidationErrors(errs []error) error {
	var messages []string
	for _, e := range errs {
		messages = append(messages, e.Error())
	}
	return qerrors.NewBadUsagef("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
}

// policyDisplayName prefers metadata.name, falling back to metadata.id
// (the universal manifest wrapper requires at least one of them).
func policyDisplayName(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

// applyChangeReviewPolicyYAML handles kind: ChangeReviewPolicy for the generic
// dispatcher. The PUT replaces the whole policy at the manifest's scope; the
// pre-apply GET of the stored policy at that scope only decides the
// created/updated label.
func applyChangeReviewPolicyYAML(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := parseChangeReviewPolicyManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, formatPolicyValidationErrors(errs)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	body := m.ToRequestBody()

	var current, applied *review_policies.ChangePolicy
	scopeLabel := scopeOrganization
	if m.Spec.DatasetID > 0 {
		scopeLabel = fmt.Sprintf("table %d", m.Spec.DatasetID)
		if current, err = client.GetDatasetChangePolicy(token, m.Spec.DatasetID); err != nil {
			return nil, fmt.Errorf("failed to fetch current change review policy: %w", err)
		}
		if applied, err = client.PutDatasetChangePolicy(token, m.Spec.DatasetID, body); err != nil {
			return nil, fmt.Errorf("failed to apply change review policy: %w", err)
		}
	} else {
		if current, err = client.GetOrgChangePolicy(token); err != nil {
			return nil, fmt.Errorf("failed to fetch current change review policy: %w", err)
		}
		if applied, err = client.PutOrgChangePolicy(token, body); err != nil {
			return nil, fmt.Errorf("failed to apply change review policy: %w", err)
		}
	}
	action := actionCreated
	if current != nil {
		action = actionUpdated
	}

	fmt.Fprintf(humanOut(cmd), "change-review-policy/%s %s (%s)\n",
		terminal.Sanitize(policyDisplayName(m.Metadata.Name, m.Metadata.ID)), action, scopeLabel)
	return namedApplyResult(applied, "change-review-policy", applied.ID), nil
}

func parseChangeReviewPolicyManifest(data []byte) (*review_policies.ChangeReviewPolicyManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != review_policies.ChangeReviewPolicyKind {
		return nil, qerrors.NewBadUsagef("expected kind '%s', got '%s'", review_policies.ChangeReviewPolicyKind, generic.Kind)
	}
	var m review_policies.ChangeReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
