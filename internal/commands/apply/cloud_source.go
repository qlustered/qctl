package apply

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the apply cloud-source command
func NewCloudSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-source",
		Short: "Apply a cloud source configuration from a file",
		Long: fmt.Sprintf(`Apply a cloud source configuration from a YAML file.

This command creates the cloud source if the target table has none of that
name, or updates the one with the same name within the target table.

'qctl get cloud-source <id> -o yaml' prints a source in this form, minus
secrets. The export re-applies to an existing source as is. A new active sftp
source needs sftp_password or sftp_ssh_key, and a new active dropbox source
needs dropbox_authorization_code. A simple_url source needs simple_url unless
the manifest creates a draft, and a draft needs no credentials.

spec.state: draft creates the source as a draft. The server still checks its
schedule and URL format, but does not connect to it. Apply then prints a Next:
line: remove spec.state, add the secrets, apply again, and turn the source on
with 'qctl enable cloud-source <id>'. draft is the only value spec.state takes,
and an update that carries it exits 2; change an existing source's state with
enable or disable cloud-source.

SCHEMA
======
  apiVersion: qluster.ai/v1            # Required. Must be "qluster.ai/v1"
  kind: CloudSource                    # Required. Must be "CloudSource"
  metadata:
    name: <string>                     # Required. Identifies the source within the table
    labels:                            # Optional
      <key>: <value>
    annotations:                       # Optional
      <key>: <value>
  spec:
    dataset_id: <int>                  # Required. Target table ID
    data_source_type: <string>         # Required. %s
    settings_model_id: <int>           # Optional. Resolved from dataset_id; must match it if given
    state: draft                       # Optional. Create only; omit for an active source
    table_series_id: <uuid>            # Optional. Create only; makes a series source
    routing_mode: <string>             # Required for a series source: uploader_chooses, file_name, column or latest_unlocked
    routing_pattern: <string>          # file_name only; holds {period_key}, or {YYYY} and {MM}
    routing_column: <string>           # column only; the reporting-period column
    <field>: <value>                   # Any writable field of the source
    settings_overrides:                # Optional. This source's overrides of table settings
      <setting>: <value>               # Keyed by the table setting's name, e.g. null_values

'qctl explain cloud-source' describes each field of the source, an override
under its wire name, <setting>_override. It marks dropbox_authorization_code,
which the read never returns, <write-only>, and state, which only a create
takes, <create-only>.

UPDATE SEMANTICS
================
The manifest is the source's whole desired state. %s
Any other field it leaves out keeps its stored value, every credential and
schedule included; schedule: "" stops polling.

To update, apply reads the live source and prints each change, never a
credential's value, and the two server side effects it knows of: a change to
ignore_column_ids or the fields_to_expand, infer_datetime,
infer_datetime_for_columns or ignore_column_names override can queue the table
to learn its schema again, and a csv_* change resolves the source's
cant_sniff_header alerts you may resolve and requeues their jobs. Then
it asks to confirm. -o json|yaml and a non-interactive run need --yes (-y), which
skips the change list and the question and prints those side effects to stderr
once the update succeeds. Apply sends changed fields and, on a repointing update, supplied secrets,
with the version it read. If something else wrote the source in between (another edit,
enable or disable, an alert resolution scoped to it, or a --signature row
delete scoped to it), apply fails: re-run it to read the source again. A
manifest that matches the live source sends nothing and reports "unchanged". A
create is sent without a prompt.

Credentials support an exact ${ENV} value; other values preserve literal dollar
signs. A credential that expands to nothing, such as
${VAR} with VAR unset, or that is masked ("**********", or any value
containing '•') is never sent, so an ordinary update keeps its stored value. The read
masks every credential but s3_access_key, so one the manifest sets never
matches the live source: apply sends it again on every update and never
reports "unchanged". Leave it out, or its ${VAR} unset, to keep the stored
value.

Changing the source's location or file selection (such as pattern, prefix or
excel_sheet_name) requires every stored secret again. Apply refuses a
non-draft update with exit 2 and lists the missing spec keys; supply them using
${VAR} and re-run. On a draft it warns before sending that omitted secrets
will be cleared. A simple_url counts as a secret for this check: it supports
${VAR}, is hidden in the change list, and is re-sent when supplied even if its
value is unchanged. An unedited URL in an exported manifest counts as supplied.

EXAMPLE
=======
# File: cloud-source-s3.yaml
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  s3_bucket: raw-orders
  s3_prefix: incoming/
  s3_region_name: us-east-1
  s3_access_key: ${S3_ACCESS_KEY}
  s3_secret_key: ${S3_SECRET_KEY}
  schedule: "@hourly"
  settings_overrides:
    null_values: ["N/A"]

# Apply the configuration:
export S3_ACCESS_KEY=abc
export S3_SECRET_KEY=def
qctl apply cloud-source -f cloud-source-s3.yaml`, dataSourceTypeValues(), clearedWhenOmittedText()),
		Annotations: cmdutil.WithFormats(catalog.For("cloud-source"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)")
			}
			return applyStream(cmd, filePath, onlyKind(applyCloudSource))
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// dataSourceTypeValues is every valid spec.data_source_type, joined with "|".
func dataSourceTypeValues() string {
	types := cloud_sources.ValidDataSourceTypes()
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return strings.Join(names, "|")
}

// clearedWhenOmittedText is the help's sentence naming the omissions an update
// clears: every override and each of cloud_sources.ClearedWhenOmittedKeys.
func clearedWhenOmittedText() string {
	keys := cloud_sources.ClearedWhenOmittedKeys()
	if len(keys) == 0 {
		return "An override it leaves\nout is cleared."
	}
	return "An override it leaves\nout is cleared, and so is any of these fields it leaves out:\n  " +
		strings.Join(keys, ", ")
}

// applyCloudSource is the shared apply logic for cloud source manifests, used by
// both the "apply cloud-source" subcommand and the generic "apply -f" dispatcher.
func applyCloudSource(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	sourceManifest, err := parseCloudSourceManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}

	processCloudSourceEnv(sourceManifest)

	if errs := sourceManifest.Validate(); len(errs) > 0 {
		var messages []string
		for _, e := range errs {
			messages = append(messages, e.Error())
		}
		return nil, qerrors.NewBadUsagef("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}

	if err := resolveSettingsModelID(ctx, sourceManifest); err != nil {
		return nil, err
	}

	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	plan, err := client.PrepareApply(ctx.Credential.AccessToken, sourceManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare cloud source apply: %w", err)
	}
	for _, warning := range plan.Warnings() {
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: "+warning)
	}

	yes, _ := cmd.Flags().GetBool("yes")
	if err := approveCloudSourceUpdate(cmd, ctx, plan, doc, yes); err != nil {
		return nil, err
	}

	result, err := client.ApplyPrepared(ctx.Credential.AccessToken, plan)
	if err != nil {
		return nil, fmt.Errorf("failed to apply cloud source: %w", err)
	}
	if yes && !plan.IsCreate() {
		fmt.Fprint(cmd.ErrOrStderr(), appliedSideEffectNotes(plan))
	}
	fmt.Fprintf(humanOut(cmd), "cloud-source/%s %s\n",
		terminal.Sanitize(sourceManifest.Metadata.Name), terminal.Sanitize(result.Action))
	if plan.CreatesDraft() {
		fmt.Fprint(humanOut(cmd), draftNextLine(doc, result.ID))
	}
	return namedApplyResult(result, "cloud-source", result.ID), nil
}

// draftNextLine is the Next: line after doc created cloud source id as a
// draft.
func draftNextLine(doc manifestDoc, id int) string {
	return terminal.Sanitize(fmt.Sprintf("Next: remove spec.state from %s, add any secrets the source needs, "+
		"and run 'qctl apply -f %s'; then turn it on with 'qctl enable cloud-source %d'\n",
		doc.Source, cmdutil.ShellArg(doc.Path), id))
}

// approveCloudSourceUpdate returns nil for a create or an update with no
// changes. For any other update, yes writes the stale-export warning to
// stderr; without yes, -o json and -o yaml are refused and anything else goes
// through confirmCloudSourceChanges.
func approveCloudSourceUpdate(cmd *cobra.Command, ctx *cmdutil.CommandContext,
	plan *cloud_sources.ApplyPlan, doc manifestDoc, yes bool) error {
	if plan.IsCreate() || len(plan.Changes()) == 0 {
		return nil
	}
	if err := refuseUnconfirmedStructuredOutput(cmd, doc, yes); err != nil {
		return err
	}
	if yes {
		fmt.Fprint(cmd.ErrOrStderr(), staleExportWarning(plan))
		return nil
	}
	return confirmCloudSourceChanges(humanOut(cmd), cmd.ErrOrStderr(), ctx, plan, doc)
}

// confirmCloudSourceChanges prints the live identity, the stale-export
// warning, the change list and the side effects of plan's update, then asks.
// A decline returns errDocumentDeclined.
func confirmCloudSourceChanges(out, promptOut io.Writer, ctx *cmdutil.CommandContext,
	plan *cloud_sources.ApplyPlan, doc manifestDoc) error {
	existing := plan.Existing()
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = unknownOrganizationName
	}
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), ctx.OrganizationID)
	fmt.Fprintf(out, "Cloud source : %s (id: %d)\n", terminal.Sanitize(valueOf(existing.Name)), valueOf(existing.ID))
	fmt.Fprintf(out, "Table        : %s (id: %d)\n", terminal.Sanitize(valueOf(existing.DatasetName)), valueOf(existing.DatasetID))
	fmt.Fprintf(out, "File         : %s\n\n", doc.Source)
	if warning := staleExportWarning(plan); warning != "" {
		fmt.Fprint(out, warning+"\n")
	}
	fmt.Fprintln(out, "Changes to apply:")
	fmt.Fprint(out, cloud_sources.FormatChanges(plan.Changes(), "  "))
	fmt.Fprintln(out)
	if effects := sideEffectNotes(plan); effects != "" {
		fmt.Fprint(out, effects+"\n")
	}

	if !cmdutil.CanPrompt() {
		return qerrors.NewBadUsagef("confirmation required for %s: re-run with --yes (-y) to apply without prompting", doc.Source)
	}
	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Apply these cloud source changes?")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintf(out, "Skipping %s\n\n", doc.Source)
		return errDocumentDeclined
	}
	fmt.Fprintf(out, "Updating cloud source %s...\n", terminal.Sanitize(valueOf(existing.Name)))
	return nil
}

// staleExportWarning is one line naming both versions when plan's manifest was
// exported from an older version of the cloud source, and "" otherwise.
func staleExportWarning(plan *cloud_sources.ApplyPlan) string {
	exported, live, ok := plan.StaleExport()
	if !ok {
		return ""
	}
	return fmt.Sprintf("Warning: cloud source %s changed since this manifest was exported (version %d, now %d); "+
		"a change this apply makes may undo an edit made since then.\n",
		terminal.Sanitize(valueOf(plan.Existing().Name)), exported, live)
}

// sideEffectNotes lists plan.SideEffects as the side effects of the update
// about to be sent, or is "" when there are none.
func sideEffectNotes(plan *cloud_sources.ApplyPlan) string {
	return listSideEffects(plan, "Side effects of updating cloud source %s:\n")
}

// appliedSideEffectNotes lists plan.SideEffects as what the server did when it
// accepted the update, or is "" when there are none.
func appliedSideEffectNotes(plan *cloud_sources.ApplyPlan) string {
	return listSideEffects(plan, "The server acted on this update of cloud source %s before it answered:\n")
}

// listSideEffects is header, formatted with the cloud source's name, followed
// by one line per plan.SideEffects entry, or "" when there are none.
func listSideEffects(plan *cloud_sources.ApplyPlan, header string) string {
	effects := plan.SideEffects()
	if len(effects) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, header, terminal.Sanitize(valueOf(plan.Existing().Name)))
	for _, effect := range effects {
		fmt.Fprintf(&b, "  - %s\n", effect)
	}
	return b.String()
}

// valueOf returns *p, or the zero value when p is nil.
func valueOf[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func parseCloudSourceManifest(data []byte) (*cloud_sources.CloudSourceManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}

	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "CloudSource" {
		return nil, qerrors.NewBadUsagef("expected kind 'CloudSource', got '%s'", generic.Kind)
	}
	if err := cloud_sources.CheckRenamedKeys(generic.Spec); err != nil {
		return nil, err
	}

	var manifest cloud_sources.CloudSourceManifest
	if err := pkgmanifest.StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// processCloudSourceEnv applies expandPointer to each of
// cloud_sources.CredentialFields in m's spec.
func processCloudSourceEnv(m *cloud_sources.CloudSourceManifest) {
	for _, field := range cloud_sources.CredentialFields(&m.Spec.CloudSourceFields) {
		expandPointer(field)
	}
}

func expandPointer(field **string) {
	if field == nil || *field == nil {
		return
	}
	expanded := expandEnvVars(**field)
	if expanded == "" {
		*field = nil
		return
	}
	*field = &expanded
}

// resolveSettingsModelID fills in spec.settings_model_id from the table
// spec.dataset_id names, and refuses a supplied one that belongs to a different
// table.
//
// It reads the table on EVERY apply, not only when the field is absent, because
// the check and the fill need the same fact -- which settings model
// spec.dataset_id actually owns -- and there is no cheaper source of it.
//
// The check is not a new rule; it closes a live silent-wrong-table bug. The
// create route drops dataset_id via ignore_keys
// (atlas _create_new_data_source_model_async,
// capi/capi/views/data_source_view.py) and, when settings_model_id is present,
// derives dataset_id from THAT (get_dataset_id_for_settings_model_id_async). So
// a manifest pairing dataset_id: 7 with another table's settings_model_id
// creates the source in the settings model's table, while qctl's own
// PrepareApply existence check looked in table 7 -- a create that lands
// somewhere the manifest does not name, and that the next apply does not find.
//
// Cross-domain composition lives at the command layer here, as it does in
// internal/commands/rows: internal/cloud_sources does not import
// internal/datasets.
func resolveSettingsModelID(ctx *cmdutil.CommandContext, m *cloud_sources.CloudSourceManifest) error {
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	table, err := dsClient.GetDataset(ctx.Credential.AccessToken, m.Spec.DatasetID)
	if err != nil {
		return fmt.Errorf("failed to read the table named by spec.dataset_id (%d): %w", m.Spec.DatasetID, err)
	}

	// api.DataSetSchemaFull.SettingsModel is a non-pointer struct whose ID is a
	// non-pointer int, so a response body that omits settings_model -- or sends
	// an id of 0 -- decodes silently to the zero value. Filling that in would
	// post settings_model_id: 0 and land in the very
	// get_dataset_id_for_settings_model_id(0) branch this function exists to
	// avoid; on the supplied-value path it would instead produce the nonsense
	// refusal "table \"orders\" owns settings model 0". Exit 1, not 2: the
	// manifest is fine and the user has nothing to correct -- the server
	// answered with something qctl cannot act on -- which is the same reading
	// as PrepareApply's "settings_model_id was not resolved" guard.
	owned := table.SettingsModel.ID
	if owned <= 0 {
		return fmt.Errorf("table %d (%q) reported no settings model", m.Spec.DatasetID, table.Name)
	}
	if m.Spec.SettingsModelID == nil {
		m.Spec.SettingsModelID = &owned
		return nil
	}
	if *m.Spec.SettingsModelID != owned {
		return qerrors.NewBadUsagef(
			"spec.settings_model_id %d does not belong to spec.dataset_id %d (table %q owns settings model %d).\n"+
				"The server derives the target table from settings_model_id, so this manifest would create the\n"+
				"cloud source in a different table than it names.\n"+
				"Drop spec.settings_model_id and let qctl fill it in.",
			*m.Spec.SettingsModelID, m.Spec.DatasetID, table.Name, owned)
	}
	return nil
}
