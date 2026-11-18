package submit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTableKindsCommand creates the 'submit table-kinds' command
func NewTableKindsCommand() *cobra.Command {
	var (
		yes       bool
		filePaths []string
	)

	cmd := &cobra.Command{
		Use:     "table-kinds",
		Aliases: []string{"table-kind"},
		Short:   "Submit table kind definitions from TOML or YAML files",
		Long: `Submit table kind definitions from TOML or YAML config files.

The files are parsed server-side. Each file needs a problem_domain section
with a name and optional slug, description, and a concepts list of standard
field slugs. Optional concepts entries define standard fields by name, with
optional slug, description, aliases, and expected_field_type. In TOML, use
[problem_domain] and [[concepts]]; in YAML, use a problem_domain mapping and
concepts list. The file format keeps the API's word "concepts". An entry is
linked only when its slug appears in problem_domain.concepts.

Submitting an existing slug updates the table kind and matching standard fields.
Non-empty values replace existing values; listed links are added. Omitting a
link or value does not remove it. See qctl explain table-kind.

Supported file types: .toml, .yaml, .yml

File constraints:
  - Maximum file size: 500,000 characters

Under -o json or yaml, {created, table_kind} is printed, table_kind being the
manifest 'qctl get table-kind -o yaml' prints; several files print one List.
-o name prints table-kind/<uuid>. The prose goes to stderr.

Examples:
  # Submit a single TOML file
  qctl submit table-kinds -f car-policy-bordereau.toml

  # Submit multiple files
  qctl submit table-kinds -f kinds1.toml -f kinds2.yaml

  # Skip confirmation prompt
  qctl submit table-kinds -f car-policy-bordereau.toml --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("table-kind"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return qerrors.NewBadUsage("at least one file is required (-f or --filename)")
			}

			// Validate all file extensions
			for _, fp := range filePaths {
				if _, err := dataset_kinds.FormatForFile(fp); err != nil {
					return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
				}
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			orgID, orgName := getTableKindsOrgContext(ctx)

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken

			w := cmdutil.NewResultWriter(cmd)
			var results []any
			var names []string
			var runErr error
			for _, fp := range filePaths {
				result, err := importTableKindFile(w.Human, cmd.ErrOrStderr(), client, token, fp, orgID, orgName, yes)
				if err != nil {
					runErr = err
					break
				}
				if result != nil {
					results = append(results, result)
					names = append(names, "table-kind/"+result.TableKind.Status.ID.String())
				}
			}

			writeErr := w.WriteRun(len(filePaths), results, names...)
			if runErr != nil {
				return runErr
			}
			return writeErr
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Path to the TOML or YAML config file")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}

func getTableKindsOrgContext(ctx *cmdutil.CommandContext) (string, string) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = "Unknown"
	}
	return orgID, orgName
}

// submittedTableKind is the result document of one submitted file.
type submittedTableKind struct {
	Created   bool                    `json:"created"`
	TableKind *dataset_kinds.Manifest `json:"table_kind"`
}

// importTableKindFile submits one file and returns its result document, or
// nil when the prompt was declined.
func importTableKindFile(out, promptOut io.Writer, client *dataset_kinds.Client, token, filePath, orgID, orgName string, yes bool) (*submittedTableKind, error) {
	fileText, err := readAndValidateTableKindFile(filePath)
	if err != nil {
		return nil, err
	}

	format, err := dataset_kinds.FormatForFile(filePath)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	}

	displayTableKindImportConfirmation(out, filePath, orgID, orgName, utf8.RuneCountInString(fileText))

	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Import table kind from this file?")
		if err != nil {
			return nil, err
		}
		if !confirmed {
			fmt.Fprintf(out, "Skipping %s\n\n", filePath)
			return nil, nil
		}
	}

	fmt.Fprintf(out, "Importing table kind from %s...\n", filepath.Base(filePath))
	result, err := client.ImportFromConfig(token, fileText, format)
	if err != nil {
		if strings.Contains(err.Error(), "unknown keys field_kinds") {
			return nil, fmt.Errorf("%w\n\nuse [[concepts]] entries (one per standard field) and list their slugs in [problem_domain].concepts", err)
		}
		return nil, fmt.Errorf("%w", err)
	}

	displayTableKindImportResult(out, result)
	return &submittedTableKind{Created: result.Created, TableKind: dataset_kinds.FromAPI(result.Kind)}, nil
}

func readAndValidateTableKindFile(filePath string) (string, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	fileText := string(fileBytes)
	if length := utf8.RuneCountInString(fileText); length > maxFileTextLength {
		return "", qerrors.NewBadUsagef("file %s exceeds maximum size: %d characters (limit: %d)",
			filePath, length, maxFileTextLength)
	}

	return fileText, nil
}

func displayTableKindImportConfirmation(out io.Writer, filePath, orgID, orgName string, fileLength int) {
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), terminal.Sanitize(orgID))
	fmt.Fprintf(out, "File         : %s\n", filepath.Base(filePath))
	fmt.Fprintf(out, "Size         : %d characters (limit %d)\n", fileLength, maxFileTextLength)
	fmt.Fprintln(out)
}

// displayTableKindImportResult leads with the verb: the endpoint upserts, and a
// submission that quietly updated an existing slug looks identical otherwise.
func displayTableKindImportResult(out io.Writer, result *dataset_kinds.ImportResult) {
	verb := "Updated"
	if result.Created {
		verb = "Created"
	}

	kind := result.Kind
	fmt.Fprintf(out, "\n%s table kind: %s (%s)\n", verb, terminal.Sanitize(kind.Name), terminal.Sanitize(kind.Slug))
	conceptCount := 0
	if kind.Concepts != nil {
		conceptCount = len(*kind.Concepts)
	}
	fmt.Fprintf(out, "Standard fields: %d\n", conceptCount)
	fmt.Fprintln(out)
}
