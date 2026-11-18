package copycmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
)

// cloudSourceCopyDryRun is the document --dry-run writes under -o json|yaml.
type cloudSourceCopyDryRun struct {
	Template json.RawMessage `json:"template"`
	Request  json.RawMessage `json:"request"`
}

type cloudSourceCopyPlan struct {
	source    *cloud_sources.CloudSourceTiny
	upload    *cloud_sources.CloudSourceTiny
	flags     copyCloudSourceFlags
	tableID   int
	tableName string
	// template holds the members of safeTemplate, and request those of body.
	template map[string]json.RawMessage
	request  map[string]json.RawMessage
	// safeTemplate is the copy template without its credential fields.
	safeTemplate json.RawMessage
	// body is the request the copy sends.
	body     []byte
	cursor   *int
	resupply []string
	// accessKey reports whether the template holds a nonempty s3_access_key.
	accessKey bool
}

func prepareCloudSourceCopyPlan(client *cloud_sources.Client, token string, source *cloud_sources.CloudSourceTiny,
	template *api.DataSourceModelCopyTemplateSchema, rawTemplate []byte, tableID int, flags copyCloudSourceFlags,
) (*cloudSourceCopyPlan, error) {
	body, err := cloud_sources.BuildCopyBody(rawTemplate, flags.overrides)
	if err != nil {
		return nil, err
	}
	plan, err := buildCloudSourceCopyPlan(source, template, rawTemplate, body, tableID, flags)
	if err != nil {
		return nil, err
	}
	// The server disables no upload source for a copy that joins a table series.
	_, series := plan.request["table_series_id"]
	if flags.overrides.State == api.DataSourceStateActive && !series {
		sources, _, err := client.GetAllCloudSources(token, cloud_sources.GetCloudSourcesParams{
			DatasetID: &tableID, States: []string{string(api.DataSourceStateActive)},
		}, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to plan cloud source copy: %w", err)
		}
		plan.upload = soleActiveUpload(sources, tableID)
	}
	return plan, nil
}

// buildCloudSourceCopyPlan returns the plan of the copy that sends body. The
// target table's name, the secrets to resupply and whether an access key is
// held come from template; the old field values come from rawTemplate, the
// JSON template was decoded from.
func buildCloudSourceCopyPlan(source *cloud_sources.CloudSourceTiny, template *api.DataSourceModelCopyTemplateSchema,
	rawTemplate, body []byte, tableID int, flags copyCloudSourceFlags,
) (*cloudSourceCopyPlan, error) {
	safeTemplate, err := cloud_sources.RedactCopyTemplate(rawTemplate)
	if err != nil {
		return nil, err
	}
	plan := &cloudSourceCopyPlan{
		source: source, flags: flags, tableID: tableID, tableName: template.DatasetName,
		safeTemplate: safeTemplate, body: body, cursor: source.DeltaAppliedCursor,
		resupply: template.SecretsToResupply, accessKey: template.S3AccessKey != "",
	}
	if err := json.Unmarshal(safeTemplate, &plan.template); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &plan.request); err != nil {
		return nil, err
	}
	return plan, nil
}

// cloudSourceCopyDryRunDocument returns the --dry-run document: the redacted
// template, and the request with each credential member other than ""
// replaced by dryRunSecretMask.
func cloudSourceCopyDryRunDocument(plan *cloudSourceCopyPlan) (cloudSourceCopyDryRun, error) {
	mask, err := json.Marshal(dryRunSecretMask)
	if err != nil {
		return cloudSourceCopyDryRun{}, err
	}
	members := maps.Clone(plan.request)
	for _, key := range secretsFileKeys {
		if value, ok := members[key]; ok && string(value) != `""` {
			members[key] = mask
		}
	}
	request, err := json.Marshal(members)
	if err != nil {
		return cloudSourceCopyDryRun{}, fmt.Errorf("failed to encode cloud source copy dry run: %w", err)
	}
	return cloudSourceCopyDryRun{Template: plan.safeTemplate, Request: request}, nil
}

func soleActiveUpload(sources []cloud_sources.CloudSourceTiny, tableID int) *cloud_sources.CloudSourceTiny {
	var upload *cloud_sources.CloudSourceTiny
	for i := range sources {
		source := &sources[i]
		if source.DatasetID != tableID || source.State != api.DataSourceStateActive || source.DataSourceType != api.DataSourceTypeQlusterUI {
			continue
		}
		if upload != nil {
			return nil
		}
		upload = source
	}
	return upload
}

func writeCloudSourceCopyPlan(w io.Writer, plan *cloudSourceCopyPlan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Copy cloud source %s (%d) into a new source:\n", planCell(plan.source.Name), plan.source.ID)
	target := strconv.Itoa(plan.tableID)
	if plan.tableName != "" {
		target = fmt.Sprintf("%s (%d)", planCell(plan.tableName), plan.tableID)
	}
	rows := [][]string{
		{"Target table:", target},
		{"Name:", cloudSourcePlanValue(plan.request["name"]), valueOrigin(plan.flags.overrides.Name != nil, nameFlag, "suggested")},
		{"State:", cloudSourcePlanValue(plan.request["state"]), valueOrigin(plan.flags.stateGiven, stateFlag, "source's")},
	}
	writePlanTable(&b, rows)
	writeCloudSourceCopyCredentials(&b, plan)
	writeCloudSourceCopyChanges(&b, plan)
	writeCloudSourceCopyEffects(&b, plan)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeCloudSourceCopyCredentials writes that the copy inherits no secret;
// then, when there are any, the credentials --secrets-file supplies, where the
// copy's s3_access_key comes from, and the secrets the copy lacks with what a
// copy in its state does without them; and for a draft that lacks any, the
// commands that activate it later. It writes no value.
func writeCloudSourceCopyCredentials(b *strings.Builder, plan *cloudSourceCopyPlan) {
	b.WriteString("Credentials: secrets and URLs are not inherited; values are not shown.\n")
	parts := suppliedCredentialParts(plan)
	state := string(plan.flags.overrides.State)
	missing := missingCopySecrets(plan.resupply, plan.flags.overrides.Secrets)
	if len(missing) > 0 {
		parts = append(parts, "missing "+planNames(missing))
		switch pronoun := secretsPronoun(missing); {
		case state == string(api.DataSourceStateDraft):
			parts = append(parts, copyInState(state)+" saves without "+pronoun)
		case needsEverySecret(state):
			parts = append(parts, copyInState(state)+" is refused without "+pronoun)
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  %s\n", strings.Join(parts, "; "))
	}
	if len(missing) > 0 && state == string(api.DataSourceStateDraft) {
		fmt.Fprintf(b, "  To activate it later: %s\n", activateSteps("<id>", missing))
	}
}

// suppliedCredentialParts names the credentials --secrets-file supplies to the
// copy, sorted, and where the copy's s3_access_key comes from: kept from the
// source, or replaced from the file. A supplied s3_access_key is named as
// supplied only when the template holds none to replace.
func suppliedCredentialParts(plan *cloudSourceCopyPlan) []string {
	secrets := plan.flags.overrides.Secrets
	supplied := slices.Sorted(maps.Keys(secrets))
	var accessKey string
	switch _, given := secrets[s3AccessKeyKey]; {
	case given && plan.accessKey:
		supplied = slices.DeleteFunc(supplied, func(key string) bool { return key == s3AccessKeyKey })
		accessKey = s3AccessKeyKey + " replaced from --" + secretsFileFlag
	case plan.accessKey:
		accessKey = s3AccessKeyKey + " kept from the source"
	}
	var parts []string
	if len(supplied) > 0 {
		parts = append(parts, "supplied "+planNames(supplied))
	}
	if accessKey != "" {
		parts = append(parts, accessKey)
	}
	return parts
}

func writeCloudSourceCopyChanges(b *strings.Builder, plan *cloudSourceCopyPlan) {
	if len(plan.flags.overrides.Fields) == 0 {
		return
	}
	keys := make([]string, 0, len(plan.flags.overrides.Fields))
	for key := range plan.flags.overrides.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b.WriteString("\nField changes (--set):\n")
	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{key + ":", cloudSourcePlanValue(plan.template[key]), "→", cloudSourcePlanValue(plan.request[key])})
	}
	writePlanTable(b, rows)
}

func writeCloudSourceCopyEffects(b *strings.Builder, plan *cloudSourceCopyPlan) {
	var effects []string
	if plan.upload != nil {
		effects = append(effects, fmt.Sprintf("disables upload source %s (%d)", planCell(plan.upload.Name), plan.upload.ID))
	}
	var schedule string
	_ = json.Unmarshal(plan.request["schedule"], &schedule)
	if plan.flags.overrides.State == api.DataSourceStateActive && schedule != "" {
		effects = append(effects, "starts polling on schedule "+cloudSourcePlanValue(plan.request["schedule"]))
	}
	if plan.cursor != nil {
		effects = append(effects, "starts with no applied delta cursor")
	}
	if len(effects) > 0 {
		b.WriteString("\nOn success:\n")
		for _, effect := range effects {
			fmt.Fprintf(b, "  - %s\n", effect)
		}
	}
}

func cloudSourcePlanValue(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return unsetValue
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return planValue(&s)
	}
	return planCell(string(raw))
}
