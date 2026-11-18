package alerts

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
)

// KeyChoiceForm names the two key fields in a message: as flags or as
// manifest keys.
type KeyChoiceForm struct {
	Columns      string // the key columns field
	ColumnsUsage string // the key columns field with a placeholder value
	None         string // the keyless field, set
}

// The two KeyChoiceForm spellings.
var (
	KeyChoiceFlags    = KeyChoiceForm{Columns: "--key-columns", ColumnsUsage: "--key-columns <a,b>", None: "--no-key-columns"}
	KeyChoiceManifest = KeyChoiceForm{Columns: "spec.key_columns", ColumnsUsage: "spec.key_columns: [a, b]", None: "spec.no_key_columns: true"}
)

// MigrationRead is one migration-details read of a table: Detail, or the Err
// the read returned.
type MigrationRead struct {
	Detail *datasets.TableSchemaDetail
	Err    error
}

// ReadMigration reads table datasetID's migration details.
func ReadMigration(ctx *cmdutil.CommandContext, datasetID int) *MigrationRead {
	detail, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetSchemaDetails(ctx.Credential.AccessToken, datasetID)
	return &MigrationRead{Detail: detail, Err: err}
}

// KeyChoicePreflight reads table datasetID's migration details for an
// apply_migration spec and returns spec.CheckKeyStep's refusal. A failed read
// writes a warning to warn and is returned with no error. Any other action
// reads nothing and returns nil.
func KeyChoicePreflight(ctx *cmdutil.CommandContext, warn io.Writer, datasetID int, spec AlertResolutionSpec,
	form KeyChoiceForm,
) (*MigrationRead, error) {
	if spec.Action != string(api.AlertActionApplyMigration) {
		return nil, nil
	}
	read := ReadMigration(ctx, datasetID)
	if read.Err != nil {
		line, _, _ := strings.Cut(read.Err.Error(), "\n")
		fmt.Fprintf(warn, "Warning: could not read table %d's key step, so its key columns are left to the server to check: %s\n",
			datasetID, terminal.Sanitize(line))
		return read, nil
	}
	return read, spec.CheckKeyStep(read.Detail, form)
}

// CheckKeyStep refuses with exit 2 an apply_migration key choice that
// detail's key step refuses: neither field at the "required" step, either
// field at a null step, or no_key_columns at the "optional" step. A nil
// detail, and any other action, passes.
func (s AlertResolutionSpec) CheckKeyStep(detail *datasets.TableSchemaDetail, form KeyChoiceForm) error {
	if s.Action != string(api.AlertActionApplyMigration) || detail == nil {
		return nil
	}
	keys, keyless := len(s.KeyColumns) > 0, s.noKeyColumns()
	switch {
	case detail.KeyStep == nil:
		if !keys && !keyless {
			return nil
		}
		sent := form.Columns
		if keyless {
			sent = form.None
		}
		return qerrors.NewBadUsagef(
			"table %d takes no key column choice on this migration (its key columns: %s): key columns lock once a migration "+
				"of the table is applied or confirmed, and only a mirror or append table has them; drop %s",
			detail.DatasetID, joinKeyColumns(detail.KeyColumns), sent)
	case *detail.KeyStep == api.Required && !keys && !keyless:
		return qerrors.NewBadUsagef(
			"table %d has no key columns yet, and its first migration must choose them: add %s, or %s if the table has none\n%s%s",
			detail.DatasetID, form.ColumnsUsage, form.None,
			strings.TrimRight(datasets.FormatKeyColumnCandidates(detail.KeyColumnCandidates), "\n"), previousKeyChoiceHint(detail, form))
	case *detail.KeyStep == api.Optional && keyless:
		return qerrors.NewBadUsagef(
			"table %d already has key columns (%s), and %s cannot remove them: clear them first by applying the table's "+
				"manifest with spec.settings_model.key_columns: null, replace them with %s, or drop %s to keep them",
			detail.DatasetID, joinKeyColumns(detail.KeyColumns), form.None, form.ColumnsUsage, form.None)
	}
	return nil
}

func previousKeyChoiceHint(detail *datasets.TableSchemaDetail, form KeyChoiceForm) string {
	if detail.PreselectPreviousKeyColumns == nil || !*detail.PreselectPreviousKeyColumns ||
		detail.PreviousKeyColumns == nil || len(*detail.PreviousKeyColumns) == 0 {
		return ""
	}
	return fmt.Sprintf("\nPrevious key columns (%s) can be reused: set %s explicitly to those columns.",
		joinKeyColumns(detail.PreviousKeyColumns), form.Columns)
}

// KeyOutcome is the line an apply_migration confirmation prints about the
// table's key columns, "" when the apply leaves them alone. A nil detail is a
// key step that could not be read.
func (s AlertResolutionSpec) KeyOutcome(detail *datasets.TableSchemaDetail) string {
	keys, keyless := len(s.KeyColumns) > 0, s.noKeyColumns()
	const locks = " Applying locks the key columns; they cannot be changed afterwards.\n"
	switch {
	case detail == nil && keys:
		return "Key columns: sets them to " + joinKeyColumns(&s.KeyColumns) + ", if the table is at its key step\n"
	case detail == nil && keyless:
		return "Key columns: records the table as keyless, if it is at its key step\n"
	case detail == nil || detail.KeyStep == nil:
		return ""
	case keys && *detail.KeyStep == api.Optional:
		return "Key columns: replaces " + joinKeyColumns(detail.KeyColumns) + " with " + joinKeyColumns(&s.KeyColumns) + "." + locks
	case keys:
		return "Key columns: sets them to " + joinKeyColumns(&s.KeyColumns) + "." + locks
	case keyless:
		return "Key columns: none; the table is recorded as keyless." + locks
	case *detail.KeyStep == api.Optional:
		return "Key columns: keeps " + joinKeyColumns(detail.KeyColumns) + "." + locks
	}
	return ""
}

// ApplyMigrationKeyArgs is what a suggested apply_migration command on table
// datasetID adds for its key step: an argument suffix and indented note
// lines. known is false when the key step was not read.
func ApplyMigrationKeyArgs(datasetID int, step *datasets.KeyStep, known bool) (suffix, note string) {
	switch {
	case !known:
		return "", "    a mirror or append table's first migration also takes --key-columns <a,b> or\n" +
			fmt.Sprintf("    --no-key-columns; qctl describe table-schema %d shows whether it needs them\n", datasetID)
	case step == nil:
		return "", ""
	case *step == api.Required:
		return " --key-columns <a,b>", "    or --no-key-columns if the table has none; key candidates:\n" +
			fmt.Sprintf("    qctl describe table-schema %d\n", datasetID)
	default:
		return "", "    keeps the table's key columns; --key-columns <a,b> replaces them\n"
	}
}

func joinKeyColumns(columns *[]string) string {
	if columns == nil || len(*columns) == 0 {
		return "none"
	}
	return terminal.Sanitize(strings.Join(*columns, ", "))
}

// skeletonKeyStep is the key step an apply_migration skeleton is written for.
type skeletonKeyStep int

const (
	keyStepUnread skeletonKeyStep = iota // not apply_migration, or not read
	keyStepNone
	keyStepRequired
	keyStepOptional
)

// keyFields are the request fields a read key step decides.
var keyFields = map[string]bool{"key_columns": true, "no_key_columns": true}

func migrationKeyStep(action string, detail *datasets.TableSchemaDetail) skeletonKeyStep {
	switch {
	case action != string(api.AlertActionApplyMigration) || detail == nil:
		return keyStepUnread
	case detail.KeyStep == nil:
		return keyStepNone
	case *detail.KeyStep == api.Required:
		return keyStepRequired
	case *detail.KeyStep == api.Optional:
		return keyStepOptional
	}
	return keyStepUnread
}

// comment is the skeleton's comment lines about the key step.
func (k skeletonKeyStep) comment(detail *datasets.TableSchemaDetail) string {
	switch k {
	case keyStepNone:
		return fmt.Sprintf("# key_step: none — this migration takes no key_columns or no_key_columns (key columns: %s)\n",
			joinKeyColumns(detail.KeyColumns))
	case keyStepRequired:
		return "# key_step: required — the table has no key columns yet: set key_columns, or delete it and\n" +
			"# uncomment no_key_columns: true if the table has none. Applying locks the choice.\n" +
			output.YAMLComment(datasets.FormatKeyColumnCandidates(detail.KeyColumnCandidates))
	case keyStepOptional:
		return fmt.Sprintf("# key_step: optional — key columns %s are kept unless key_columns replaces them;\n",
			joinKeyColumns(detail.KeyColumns)) +
			"# applying locks them\n" +
			output.YAMLComment(datasets.FormatKeyColumnCandidates(detail.KeyColumnCandidates))
	}
	return ""
}

// fields is the skeleton's spec lines for the key fields; "" leaves them to
// the action's optional fields.
func (k skeletonKeyStep) fields() string {
	switch k {
	case keyStepRequired:
		return "  key_columns:\n  # no_key_columns: true\n"
	case keyStepOptional:
		return "  # key_columns:\n"
	}
	return ""
}
