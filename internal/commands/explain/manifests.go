package explain

import (
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/schema"
)

// manifestSchemas documents qctl apply manifests that have no 1:1 OpenAPI
// component schema (the manifest wrapper is a qctl-side construct). These are
// served statically so they work offline and always match what
// `qctl apply -f` actually parses. Keep in sync with
// dataset_rules.TableRuleApplyManifest.
var manifestSchemas = map[string]*schema.SchemaInfo{
	"table-rule":              tableRuleManifestSchema,
	"data-review-policy":      dataReviewPolicyManifestSchema,
	"rule-review-policy":      ruleReviewPolicyManifestSchema,
	"change-review-policy":    changeReviewPolicyManifestSchema,
	"table-schema":            tableSchemaManifestSchema,
	"deliverable":             deliverableManifestSchema,
	"notification-preference": notificationPreferencesManifestSchema,
}

// ManifestSchemaNames returns the resources served by a static manifest
// schema. Exported so a page's `manifest:` reference can be proven to resolve.
func ManifestSchemaNames() []string {
	names := make([]string, 0, len(manifestSchemas))
	for k := range manifestSchemas {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// producedFieldItemFields is ONE spec.produced_fields entry, declared once and
// rendered three ways, for the same reason tableSchemaColumnFields is: as
// Items.Properties it types the array, as Properties it makes
// `qctl explain 'table-rule.spec.produced_fields.field_schema'` resolve
// (findManifestField walks Properties and knows nothing about array items),
// and flattened by dottedColumnFields it survives the one-level summary
// `qctl explain table-rule` prints. One declaration means the three cannot
// drift.
//
// Each Description leads with its load-bearing clause, because the summary
// truncates at 77 characters.
var producedFieldItemFields = []schema.FieldInfo{
	{
		Name: "rule_field_name", Type: "string", Required: true,
		Description: "The rule's own name for the output field -- one of the revision's enriches_columns or corrects_columns, which `qctl get rule <name> -o yaml` lists. The \"*\" wildcard names no single column and is never one of them.",
	},
	{
		Name: "dataset_field_name", Type: "string", Required: true,
		Description: "The table column to create or reclaim for this output. It must not be a reserved metadata column and must already be in Atlas normalized form (`Gross Premium` is rejected, `gross_premium` accepted). An existing column must belong to this rule, or to the upgrade predecessor with the same rule field and schema; normal and unrelated rule columns cannot be reclaimed.",
	},
	{
		Name: "field_schema", Type: "object",
		Description: "Optional: omitted, qctl sends the rule author's own declaration for that output field, which is what the web wizard sends. State it to override: type (required) plus the optional args, is_nullable, indexed and the rest of a TableSchema column block. A `generated_by` written here is dropped -- the server stamps its own ownership block.",
	},
}

var rowFilterConditionFields = []schema.FieldInfo{
	{Name: "column_name", Type: "string", Required: true, Description: "Table column to compare."},
	{Name: "operator", Type: "string", Required: true, Enum: api.StringEnumValues["RowFilterOperator"], Description: "Comparison operator."},
	{Name: "value", Type: "string", Description: "Operand for eq, neq, gt, gte, lt, lte, or the low end of between."},
	{Name: "value_high", Type: "string", Description: "Inclusive high end of between."},
	{Name: "value_list", Type: "array", Items: &schema.FieldInfo{Type: "string"}, Description: "Non-empty operands for in and not_in."},
	{Name: "case_sensitive", Type: "boolean", Description: "Text comparisons are case insensitive by default."},
}

var rowFilterFields = []schema.FieldInfo{
	{Name: "description", Type: "string", Required: true, Description: "Which rows the saved filter keeps; non-blank, at most 200 characters."},
	{Name: "conditions", Type: "array", Items: &schema.FieldInfo{Type: "object", Properties: rowFilterConditionFields}, Properties: rowFilterConditionFields, Description: "AND-ed conditions on table columns."},
	{Name: "predicate", Type: "object", Description: "Optional Starlark predicate evaluated after all conditions match.", Properties: []schema.FieldInfo{
		{Name: "source", Type: "string", Required: true, Description: "Module defining def applies(row), returning a bool. Use decimal(\"12.5\") for decimal literals."},
		{Name: "columns", Type: "array", Required: true, Items: &schema.FieldInfo{Type: "string"}, Description: "Table columns available in row."},
	}},
}

var tableRuleManifestSchema = &schema.SchemaInfo{
	Name: "TableRule",
	Description: "Manifest for `qctl apply -f`. Instantiates a rule revision onto a table " +
		"(creates a table rule) when spec.rule_revision_id is set and metadata.id is absent; " +
		"patches an existing table rule when metadata.id is set. " +
		"See also: qctl guide instantiate-rule.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "spec"},
	Fields: []schema.FieldInfo{
		{
			Name: "apiVersion", Type: "string", Required: true,
			Description: "Must be qluster.ai/v1.",
		},
		{
			Name: "kind", Type: "string", Required: true,
			Description: "Must be TableRule.",
		},
		{
			Name: "metadata", Type: "object",
			Description: "Optional for instantiate ('metadata: {}' is valid). Setting metadata.id switches to patch mode.",
			Properties: []schema.FieldInfo{
				{
					Name: "id", Type: "string", Format: "uuid",
					Description: "Existing table rule ID. Present: patch that rule. Absent: instantiate a new one from spec.rule_revision_id.",
				},
				{
					Name: "instance_name", Type: "string",
					Description: "The same field as spec.instance_name, under the key `get table-rule -o yaml` writes. " +
						"Either key alone names the rule; setting both to different names exits 2.",
				},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: append([]schema.FieldInfo{
				{
					Name: "dataset_id", Type: "integer", Required: true,
					Description: "Target table (dataset) ID.",
				},
				{
					Name: "rule_revision_id", Type: "string",
					Description: "Rule revision to attach, as a full UUID, a SHORT-ID, or a slug. " +
						"Required for instantiate unless spec.rule is set. In patch mode it means " +
						"rebind this rule to that revision, which starts a revalidation candidate.",
				},
				{
					Name: "rule", Type: "string",
					Description: "Mutually exclusive with rule_revision_id: a rule name or slug to attach, " +
						"resolved to a revision at apply time. Pair with spec.release when the name has several " +
						"releases. Accepted in patch mode too, where it names the revision to rebind to.",
				},
				{
					Name: "release", Type: "string",
					Description: "Which release of spec.rule to attach. Only valid alongside spec.rule.",
				},
				{
					Name: "instance_name", Type: "string",
					Description: "Optional, unique per table; omit it for a server-generated name. When both " +
						"spec.instance_name and metadata.instance_name are omitted, the server takes the first of " +
						"`<Family>`, `<Family> on <cols>`, `<Family> on <cols> (2)`, ... that the table does not hold: " +
						"a built-in family's name as it is or a custom family's humanized, and at most two mapped columns, " +
						"then `+N more`. A name you set is used verbatim on an immediate create; accepting a staged create, " +
						"by hand or inside apply_migration, may add a suffix. Uniqueness is exact " +
						"and case-sensitive and counts every rule on the table: a deleted or validation_failed rule keeps " +
						"its name, and an upgrade (spec.upgradedFrom) cannot reuse its predecessor's. A create whose " +
						"name is taken is refused with exit 2 before the request, even with --yes. spec.force lifts that " +
						"refusal, and a forced create on a taken name can update the rule holding it in place and try to " +
						"enable it with no validation dry run. A rule in error or a failed produced-field activation check " +
						"(for example, a produced column missing from the live schema) still returns 409 (exit 2).",
				},
				{
					Name: "state", Type: "string",
					Enum: []string{"enabled", "disabled"},
					Description: "Patch mode only: an instantiate manifest's spec.state is silently dropped, " +
						"not rejected. `enabled`/`disabled` are the two worth sending by hand. " +
						"`error`, `pending_validation` and `validation_failed` are refused with exit 2 " +
						"before the request, because the server sets them and answers a requested one with a " +
						"422 -- delete the key a round-tripped manifest carries. " +
						"`deleted` is a real, working soft delete, not backend-managed (`qctl explain " +
						"table-rule --grep 'soft delete'` says why `disabled` is usually the better choice).",
				},
				{
					Name: "severity", Type: "string",
					Enum: []string{"warning", "blocker", "as_declared"},
					Description: "What this rule instance does with the severity each of its findings declares. " +
						"`warning` forces every finding to a warning, `blocker` forces every one to quarantine the row " +
						"as an alert, and `as_declared` keeps the severity the rule itself gave each finding -- so only " +
						"`as_declared` leaves both classes reachable. On instantiate, omitting it takes the rule " +
						"revision's declared default_severity, NOT a fixed value: qctl has no local copy of that " +
						"declaration, so state the key explicitly whenever the answer matters. On patch, omitting it " +
						"leaves the current value unchanged, and setting it applies to the live rule right away even " +
						"when a draft migration holds spec.params and spec.state for review -- unless the request " +
						"rides a revalidation candidate, one it starts by also changing the revision, column mapping " +
						"or produced fields, or one already pending, which bakes severity into that candidate's " +
						"snapshot instead and leaves the live rule untouched until its dry run passes. " +
						"A value outside the three is refused with exit 2 before the request. " +
						"Replaced the boolean treat_as_alert in the 2026-09-10 spec: a manifest still carrying that key is " +
						"refused with exit 2 rather than dropped, since `treat_as_alert: true` is `severity: as_declared` " +
						"and `false` is `severity: warning`.",
				},
				{Name: "row_filter", Type: "object", Properties: rowFilterFields, Description: "Rows this rule checks. Set conditions and/or a predicate plus description. On patch, omission keeps the filter and null removes it."},
				{
					Name: "params", Type: "object",
					Description: "Rule parameters. Validated client-side against the revision's param_schema before the request is sent. " +
						"A builtin range_rule's min and max are in the bounded column's display units, which differ from the stored ones only on a percent column: " +
						"max: 12.5 there is 12.5%, stored as 0.125.",
				},
				{
					Name: "column", Type: "string",
					Description: "Instantiate-only shorthand: the table column to attach the rule to. " +
						"Expands to column_mapping with the rule's single required field as the key, " +
						"so `column: booking_number` on required_field sends {value: booking_number}. " +
						"Mutually exclusive with column_mapping, rejected in patch mode, and rejected for a " +
						"rule with zero or several required fields -- use column_mapping there. Count distinct input/validates/corrects/enriches fields, excluding '*' and optional_columns, including fields supplied by produced declarations or standard fields.",
				},
				{
					Name: "column_mapping", Type: "object",
					Description: "Maps rule fields to existing table columns. On instantiate, a non-optional field with a standard field carried by exactly one table column is left out of the request so the server fills it; an explicit mapping wins. When column_mapping is omitted or empty, other inputs (including optional ones) and required validates/corrects/enriches fields not declared in produced_fields use the same name as the table column. Optional role-only fields stay unbound. A builtin's input names (value, field, operand_1) are often placeholders, so use spec.column or a mapping when no unique standard field column exists. An explicit mapping must bind every non-optional input/validates/corrects/enriches field except '*' unless a produced_fields declaration or unique standard field column supplies it; missing fields are rejected locally with exit 2, including under spec.force. Fields in optional_columns may be left unbound in an explicit mapping. Patch mode sends the mapping as written.",
				},
				{
					Name: "force", Type: "boolean",
					Description: "Instantiate mode: override the controller-level force flag.",
				},
				{
					Name: "upgradedFrom", Type: "string",
					Description: "Instantiate mode only: the existing table rule this one replaces, as a full dataset-rule UUID or an r-* alias. qctl resolves and fetches it before the POST, requires it to be on spec.dataset_id and in the new revision's rule family, and sends its UUID as upgraded_from_dataset_rule_id. The predecessor remains active until the replacement is enabled; a staged replacement remains active until its suggestion is accepted. Atlas then retires the predecessor and puts the replacement in its rule position.",
				},
				{
					Name: "produced_fields", Type: "array",
					Description: "Table columns this rule writes -- what makes an enrichment or correcting rule attachable at all: the backend requires each output field the mapping has to bind (all of them bar the revision's optional_columns) to be either mapped to an existing column or declared here, and one named in neither is what the API rejects as `rule_column_mapping lacks keys for required columns`. Declaring any makes the new rule land in pending_validation. Once validation passes, existing matching predecessor columns let an upgrade enable directly; new columns need a draft migration applied first. In patch mode the key ADDS an output column, validated as a revalidation candidate before it reaches the live rule; omitting it leaves the rule's declarations alone. Dropping one (an empty list included) or retyping one without adding a column is refused with exit 2: a drop of an active column read by a linked table gets a 409 at PATCH time; otherwise the candidate can pass its dry run but fail promotion.",
					Items:       &schema.FieldInfo{Type: "object", Properties: producedFieldItemFields},
					Properties:  producedFieldItemFields,
				},
			}, dottedColumnFields("produced_fields.", producedFieldItemFields)...),
		},
	},
}

// dataReviewPolicyManifestSchema documents `kind: DataReviewPolicy` offline.
// The word "quarantine" is deliberate: it is the term users search for and it
// appears nowhere in the backend schema. The static schema here is
// deliberate, not a stopgap: it works offline and matches the fields
// `qctl apply -f` accepts, apart from the output-only `status` block,
// independent of what atlas's own OpenAPI schema documents.
var dataReviewPolicyManifestSchema = &schema.SchemaInfo{
	Name: "DataReviewPolicy",
	Description: "Manifest for `qctl apply -f`. Gates rows that land in quarantine: " +
		"whether they require reviewer approval before release, how many reviewers, " +
		"and whether approval releases them automatically. Omit spec.dataset_id for an " +
		"org-level policy. See also: qctl guide data-review-policy.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be DataReviewPolicy."},
		{
			Name: "metadata", Type: "object",
			Description: "Optional. metadata.name is a human label; metadata.id is set on round-tripped output.",
			Properties: []schema.FieldInfo{
				{Name: "name", Type: "string", Description: "Human-readable policy name."},
				{Name: "id", Type: "string", Format: "uuid", Description: "Existing policy ID (present on `get -o yaml` output)."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: []schema.FieldInfo{
				{Name: "dataset_id", Type: "integer", Description: "Target table (dataset) ID. Omit for an org-level policy that applies to every table."},
				{Name: "requires_review", Type: "boolean", Required: true, Description: "If true, rows landing in quarantine require reviewer approval before they can be released."},
				{Name: "auto_release_on_approval", Type: "boolean", Description: "If true, a row whose data review is approved is released out of quarantine automatically; if false, release is a separate step. 'qctl rows release' ignores this setting: it records the caller's approval and releases a row only once it has enough approvals, skipping rows the caller edited or submitted for review."},
				{Name: "min_reviewers", Type: "integer", Description: "Minimum number of distinct eligible reviewers who must approve the row's current version before release. Anyone who submitted or ever edited the row, including undo and redo actors, is excluded."},
				{Name: "designated_reviewer_ids", Type: "array", Items: &schema.FieldInfo{Type: "string", Format: "uuid"}, Description: "If set, only these reviewers' approvals count toward min_reviewers."},
			},
		},
	},
}

// ruleReviewPolicyManifestSchema documents `kind: RuleReviewPolicy` offline.
// This policy gates rule-revision reviews (not data quarantine). The static
// schema here is deliberate, not a stopgap: atlas's ReviewPolicyRequestSchema
// already carries a description on every field, but this copy still works
// offline and matches the fields `qctl apply -f` accepts, apart from the
// output-only `status` block.
var ruleReviewPolicyManifestSchema = &schema.SchemaInfo{
	Name: "RuleReviewPolicy",
	Description: "Manifest for `qctl apply -f`. Governs how submitted rule revisions are " +
		"reviewed before they can be enabled: whether review is required, how many reviewers, " +
		"and dry-run requirements. Omit spec.dataset_id for an org-level policy. " +
		"See also: qctl guide review-rules.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be RuleReviewPolicy."},
		{
			Name: "metadata", Type: "object",
			Description: "Optional. metadata.name is a human label; metadata.id is set on round-tripped output.",
			Properties: []schema.FieldInfo{
				{Name: "name", Type: "string", Description: "Human-readable policy name."},
				{Name: "id", Type: "string", Format: "uuid", Description: "Existing policy ID (present on `get -o yaml` output)."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: []schema.FieldInfo{
				{Name: "dataset_id", Type: "integer", Description: "Target table (dataset) ID. Omit for an org-level policy."},
				{Name: "requires_review", Type: "boolean", Description: "If true, a submitted rule revision must pass review before it can be enabled."},
				{Name: "min_reviewers", Type: "integer", Description: "Minimum number of distinct reviewers who must approve a rule revision."},
				{Name: "designated_reviewer_ids", Type: "array", Items: &schema.FieldInfo{Type: "string", Format: "uuid"}, Description: "If set, only these reviewers' approvals count toward min_reviewers."},
				{Name: "dismiss_stale_reviews", Type: "boolean", Description: "If true, existing approvals are dismissed when the rule revision changes."},
				{Name: "require_dry_run", Type: "boolean", Description: "If true, a dry run must be linked to the review before the revision can be enabled."},
				{Name: "require_dry_run_success", Type: "boolean", Description: "If true, the linked dry run must have passed before the revision can be enabled: finished, with no rule load errors and no rule-bug or sandbox failure."},
				{Name: "auto_enable_on_approval", Type: "boolean", Description: "If true, an approved rule revision is enabled automatically."},
			},
		},
	},
}

// changeReviewPolicyManifestSchema documents `kind: ChangeReviewPolicy`
// offline. spec.operations is a nested document the static schema describes
// rather than enumerates; `qctl get change-review-policy --template` prints
// every key.
var changeReviewPolicyManifestSchema = &schema.SchemaInfo{
	Name: "ChangeReviewPolicy",
	Description: "Manifest for `qctl apply -f`. A whitelist of the roles that may resolve directly, " +
		"propose, and approve each governed operation key; while a policy is in force, a key it does not " +
		"list is denied to every role. Omit spec.dataset_id for the organization policy. " +
		"Start from `qctl get change-review-policy --template`.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be ChangeReviewPolicy."},
		{
			Name: "metadata", Type: "object",
			Description: "Optional. metadata.name is a human label (org-change-review-policy or table-<id>-change-review-policy on `get -o yaml` output).",
			Properties: []schema.FieldInfo{
				{Name: "name", Type: "string", Description: "Human-readable policy name."},
				{Name: "id", Type: "string", Description: "Label used when name is absent."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: []schema.FieldInfo{
				{Name: "dataset_id", Type: "integer", Description: "Target table (dataset) ID. Omit for the organization policy."},
				{Name: "require_distinct_approver", Type: "boolean", Description: "Whether a proposal's approver must differ from its proposer. Required on the organization policy; omitted on a table policy, the table inherits the organization's value."},
				{Name: "operations", Type: "object", Required: true, Description: "Per operation, its keys nested down to an entry of resolve, propose and approve role lists. alert_resolve and warning_resolve nest issue type, then action (or no_action / field_names_resolution); every other operation has one key part, e.g. dataset_rule_patch -> field. Every entry carries all three verbs. On a table policy an entry replaces the organization's whole entry for that key."},
			},
		},
		{
			Name: "status", Type: "object", ReadOnly: true,
			Description: "Output-only context from `get -o yaml`; apply ignores it.",
			Properties: []schema.FieldInfo{
				{Name: "source", Type: "string", Description: "organization or dataset: where the policy shown came from."},
				{Name: "policy_id", Type: "string", Format: "uuid", Description: "The stored policy shown."},
				{Name: "organization_policy_id", Type: "string", Format: "uuid", Description: "On a table's effective policy: the organization policy, if any."},
				{Name: "dataset_policy_id", Type: "string", Format: "uuid", Description: "On a table's effective policy: the table override, if any."},
				{Name: "uncovered", Type: "object", Description: "On a table's effective policy: per operation, the registered keys the policy does not list, which no role may resolve, propose or approve."},
			},
		},
	},
}

// notificationPreferencesManifestSchema documents `kind:
// NotificationPreferences` offline.
var notificationPreferencesManifestSchema = &schema.SchemaInfo{
	Name: "NotificationPreferences",
	Description: "Manifest for `qctl apply -f`. Your notification email settings in the current " +
		"organization: spec.email replaces every override you have, and a type it leaves out follows " +
		"your role's default. Start from `qctl get notification-preferences -o yaml`.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "metadata", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be NotificationPreferences."},
		{
			Name: "metadata", Type: "object", Required: true,
			Description: "metadata.name is a label for the apply output (me on `get -o yaml` output).",
			Properties: []schema.FieldInfo{
				{Name: "name", Type: "string", Required: true, Description: "Label printed as notification-preference/<name>."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: []schema.FieldInfo{
				{Name: "email", Type: "object", Required: true, Description: "Notification type to true or false; null exits 2. The types are the ones `qctl get notification-preferences` lists; any other exits 2 before anything is written. An entry equal to its role default is not stored, and {} restores every role default."},
			},
		},
		{
			Name: "status", Type: "object", ReadOnly: true,
			Description: "Output-only context from `get -o yaml`; apply ignores it.",
			Properties: []schema.FieldInfo{
				{Name: "effective", Type: "object", Description: "Notification type to the setting that applies to you now."},
				{Name: "defaults", Type: "object", Description: "Notification type to your role's default in this organization."},
			},
		},
	},
}

// tableSchemaColumnFields is ONE column's editable definition, declared once
// and rendered twice.
//
// Nested, it is spec.fields.<column>.Properties, which is what makes
// `qctl explain 'table-schema.spec.fields.<column>.is_deleted'` resolve --
// findManifestField walks Properties, so a dotted Name alone is a dead end and
// the description would only ever be reachable truncated at 77 chars.
// Flattened by dottedColumnFields, the same entries are spec's direct
// properties, which is what keeps them in the one-level summary
// `qctl explain table-schema` prints (printFieldSummaryChildren stops at depth
// 1 unless --recursive). Neither surface alone covers both readers, and one
// declaration means the two cannot drift.
//
// Each Description leads with its load-bearing clause, because the summary
// truncates at 77 characters.
var tableSchemaColumnFields = []schema.FieldInfo{
	{Name: "type", Type: "string", Required: true, Enum: api.FieldTypes, Description: "The column type -- required on every stated column. Every accepted value is listed under ALLOWED VALUES; the `qctl explain table-schema` summary abbreviates that list, this drill-down prints it whole."},
	{Name: "args", Type: "array", Items: &schema.FieldInfo{Type: "integer"}, Description: "Type arguments: [64] for String(64), [12, 2] for Decimal(12,2)."},
	{Name: "is_nullable", Type: "boolean", Description: "Whether the column accepts nulls. A column being ADDED is always added nullable, and the server refuses a stated false on one, so qctl rejects it before sending."},
	{Name: "indexed", Type: "boolean", Description: "Whether to build a database index on the column. The backend always indexes Date, DateTime, UUID, SmallIntArray, IntArray, BigIntArray and UUIDArray columns and overwrites a stated false, so qctl rejects `indexed: false` on those types."},
	{Name: "trgm_indexed", Type: "boolean", Description: "Set by the backend from the type, whatever the block says: true for String, Text, Array, Json, Jsonb and every numeric type, and absent for the rest. qctl rejects a stated value that disagrees -- false on a type that gets one, any value on a type that gets none -- because the backend would store its own and the line would show as a change on every diff. `get -o yaml` emits the flag, so a block retyped from a type that gets one can carry a `trgm_indexed: true` that has to be removed."},
	{Name: "is_deleted", Type: "boolean", Description: "A mode switch, not a property: stop ingesting this column (true) or resume (false). The backend applies it alone, discards every other property in the same block, and forces is_nullable true whichever value it takes -- so qctl rejects a block that sets it alongside another change. It also edits settings_model.ignore_column_names. `get -o yaml` states it only on a deleted column, as true. On a deleted column, removing the line changes nothing, and a block without it that changes anything, or `<column>: null`, exits 2 before anything is sent, since the backend would reject the whole request: restore first with a block whose only change is `is_deleted: false`, then edit in a second apply. On a column the schema does not have -- one this manifest would ADD -- either value is rejected: there is nothing to delete."},
	{Name: "is_dollar", Type: "boolean", Description: "Show the column as money, e.g. $1,234.56; values are stored as dollars, unscaled. Rejected together with is_percent: the backend would show such a column as currency while its cleaner divides loaded values by 100 as a percent."},
	{Name: "is_percent", Type: "boolean", Description: "Percentages: a loaded 12.5 or 12.5% is stored as the fraction 0.125 and shown as 12.50%. Rejected together with is_dollar."},
	{Name: "display_decimals", Type: "integer", Description: "Places a numeric column is shown rounded to, 0 to 16383; unset is the default -- 2 for money and percent, none for a plain number. An omitted line keeps the column's current value, and `display_decimals: null` -- the one property here that takes a null of its own -- puts it back to the default. Search, the CSV download and `qctl get data` all round the stored value to it. Changing it rescales nothing stored."},
	{Name: "datetime_formats", Type: "array", Items: &schema.FieldInfo{Type: "string"}, Description: "Accepted datetime formats for this column."},
}

// dottedColumnFields re-labels a nested property set as dotted leaves of the
// parent, so the same declarations appear in the depth-limited summary.
func dottedColumnFields(prefix string, fields []schema.FieldInfo) []schema.FieldInfo {
	out := make([]schema.FieldInfo, 0, len(fields))
	for _, f := range fields {
		f.Name = prefix + f.Name
		out = append(out, f)
	}
	return out
}

// tableSchemaFieldsField is spec.fields: the column map itself, carrying the
// per-column definition as a nested <column> node.
var tableSchemaFieldsField = schema.FieldInfo{
	Name: "fields", Type: "object", Required: true,
	Description: "Column name -> desired definition. A column you leave out is a NO-OP, not a drop: removing a field from the file changes nothing, and `is_deleted: true` is what stops a column being ingested. A column set to null clears its override entirely, putting the column back to the inferred definition; on a deleted column a null exits 2, so restore it first with a block whose only change is `is_deleted: false`. A column name the schema does not have ADDS that column, with exactly the definition the block states; `qctl diff -f` and the apply prompt show it as `add column`, which is what catches a mistyped name.",
	Properties: []schema.FieldInfo{
		{
			Name: "<column>", Type: "object",
			Description: "One column, keyed by its current name. Drill into a property with `qctl explain 'table-schema.spec.fields.<column>.<property>'` (quote it -- the angle brackets are shell redirections otherwise).",
			Properties:  tableSchemaColumnFields,
		},
	},
}

// tableSchemaManifestSchema documents `kind: TableSchema` offline. Static, and
// deliberately so: it works with no API reachable and it matches the fields
// `qctl apply -f` actually parses, apart from the output-only status block.
var tableSchemaManifestSchema = &schema.SchemaInfo{
	Name: "TableSchema",
	Description: "Manifest for `qctl apply -f`. Changes the column definitions of a table's " +
		"schema: type, size, nullability, indexing and the monetary/percent flags. Round-trips " +
		"from `qctl get table-schema <table> -o yaml`; preview with `qctl diff -f`. " +
		"A column name the schema does not have is added; it cannot drop or rename one -- naming a column something else adds a second column and leaves the first untouched. " +
		"Writing needs the MIGRATION_EXECUTE permission (ops, admin and agent roles only). " +
		"See also: qctl guide inspect-schema.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "metadata", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be TableSchema."},
		{
			Name: "metadata", Type: "object", Required: true,
			Description: "Names the table whose schema this is. Give metadata.id or metadata.name; id is preferred because it survives a rename.",
			Properties: []schema.FieldInfo{
				{Name: "name", Type: "string", Description: "Table (dataset) name."},
				{Name: "id", Type: "string", Description: "Table (dataset) ID. Written as an integer in YAML; emitted quoted."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Properties: append(
				[]schema.FieldInfo{tableSchemaFieldsField},
				dottedColumnFields("fields.<column>.", tableSchemaColumnFields)...,
			),
		},
		{
			Name: "status", Type: "object",
			Description: "Emitted by `qctl get table-schema -o yaml` and ignored by apply. Carries schema_state, state, confirmed, migration_id, settings_model_version_id and migration_model_version_id.",
		},
	},
}

// deliverableSpecFields is spec of a kind: Deliverable manifest, as
// deliverables.ManifestSpec decodes it.
var deliverableSpecFields = []schema.FieldInfo{
	{Name: "name", Type: "string", Required: true, Description: "The deliverable's name, 1-200 characters. On an existing deliverable a different name renames it; the slug stays."},
	{Name: "receiver_label", Type: "string", Description: "Who receives the file. Omitted, an existing deliverable keeps its value and a new one gets \"\"; null exits 2, so write \"\" for none."},
	{Name: "filename_pattern", Type: "string", Required: true, Description: "The output file's name, with {param} placeholders, e.g. bordereau-{period}.xlsx."},
	{Name: "period_param", Type: "string", Nullable: true, Description: "The run parameter whose value picks a series member by period key, e.g. period. Omitted keeps the stored value (none on a new deliverable); null clears it; a blank value exits 2."},
	{Name: "release", Type: "string", Description: "The one release (revision) the manifest names, 1-100 characters, matched exactly. A release the deliverable lacks is created as a draft: a copy of the enabled release when there is one, else empty. Of an existing release, apply changes only a draft's bindings, through spec.sheets and spec.cells. Omitted, apply writes the basic fields only."},
	{Name: "description", Type: "string", Nullable: true, Description: "Create-only: the release's description, sent only when apply creates the release, which does not inherit the enabled release's. On an existing release a different value exits 2 before any write; omitted, it is not compared. Requires spec.release."},
	{Name: "template", Type: "string", Description: "Create-only: an .xlsx or .xlsm file, relative to the manifest's directory unless absolute, uploaded before any other write when apply creates the release, replacing a copied template. On an existing release it is not compared; apply prints the qctl upload deliverable-template command for a draft. diff never uploads it. Requires spec.release."},
	{Name: "acknowledge_lossy", Type: "boolean", Description: "Attach a template whose fidelity report is lossy. Without it, a lossy template exits 2 listing the losses, before the deliverable or the release is written. Requires spec.template."},
	{
		Name: "sheets", Type: "object",
		Description: "The release's sheet bindings, keyed by template sheet name. Requires spec.release. Present, it is the full set: apply deletes a sheet binding it does not name, " +
			"with its column bindings, only after confirmation (--yes, or a y/N prompt at a terminal); omitted, apply leaves every sheet alone. " +
			"Only a draft's bindings change: on a release that is not a draft, a difference exits 2 before any write, so bump spec.release. " +
			"Writing a sheet or column binding to a release with no template or to a sheet its template lacks (names match exactly, case included), " +
			"or deleting a sheet that an unmanaged cell binding totals, also exits 2 before any write; for a release apply creates with spec.template, " +
			"the sheet-name check waits until after the release's POST, and diff skips it. " +
			"Drill in with `qctl explain 'deliverable.spec.sheets.<sheet>'` (quote it -- the angle brackets are shell redirections otherwise).",
		Properties: []schema.FieldInfo{{
			Name: "<sheet>", Type: "object",
			Description: "One sheet binding, keyed by the template sheet's name. A changed name is a delete plus a create, not a rename.",
			Properties:  deliverableSheetFields,
		}},
	},
	{
		Name: "cells", Type: "object",
		Description: "The release's cell bindings, keyed by Sheet!A1. Requires spec.release. A1 references are normalized to upper case without $ markers or leading row zeros, within A1:XFD1048576; duplicate normalized keys exit 2. " +
			"Present, this is the full set: omitted cells are deleted after confirmation (--yes or a terminal prompt), while omitting spec.cells leaves them unmanaged. " +
			"Only draft bindings change. Cells are saved after sheet writes; mirrors of removed sheets are deleted first and recreated if still wanted. " +
			"A copied template may move cells before apply re-plans them; managing those cells then requires removal confirmation too.",
		Properties: []schema.FieldInfo{{Name: "<cell>", Type: "object", Description: "One cell, e.g. Summary!B3. Set exactly one of run_param, literal or total.", Properties: deliverableCellFields}},
	},
}

var deliverableCellFields = []schema.FieldInfo{
	{Name: "run_param", Type: "string", Description: "A non-blank run parameter name. Excludes literal and total."},
	{Name: "literal", Type: "string", Description: "Fixed text, including an empty string. Excludes run_param and total; null does not select a source. At most 32767 characters, with no control character but tab, newline and carriage return."},
	{Name: "total", Type: "object", Description: "An aggregate of a table, a series member, or a bound sheet's rendered rows. Excludes run_param and literal.", Properties: deliverableTotalFields},
	{Name: "label_cell", Type: "string", Description: "An A1 reference on this cell's sheet with identifying text, different from the bound cell. Normalized like the target. Omitted, a PATCH or a mirror recreated after sheet removal keeps the stored label; a new cell lets the server choose one. Null has the same effect as omission."},
	{Name: "format", Type: "string", Description: "An Excel number format. Omitted or null clears a stored format on save."},
}

var deliverableTotalFields = []schema.FieldInfo{
	{Name: "table", Type: "string", Description: "A table name or numeric ID. Required unless same_rows_as is set; with series, this is its reference table and must be a member. Exported as an ID."},
	{Name: "series", Type: "string", Description: "A table series by name or UUID. Requires table, excludes same_rows_as, and is exported by name. Omitted means no series; validation requires the deliverable's period_param when one is set."},
	{Name: "same_rows_as", Type: "string", Description: "The name of a bound sheet whose rendered rows supply this total. Excludes table and series. The sheet must survive spec.sheets or be created by it; the target cell may be on another sheet."},
	{Name: "func", Type: "string", Required: true, Description: "sum, count, min or max."},
	{Name: "column", Type: "string", Description: "The source table column; required for sum, min and max, optional for count."},
	{Name: "row_filter", Type: "object", Nullable: true, Properties: rowFilterFields, Description: "The rows of table the total runs over: conditions and/or a predicate, plus a description. Excludes same_rows_as. Omitted keeps the stored filter; null clears it. Exported when the total has one."},
}

// deliverableSheetFields is one spec.sheets entry, as
// deliverables.ManifestSheet decodes it.
var deliverableSheetFields = []schema.FieldInfo{
	{Name: "table", Type: "string", Required: true, Description: "The table whose rows fill the sheet, by name or ID (a string or integer in YAML and JSON); with series, the series' reference table, which must be one of its members. Exported as the table's ID string."},
	{Name: "series", Type: "string", Nullable: true, Description: "The table series whose table for the run's period fills the sheet, by name or UUID. Omitted keeps the stored series (none on a new sheet); null clears it. Exported by name. While the deliverable has no period_param, apply and diff print a Note: validate reports an error until spec.period_param is set."},
	{Name: "header_rows", Type: "array", Required: true, Items: &schema.FieldInfo{Type: "integer"}, Description: "The template rows holding the column header labels: at most 3, each from 1 to 1048576 (Excel's last row), strictly increasing and above data_start_row; [] for none. Changing it or data_start_row makes the server re-anchor the sheet's column bindings by their header labels."},
	{Name: "data_start_row", Type: "integer", Required: true, Description: "The first template row the table's rows fill, from 1 to 1048576 (Excel's last row). The server refuses a sheet write while a template cell at or below data_start_row + instruction_row_count is non-empty."},
	{Name: "instruction_row_count", Type: "integer", Description: "How many template rows from data_start_row on may be non-empty, 0-10. Omitted keeps the stored value (0 on a new sheet)."},
	{
		Name: "row_order", Type: "array", Items: &schema.FieldInfo{Type: "object", Properties: deliverableRowOrderFields}, Properties: deliverableRowOrderFields,
		Description: "The sheet's row order, each column at most once; [] clears it. Omitted keeps the stored order (none on a new sheet). Sent as sort_keys, positioned by list order from 0.",
	},
	{Name: "row_filter", Type: "object", Nullable: true, Properties: rowFilterFields, Description: "The rows of table that fill the sheet: conditions and/or a predicate, plus a description. Omitted keeps the stored filter (none on a new sheet); null clears it. Exported when the sheet has one."},
	{
		Name: "columns", Type: "object",
		Description: "The sheet's column bindings, keyed by column letter (A to XFD) or 1-based index (1 to 16384, without leading zeros); two keys naming one column exit 2. " +
			"Present, it is the full set: apply deletes a binding it does not name, only after the same confirmation as a sheet; omitted, apply leaves the sheet's column bindings alone. " +
			"A header_rows or data_start_row change, or spec.template replacing a copied release's template, asks for that confirmation too when the sheet has column bindings: " +
			"the server may move them first, and apply removes any it leaves outside the listed columns. " +
			"A binding that needs confirmation and matches is not saved: apply and diff print a Note naming qctl deliverable confirm-columns. Exported by letter.",
		Properties: []schema.FieldInfo{{
			Name: "<column>", Type: "object",
			Description: "One column binding: exactly one of column, run_param or literal, plus an optional format. Apply saves a changed binding in place, at the same index.",
			Properties:  deliverableColumnFields,
		}},
	},
}

// deliverableRowOrderFields is one row_order entry, as
// deliverables.ManifestSortKey decodes it.
var deliverableRowOrderFields = []schema.FieldInfo{
	{Name: "column", Type: "string", Required: true, Description: "A column of the sheet's table."},
	{Name: "descending", Type: "boolean", Description: "Sort by the column in descending order; omitted, ascending."},
}

// deliverableColumnFields is one columns entry, as
// deliverables.ManifestColumn decodes it.
var deliverableColumnFields = []schema.FieldInfo{
	{Name: "column", Type: "string", Description: "The table column whose values fill the template column. Excludes run_param and literal."},
	{Name: "run_param", Type: "string", Description: "The run parameter whose value is written on every row of the sheet. Excludes column and literal."},
	{Name: "literal", Type: "string", Description: "Fixed text, including an empty string, repeated for each kept row. Excludes column and run_param; null does not select a source. At most 32767 characters, with no control character but tab, newline and carriage return."},
	{Name: "format", Type: "string", Description: "An Excel number format for the written cells, e.g. 0.00. Omitted, the cells keep the template's style: apply saves the whole binding, so an omitted format clears a stored one."},
}

// deliverableManifestSchema documents `kind: Deliverable` offline.
var deliverableManifestSchema = &schema.SchemaInfo{
	Name: "Deliverable",
	Description: "Manifest for `qctl apply -f`. Creates or updates a deliverable's name, receiver_label, " +
		"filename_pattern and period_param, which belong to no release and take effect as soon as apply sends them; " +
		"creates one draft release, optionally from a template, and makes a draft release's sheet and column bindings match spec.sheets; " +
		"it never enables a release. Round-trips from `qctl get deliverable <ref> -o yaml`; preview with `qctl diff -f`. " +
		"Cell bindings are managed through spec.cells.",
	Type:     "object",
	Required: []string{"apiVersion", "kind", "metadata", "spec"},
	Fields: []schema.FieldInfo{
		{Name: "apiVersion", Type: "string", Required: true, Description: "Must be qluster.ai/v1."},
		{Name: "kind", Type: "string", Required: true, Description: "Must be Deliverable."},
		{
			Name: "metadata", Type: "object", Required: true,
			Description: "Names the deliverable by its slug.",
			Properties: []schema.FieldInfo{
				{Name: "slug", Type: "string", Required: true, Description: "The deliverable apply writes, matched exactly, in the trash or not; one in the trash exits 2 naming qctl undelete deliverable. With no match, apply creates a deliverable only when this is the slug spec.name generates, and otherwise exits 2 naming that slug."},
			},
		},
		{
			Name: "spec", Type: "object", Required: true,
			Description: "A deliverable's basic fields, at most one release, and that release's sheet, column and cell bindings.",
			Properties:  deliverableSpecFields,
		},
		{
			Name: "status", Type: "object",
			Description: "Emitted by `qctl get deliverable -o yaml` and ignored by apply: id, and the exported release's revision_id and revision_state.",
		},
	},
}

// findManifestField resolves a dot-separated field path (e.g.
// "spec.column_mapping") within a static manifest schema.
//
// Both refusals are qerrors.NewBadUsagef, not fmt.Errorf: an unresolvable
// field path is a client-side argument validation failure, and the repo's
// contract puts every one of those on exit 2. A bare fmt.Errorf carries no
// *errors.Error for GetExitCode to find, so it degraded silently to exit 1
// while the message still read perfectly.
func findManifestField(info *schema.SchemaInfo, fieldPath string) (*schema.FieldInfo, error) {
	parts := strings.Split(fieldPath, ".")
	fields := info.Fields
	for i, part := range parts {
		var match *schema.FieldInfo
		for j := range fields {
			if fields[j].Name == part {
				match = &fields[j]
				break
			}
		}
		if match == nil {
			return nil, qerrors.NewBadUsagef("field %q not found in %s manifest (at %q)", part, info.Name, strings.Join(parts[:i+1], "."))
		}
		if i == len(parts)-1 {
			return match, nil
		}
		fields = match.Properties
	}
	return nil, qerrors.NewBadUsagef("field path %q not found", fieldPath)
}
