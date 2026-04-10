package apply

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// docResult holds the outcome of processing one YAML document.
type docResult struct {
	name    string // metadata.name or ""
	id      string // metadata.id or ""
	outcome string // "patched" | "unchanged" | "failed"
	err     error
}

// immutableSpecFields lists spec fields that cannot be changed via apply.
// Changing these requires submitting a new revision via `qctl submit rules`.
var immutableSpecFields = []string{
	"release",
	"code",
	"input_columns",
	"validates_columns",
	"corrects_columns",
	"enriches_columns",
	"affected_columns",
	"param_schema",
	"is_builtin",
	"is_caf",
}

// applyRuleYAML is the entry point for the generic "apply -f" dispatcher.
func applyRuleYAML(cmd *cobra.Command, filePath string) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	docs, err := parseRuleDocuments(data)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", filePath, err)
	}

	failFast, _ := cmd.Flags().GetBool("fail-fast")
	token := ctx.Credential.AccessToken
	out := cmd.OutOrStdout()

	var results []docResult
	for _, doc := range docs {
		r := processOneRuleDoc(client, token, doc)
		results = append(results, r)

		label := formatLabel(r)
		switch r.outcome {
		case "patched":
			fmt.Fprintf(out, "%s patched\n", label)
		case "unchanged":
			fmt.Fprintf(out, "%s unchanged\n", label)
		case "failed":
			fmt.Fprintf(out, "%s failed: %s\n", label, r.err.Error())
		}

		if failFast && r.outcome == "failed" {
			// Mark remaining docs as skipped
			break
		}
	}

	// Count failures
	failCount := 0
	for _, r := range results {
		if r.outcome == "failed" {
			failCount++
		}
	}
	if failCount > 0 {
		return fmt.Errorf("%d of %d documents failed", failCount, len(docs))
	}
	return nil
}

// formatLabel builds the "rule/<name> (<short-id>)" display label.
func formatLabel(r docResult) string {
	name := r.name
	if name == "" {
		name = "unknown"
	}
	if r.id != "" {
		return fmt.Sprintf("rule/%s (%s)", name, rule_versions.ShortID(r.id))
	}
	return fmt.Sprintf("rule/%s", name)
}

// parseRuleDocuments splits a multi-document YAML byte slice into individual
// documents, each decoded as map[string]interface{}.
func parseRuleDocuments(data []byte) ([]map[string]interface{}, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var docs []map[string]interface{}
	for {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no documents found")
	}
	return docs, nil
}

// processOneRuleDoc validates, diffs, and patches a single rule document.
func processOneRuleDoc(client *rule_versions.Client, token string, doc map[string]interface{}) docResult {
	// 1. Validate envelope
	apiVersion, _ := doc["apiVersion"].(string)
	if apiVersion != "qluster.ai/v1" {
		return docResult{outcome: "failed", err: fmt.Errorf("unsupported apiVersion %q (expected qluster.ai/v1)", apiVersion)}
	}

	kind, _ := doc["kind"].(string)
	if kind != "Rule" {
		return docResult{outcome: "failed", err: fmt.Errorf("unsupported kind %q (expected Rule)", kind)}
	}

	metadata, _ := doc["metadata"].(map[string]interface{})
	if metadata == nil {
		return docResult{outcome: "failed", err: fmt.Errorf("metadata section is required")}
	}

	id, _ := metadata["id"].(string)
	if id == "" {
		return docResult{outcome: "failed", err: fmt.Errorf("metadata.id is required")}
	}

	name, _ := metadata["name"].(string)

	result := docResult{name: name, id: id}

	// 2. Fetch live state
	live, err := client.GetRuleRevisionDetails(token, id)
	if err != nil {
		result.outcome = "failed"
		result.err = fmt.Errorf("failed to fetch live state: %w", err)
		return result
	}

	// Fill in name from live if not set in manifest
	if result.name == "" {
		result.name = live.Name
	}

	// 3. Extract user's spec and status maps
	specMap, _ := doc["spec"].(map[string]interface{})
	statusMap, _ := doc["status"].(map[string]interface{})

	// 4. Check metadata.name immutability
	if name != "" && name != live.Name {
		result.outcome = "failed"
		result.err = fmt.Errorf("immutable fields changed: metadata.name\nImmutable changes require submitting a new revision via `qctl submit rules`.")
		return result
	}

	// 5. Detect immutable spec field changes
	var immutableChanged []string
	if specMap != nil {
		for _, field := range immutableSpecFields {
			userVal, present := specMap[field]
			if !present {
				continue
			}
			liveVal := getLiveFieldValue(field, live)
			if !fieldEquals(userVal, liveVal) {
				immutableChanged = append(immutableChanged, "spec."+field)
			}
		}
	}
	if len(immutableChanged) > 0 {
		sort.Strings(immutableChanged)
		result.outcome = "failed"
		result.err = fmt.Errorf("immutable fields changed: %s\nImmutable changes require submitting a new revision via `qctl submit rules`.",
			strings.Join(immutableChanged, ", "))
		return result
	}

	// 6. Build smart patch — only changed patchable fields
	patchReq := api.PatchRuleRevisionJSONRequestBody{}
	changed := false

	// state: prefer spec, fall back to status
	if userState := getFieldFromMaps("state", specMap, statusMap); userState != nil {
		liveState := getLiveFieldValue("state", live)
		if !fieldEquals(userState, liveState) {
			s, _ := userState.(string)
			state := api.RuleState(s)
			patchReq.State = &state
			changed = true
		}
	}

	// is_default: prefer spec, fall back to status
	if userIsDefault := getFieldFromMaps("is_default", specMap, statusMap); userIsDefault != nil {
		liveIsDefault := getLiveFieldValue("is_default", live)
		if !fieldEquals(userIsDefault, liveIsDefault) {
			b, _ := userIsDefault.(bool)
			patchReq.IsDefault = &b
			changed = true
		}
	}

	// 7. No changes → unchanged
	if !changed {
		result.outcome = "unchanged"
		return result
	}

	// 8. Patch
	_, err = client.PatchRuleRevision(token, id, patchReq)
	if err != nil {
		result.outcome = "failed"
		result.err = fmt.Errorf("patch failed: %w", err)
		return result
	}

	result.outcome = "patched"
	return result
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
	case "description":
		if live.Description != nil {
			return *live.Description
		}
		return ""
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
	case "input_columns":
		return live.InputColumns
	case "validates_columns":
		return live.ValidatesColumns
	case "corrects_columns":
		return live.CorrectsColumns
	case "enriches_columns":
		return live.EnrichesColumns
	case "affected_columns":
		return live.AffectedColumns
	case "param_schema":
		return live.ParamSchema
	default:
		return nil
	}
}
