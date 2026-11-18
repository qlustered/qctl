package apply

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const ruleDocOutcomeFailed = "failed"

// docResult holds the outcome of processing one YAML document.
type docResult struct {
	name    string // metadata.name or ""
	id      string // metadata.id or ""
	outcome string // "patched" | "unchanged" | "failed"
	err     error
	// revision is the revision as read after the document: the live read
	// when unchanged, and when patched the re-read processOneRuleDoc makes
	// only when asked to. nil otherwise.
	revision *rule_versions.RuleRevisionFull
}

// immutableSpecFields lists spec fields that cannot be changed via apply.
// Changing these requires submitting a new revision via `qctl submit rules`.
var immutableSpecFields = []string{
	"release",
	"code",
	"description",
	"default_severity",
	"input_columns",
	"optional_columns",
	"validates_columns",
	"corrects_columns",
	"enriches_columns",
	"affected_columns",
	"affected_column_types",
	"param_schema",
	"is_builtin",
	"is_caf",
}

// serverComputedSpecFields are included in get manifests but ignored by apply.
var serverComputedSpecFields = []string{
	"params_renderability",
	"params_renderable_in_basic_ui",
	"renderability_ruleset_version",
}

// applyRuleYAML applies one kind: Rule document. Multi-document handling,
// fail-fast and the failure count belong to applyStream, which calls this once
// per document.
func applyRuleYAML(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(doc.Data, &parsed); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse %s", doc.Source))
	}
	name, id, err := ruleDocIdentity(parsed)
	if err != nil {
		return nil, err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}

	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	r := processOneRuleDoc(client, ctx.Credential.AccessToken, name, id, parsed, documentOutput(cmd))
	name = r.name
	if name == "" {
		name = "unknown"
	}
	label := "rule/" + name
	if r.id != "" {
		label = fmt.Sprintf("rule/%s (%s)", name, rule_versions.ShortID(r.id))
	}

	out := humanOut(cmd)
	switch r.outcome {
	case "patched":
		fmt.Fprintf(out, "%s patched\n", label)
	case "unchanged": //nolint:goconst // Keep the outcome strings beside the switch that prints them.
		fmt.Fprintf(out, "%s unchanged\n", label)
	case ruleDocOutcomeFailed:
		return nil, r.err
	}
	return namedApplyResult(r.revision, "rule", r.id), r.err
}

// processOneRuleDoc diffs and patches a single rule document whose identity
// ruleDocIdentity returned as name and id. With reread set, a patched revision
// is read again into the result's revision; a failed re-read is the result's
// err beside the "patched" outcome.
func processOneRuleDoc(client *rule_versions.Client, token, name, id string, doc map[string]interface{}, reread bool) docResult {
	result := docResult{name: name, id: id}

	// 1. Fetch live state
	live, err := client.GetRuleRevisionDetails(token, id)
	if err != nil {
		result.outcome = ruleDocOutcomeFailed
		result.err = fmt.Errorf("failed to fetch live state: %w", err)
		return result
	}
	result.id = live.ID.String()

	// Fill in name from live if not set in manifest
	if result.name == "" {
		result.name = live.Name
	}

	// 2. Extract user's spec and status maps
	specMap, _ := doc["spec"].(map[string]interface{})
	statusMap, _ := doc["status"].(map[string]interface{})

	// 3-4. Reject changes to immutable metadata and spec fields.
	if err := validateImmutableRuleFields(name, specMap, live); err != nil {
		result.outcome = ruleDocOutcomeFailed
		result.err = err
		return result
	}

	// 5. Build smart patch — only changed patchable fields
	patchReq, changed := buildRulePatch(specMap, statusMap, live)

	// 6. No changes → unchanged
	if !changed {
		result.outcome = "unchanged"
		result.revision = live
		return result
	}

	// 7. Patch
	_, err = client.PatchRuleRevision(token, id, patchReq)
	if err != nil {
		result.outcome = ruleDocOutcomeFailed
		result.err = fmt.Errorf("patch failed: %w", err)
		return result
	}

	result.outcome = "patched"
	if reread {
		// The PATCH answers only {result}, so the revision it changed is read
		// back for the document.
		if result.revision, err = client.GetRuleRevisionDetails(token, id); err != nil {
			result.err = fmt.Errorf("patched, but reading the revision back failed: %w", err)
		}
	}
	return result
}

func ruleDocIdentity(doc map[string]interface{}) (string, string, error) {
	apiVersion, _ := doc["apiVersion"].(string)
	if apiVersion != "qluster.ai/v1" {
		return "", "", qerrors.NewBadUsagef("unsupported apiVersion %q (expected qluster.ai/v1)", apiVersion)
	}
	kind, _ := doc["kind"].(string)
	if kind != "Rule" {
		return "", "", qerrors.NewBadUsagef("unsupported kind %q (expected Rule)", kind)
	}
	metadata, _ := doc["metadata"].(map[string]interface{})
	if metadata == nil {
		return "", "", qerrors.NewBadUsage("metadata section is required")
	}
	id, _ := metadata["id"].(string)
	if id == "" {
		return "", "", qerrors.NewBadUsage("metadata.id is required")
	}
	name, _ := metadata["name"].(string)
	return name, id, nil
}

func validateImmutableRuleFields(name string, specMap map[string]interface{}, live *rule_versions.RuleRevisionFull) error {
	if name != "" && name != live.Name {
		//nolint:revive,staticcheck // Preserve intentional capitalization and punctuation in the second guidance line.
		return qerrors.NewBadUsage("immutable fields changed: metadata.name\nImmutable changes require submitting a new revision via `qctl submit rules`.")
	}
	var changed []string
	for _, field := range immutableSpecFields {
		userVal, present := specMap[field]
		if present && !fieldEquals(userVal, getLiveFieldValue(field, live)) {
			changed = append(changed, "spec."+field)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	sort.Strings(changed)
	//nolint:revive,staticcheck // Preserve intentional capitalization and punctuation in the second guidance line.
	return qerrors.NewBadUsagef("immutable fields changed: %s\nImmutable changes require submitting a new revision via `qctl submit rules`.", strings.Join(changed, ", "))
}

func buildRulePatch(specMap, statusMap map[string]interface{}, live *rule_versions.RuleRevisionFull) (api.PatchRuleRevisionJSONRequestBody, bool) {
	patchReq := api.PatchRuleRevisionJSONRequestBody{}
	changed := false
	if userState := getFieldFromMaps("state", specMap, statusMap); userState != nil && !fieldEquals(userState, getLiveFieldValue("state", live)) {
		s, _ := userState.(string)
		state := api.RuleState(s)
		patchReq.State = &state
		changed = true
	}
	if userIsDefault := getFieldFromMaps("is_default", specMap, statusMap); userIsDefault != nil && !fieldEquals(userIsDefault, getLiveFieldValue("is_default", live)) {
		b, _ := userIsDefault.(bool)
		patchReq.IsDefault = &b
		changed = true
	}
	return patchReq, changed
}

// getFieldFromMaps returns the value of a field from the first map that contains it.
func getFieldFromMaps(field string, maps ...map[string]interface{}) interface{} {
	for _, m := range maps {
		if m == nil {
			continue
		}
		if v, ok := m[field]; ok {
			return v
		}
	}
	return nil
}

// fieldEquals compares two values by JSON-normalizing them.
// This handles type coercion (e.g., string vs api.RuleState, []interface{} vs []string).
func fieldEquals(a, b interface{}) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

// getLiveFieldValue maps a field name to its value from the live revision.
func getLiveFieldValue(fieldName string, live *rule_versions.RuleRevisionFull) interface{} {
	switch fieldName {
	case "state":
		return string(live.State)
	case "is_default":
		return live.IsDefault
	case "release":
		return live.Release
	case "code":
		if live.Code != nil {
			return *live.Code
		}
		return ""
	case "is_builtin":
		return live.IsBuiltin
	case "is_caf":
		return live.IsCaf
	default:
		return getLiveReadOnlyFieldValue(fieldName, live)
	}
}

func getLiveReadOnlyFieldValue(fieldName string, live *rule_versions.RuleRevisionFull) interface{} {
	switch fieldName {
	case "description":
		if live.Description != nil {
			return *live.Description
		}
		return ""
	case "default_severity":
		if live.DefaultSeverity != nil {
			return string(*live.DefaultSeverity)
		}
		return nil
	default:
		return getLiveCollectionFieldValue(fieldName, live)
	}
}

func getLiveCollectionFieldValue(fieldName string, live *rule_versions.RuleRevisionFull) interface{} {
	switch fieldName {
	case "input_columns":
		return live.InputColumns
	case "optional_columns":
		if live.OptionalColumns != nil {
			return *live.OptionalColumns
		}
		return nil
	case "validates_columns":
		return live.ValidatesColumns
	case "corrects_columns":
		return live.CorrectsColumns
	case "enriches_columns":
		return live.EnrichesColumns
	case "affected_columns":
		return live.AffectedColumns
	case "affected_column_types":
		return rule_versions.AffectedColumnTypes(live)
	case "param_schema":
		return live.ParamSchema
	default:
		return nil
	}
}
