package copycmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	toTableFlag   = "to-table"
	toTableIDFlag = "to-table-id"
	stateFlag     = "state"
)

// NewCloudSourceCommand creates the 'copy cloud-source' command.
func NewCloudSourceCommand() *cobra.Command {
	var fields []string
	cmd := &cobra.Command{
		Use:     "cloud-source <id>",
		Aliases: []string{"data-source"},
		Short:   "Copy a cloud source into a new source",
		Long: `Copy a cloud source into the same table or another existing table.

Secrets and URLs are not inherited. --secrets-file supplies them as a YAML
mapping of field names to values ("-" reads stdin); a value of exactly ${VAR}
is read from the environment. Quote a value that starts with *, & or !, holds
" #", or is null or ~: YAML reads those as an alias, an anchor, a tag, a
comment or null. The copy keeps the source's s3_access_key unless the file
names one. An active or disabled copy that lacks a secret the source holds is
refused; --state draft saves it without them, and the plan and the default
result name the apply and enable commands that complete it.

--set key=value changes a non-credential field and may be repeated; hyphens in
field names are accepted. An active source needs --set to change its location
or a file-selecting field, unless it is a simple_url source, whose URL is not
inherited. The server checks whether the copy would read the same files as an
active source. Deleted sources, upload sources (qluster_ui), and composite
sources are refused.

--to-table names the target table; --to-table-id selects it by ID. Without
either, the copy stays in the source's table. The name defaults to the server's
suggestion, and the state defaults to the original source's state.

The plan names the credentials supplied and missing, never their values.
--dry-run prints the plan and copies nothing; -o json|yaml prints the template
with credential fields removed and the request with credential values masked.
Otherwise, confirmation is required; --yes skips the prompt and is required
when stdin is not an interactive terminal.

After copying, -o json|yaml prints the server's response with secret fields
masked unless --allow-plaintext-secrets is set, and -o name prints
cloud-source/<new id>.

Examples:
  qctl copy cloud-source 12 --set s3-prefix=eu/ --secrets-file secrets.yaml --yes
  qctl copy cloud-source 12 --to-table orders_eu --set s3-prefix=eu/ --state draft --yes
  qctl copy cloud-source 12 --set s3-prefix=eu/ --secrets-file secrets.yaml --dry-run -o json`,
		Annotations: output.WithNameKind(cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("cloud-source"), "table", "json", "yaml", "name"), "cloud-source"), "cloud-source"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCopyCloudSource(cmd, args, fields)
		},
	}
	cmd.Flags().String(toTableFlag, "", "Target table name (exact, case-sensitive; default: the source's table)")
	cmdutil.RefInt(cmd.Flags(), toTableIDFlag, "table", "Target table ID (default: the source's table)")
	cmd.Flags().String(nameFlag, "", "Name of the new source (default: the name the server suggests)")
	cmd.Flags().String(stateFlag, "", "State of the new source: active, draft or disabled (default: the original's state)")
	bindCloudSourceSetFlag(cmd, &fields)
	cmd.Flags().String(secretsFileFlag, "",
		`YAML mapping of the copy's credentials and URL by field name ("-" reads stdin)`)
	cmd.Flags().Bool(dryRunFlag, false, "Build the request and copy nothing")
	cmd.Flags().BoolP(yesFlag, "y", false, "Copy without prompting")
	return cmd
}

type copyCloudSourceFlags struct {
	id         int
	tableID    int
	tableName  *string
	overrides  cloud_sources.CopyOverrides
	dryRun     bool
	yes        bool
	stateGiven bool
}

func readCopyCloudSourceFlags(cmd *cobra.Command, args, fields []string) (copyCloudSourceFlags, error) {
	var flags copyCloudSourceFlags
	ids, err := cmdutil.ParseIntArgs(args, "cloud source id")
	if err != nil {
		return flags, err
	}
	flags.id = ids[0]
	if flags.id <= 0 {
		return flags, qerrors.NewBadUsage("cloud source id must be a positive integer")
	}
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{toTableFlag, toTableIDFlag}, []string{dryRunFlag, yesFlag}); err != nil {
		return flags, err
	}
	if flags.tableID, _, err = cmdutil.PositiveIDFlag(cmd, toTableIDFlag); err != nil {
		return flags, err
	}
	if flags.tableName, err = nonEmptyStringFlag(cmd, toTableFlag, "name the target table, or use --to-table-id <id>"); err != nil {
		return flags, err
	}
	if flags.overrides.Name, err = nonEmptyStringFlag(cmd, nameFlag, "omit it to use the name the server suggests"); err != nil {
		return flags, err
	}
	if cmd.Flags().Changed(stateFlag) {
		flags.stateGiven = true
		state, _ := cmd.Flags().GetString(stateFlag)
		flags.overrides.State = api.DataSourceState(state)
		switch flags.overrides.State {
		case api.DataSourceStateActive, api.DataSourceStateDraft, api.DataSourceStateDisabled:
		default:
			return flags, qerrors.NewBadUsagef("--state must be active, draft or disabled, got %q", state)
		}
	}
	if flags.overrides.Fields, err = cloud_sources.ParseCopyFields(fields); err != nil {
		return flags, err
	}
	flags.dryRun, _ = cmd.Flags().GetBool(dryRunFlag)
	flags.yes, _ = cmd.Flags().GetBool(yesFlag)
	format, _ := cmd.Flags().GetString("output")
	if flags.dryRun && format == string(output.FormatName) {
		return flags, qerrors.NewBadUsage("--dry-run creates no cloud source, so -o name has no cloud-source/<id> to print; use -o json or yaml")
	}
	if cmd.Flags().Changed(secretsFileFlag) {
		path, _ := cmd.Flags().GetString(secretsFileFlag)
		if flags.overrides.Secrets, err = readSecretsFile(cmd, path); err != nil {
			return flags, err
		}
	}
	return flags, nil
}

func requireCopyableCloudSource(source *cloud_sources.CloudSourceTiny, fields map[string]json.RawMessage) error {
	if err := cloud_sources.RequireNotDeleted(source, "copied"); err != nil {
		return err
	}
	switch source.DataSourceType {
	case api.DataSourceTypeQlusterUI, api.DataSourceTypeComposite:
		return qerrors.NewBadUsagef("cloud source %d (%s) has type %s, so it cannot be copied", source.ID, source.Name, source.DataSourceType)
	}
	if source.State == api.DataSourceStateActive && len(fields) == 0 && source.DataSourceType != api.DataSourceTypeSimpleURL {
		return qerrors.NewBadUsagef("cloud source %d (%s) is active: use --set to change its location or a file-selecting field so the copy reads different files", source.ID, source.Name)
	}
	return nil
}

func resolveCopyCloudSourceTarget(ctx *cmdutil.CommandContext, flags copyCloudSourceFlags, sourceTableID int) (int, error) {
	if flags.tableName != nil {
		table, err := cmdutil.ResolveTableByName(ctx, *flags.tableName, "--"+toTableIDFlag)
		return table.ID, err
	}
	if flags.tableID != 0 {
		return flags.tableID, nil
	}
	return sourceTableID, nil
}

func runCopyCloudSource(cmd *cobra.Command, args, fields []string) error {
	flags, err := readCopyCloudSourceFlags(cmd, args, fields)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create cloud sources client: %w", err)
	}
	token := ctx.Credential.AccessToken
	source, err := client.GetCloudSourceTiny(token, flags.id)
	if err != nil {
		return err
	}
	if err := requireCopyableCloudSource(source, flags.overrides.Fields); err != nil {
		return err
	}
	tableID, err := resolveCopyCloudSourceTarget(ctx, flags, source.DatasetID)
	if err != nil {
		return err
	}
	template, rawTemplate, err := client.CopyTemplate(token, tableID, flags.id)
	if err != nil {
		return err
	}
	if flags.overrides.State == "" {
		flags.overrides.State = source.State
	}
	plan, err := prepareCloudSourceCopyPlan(client, token, source, template, rawTemplate, tableID, flags)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if err := writeCloudSourceCopyPlanFromCmd(cmd, format, plan); err != nil {
		return err
	}
	if err := refuseMissingCopySecrets(source, template.SecretsToResupply, flags.overrides); err != nil {
		return err
	}
	if flags.dryRun {
		return writeCloudSourceCopyDryRun(cmd.OutOrStdout(), format, plan)
	}
	if err := confirmCloudSourceCopy(cmd, flags, source, template.Name, tableID); err != nil {
		return err
	}
	result, raw, err := client.Copy(token, flags.id, plan.body)
	if err != nil {
		return err
	}
	return writeCloudSourceCopyResult(cmd, format, result, raw)
}

// confirmCloudSourceCopy asks before copying source into table tableID under
// the name --name gives, else suggested; --yes skips the question.
func confirmCloudSourceCopy(cmd *cobra.Command, flags copyCloudSourceFlags, source *cloud_sources.CloudSourceTiny,
	suggested string, tableID int) error {
	name := suggested
	if flags.overrides.Name != nil {
		name = *flags.overrides.Name
	}
	phrase := fmt.Sprintf("copy cloud source %d (%s) to a new source %s in table %d", source.ID, source.Name, strconv.Quote(name), tableID)
	return cmdutil.Confirm(cmd.ErrOrStderr(), flags.yes,
		"confirmation required but stdin is not an interactive terminal: pass --yes to "+phrase,
		"Copy"+phrase[len("copy"):]+"?")
}

func writeCloudSourceCopyPlanFromCmd(cmd *cobra.Command, format string, plan *cloudSourceCopyPlan) error {
	w := cmd.OutOrStdout()
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML, output.FormatName:
		w = cmd.ErrOrStderr()
	}
	return writeCloudSourceCopyPlan(w, plan)
}

// writeCloudSourceCopyDryRun writes what --dry-run prints after the plan:
// under -o json|yaml cloudSourceCopyDryRunDocument, otherwise a line saying
// nothing was copied.
func writeCloudSourceCopyDryRun(w io.Writer, format string, plan *cloudSourceCopyPlan) error {
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		document, err := cloudSourceCopyDryRunDocument(plan)
		if err != nil {
			return err
		}
		return output.WriteRaw(w, format, document)
	}
	_, err := fmt.Fprintln(w, "Dry run: nothing was copied.")
	return err
}

// writeCloudSourceCopyResult writes the copy's response: under -o json|yaml
// raw, through cmd's printer, which masks secret fields unless
// --allow-plaintext-secrets is set; under -o name cloud-source/<new id>;
// otherwise the new source, then for a draft that lacks secrets the commands
// that resupply them and activate it, else any secrets it lacks and its
// describe command.
func writeCloudSourceCopyResult(cmd *cobra.Command, format string, result *api.DataSourceModelCopyResponseSchema, raw []byte) error {
	w := cmd.OutOrStdout()
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var response any
		if err := dec.Decode(&response); err != nil {
			return fmt.Errorf("failed to decode cloud source copy response: %w", err)
		}
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return err
		}
		return printer.Print(response)
	case output.FormatName:
		_, err := fmt.Fprintf(w, "cloud-source/%d\n", result.ID)
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Created cloud source %d (%s).\n", result.ID, terminal.Sanitize(result.Name))
	if lacks := result.SecretsToResupply; len(lacks) > 0 && result.State == api.DataSourceStateDraft {
		writeActivateNext(&b, result.ID, lacks)
	} else {
		if len(lacks) > 0 {
			fmt.Fprintf(&b, "Secrets to resupply: %s\n", planNames(lacks))
		}
		fmt.Fprintf(&b, "Next: qctl describe cloud-source %d\n", result.ID)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
