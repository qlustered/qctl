package submit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const maxFileTextLength = 500_000

// versionConflictHint tells the author how to move past a locked revision.
const versionConflictHint = "Bump 'release' in RuleMetadata to create a new revision."

// forceSkipNote warns that --force skips (never overwrites) locked revisions.
// The backend omits force-skipped rules from both added and not_changed, so
// they cannot be listed individually here.
const forceSkipNote = "Note: --force does not overwrite enabled/disabled revisions whose code changed — " +
	"such rules were skipped and are not listed above. Bump 'release' in RuleMetadata to create a new revision."

// checkOnlyNote closes every --check report.
const checkOnlyNote = "Check only: nothing was submitted."

// rulesSubmitOptions carries the per-invocation flags down the submit path.
type rulesSubmitOptions struct {
	force   bool
	yes     bool
	verbose bool // verbosity >= 1: ask the backend for full conflict diffs
}

// NewRulesCommand creates the 'submit rules' command
func NewRulesCommand() *cobra.Command {
	var (
		force     bool
		yes       bool
		check     bool
		filePaths []string
	)

	cmd := &cobra.Command{
		Use:     "rules",
		Aliases: []string{"rule"},
		Short:   "Submit rule definitions from Python files",
		Long: `Submit rule definitions from Python files into the org rule library.

The Python files are parsed server-side, and only the extracted rule
versions are kept. Submitting and --check need the ops role (Qluster
operations staff); any other role gets a 403.

Each -f takes one path: a .py file, or a directory, which contributes the
regular .py files directly inside it other than __init__.py, in name
order, as if each had its own -f. Its subdirectories are not searched.
Pass the directory rather than a shell glob: -f rules/*.py leaves every
file after the first as a stray argument, which exits 2.

File constraints:
  - Maximum file size: 500,000 characters

The --force flag skips, instead of rejecting, a rule whose enabled or
disabled revision at the same release has different code.

The --check flag asks the server whether it would accept each file and
what submitting it would do with each rule (added, updated in place,
unchanged, skipped), and writes nothing. A file submit would reject exits
non-zero with the error submit prints, so --check fits CI and pre-commit
hooks.

Under -o json or yaml, the server's answer is printed: {message, added,
not_changed}, each rule a [name, release] pair, or under --check
{message, rules}; several files print one List. The prose goes to stderr.

Examples:
  # Submit a single rule file
  qctl submit rules -f rules.py

  # Submit multiple rule files
  qctl submit rules -f rules1.py -f rules2.py -f rules3.py

  # Submit the .py files directly inside a directory
  qctl submit rules -f rules/

  # Skip locked rules whose code changed instead of rejecting the file
  qctl submit rules -f rules.py --force

  # Ask whether the server would accept the file; write nothing
  qctl submit rules -f rules.py --check

  # Skip confirmation prompt
  qctl submit rules -f rules.py --yes

Submitting rules that need review? Run:
  qctl guide request-review`,
		Annotations: cmdutil.WithFormats(catalog.For("rule"), "table", "json", "yaml"),
		Args:        rulesArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return qerrors.NewBadUsage("at least one file is required (-f or --filename)")
			}

			files, err := expandRulePaths(filePaths)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			orgID, orgName := getRulesOrgContext(ctx)

			// Create rule versions client
			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			opts := rulesSubmitOptions{
				force:   force,
				yes:     yes,
				verbose: ctx.Verbosity >= 1,
			}
			w := cmdutil.NewResultWriter(cmd)

			// Process each file
			var results []any
			var runErr error
			for _, fp := range files {
				var result any
				if check {
					result, err = checkRuleFile(client, token, fp, orgID, orgName, opts, w.Human)
				} else {
					result, err = importRuleFile(client, token, fp, orgID, orgName, opts, w.Human, cmd.ErrOrStderr())
				}
				if err != nil {
					runErr = err
					break
				}
				if result != nil {
					results = append(results, result)
				}
			}

			writeErr := w.WriteRun(len(files), results)
			if runErr != nil {
				return runErr
			}
			return writeErr
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Python rule file, or a directory whose .py files are submitted (not recursive); repeat for more")
	cmd.Flags().BoolVar(&force, "force", false, "Skip, instead of rejecting, rules whose enabled/disabled revision at the same release has different code")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&check, "check", false, "Ask the server whether it would accept the files, and write nothing")

	return cmd
}

// rulesArgs rejects every positional argument. Its message names the fix: each
// file after its own -f, or their directory after one. A "true" or "false"
// among them gets cobra.NoArgs's error instead.
func rulesArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	for _, a := range args {
		if a == "true" || a == "false" {
			return cobra.NoArgs(cmd, args)
		}
	}
	subject, verb, noun := fmt.Sprintf("%q", args[0]), "was", "an argument"
	if len(args) > 1 {
		subject, verb, noun = fmt.Sprintf("%q and %d more", args[0], len(args)-1), "were", "arguments"
	}
	cause := fmt.Sprintf("-f takes one path, so %s %s left over as %s", subject, verb, noun)
	if !cmd.Flags().Changed("filename") {
		cause = fmt.Sprintf("files are passed with -f, but %s %s given as %s", subject, verb, noun)
	}
	// Left untyped: root's cmdutil.TypeArgsErrors prefixes the command path,
	// appends the Usage line and makes it exit 2.
	return fmt.Errorf("%s\n"+
		"Give each file its own -f (-f a.py -f b.py), or pass their directory "+
		"to submit the .py files in it (-f rules/)", cause)
}

// manifestExts are the extensions checkRuleFileExt points at apply -f for.
var manifestExts = map[string]bool{".yaml": true, ".yml": true, ".json": true}

// expandRulePaths returns the files to submit, in order: each of paths, with
// a directory replaced by dirRuleFiles. A path os.Stat fails on is an error;
// any other path must pass checkRuleFileExt.
func expandRulePaths(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", p, err)
		}
		if info.IsDir() {
			found, err := dirRuleFiles(p)
			if err != nil {
				return nil, err
			}
			files = append(files, found...)
			continue
		}
		if err := checkRuleFileExt(p); err != nil {
			return nil, err
		}
		files = append(files, p)
	}
	return files, nil
}

// pythonPackageMarker is the file a directory expansion skips.
const pythonPackageMarker = "__init__.py"

// dirRuleFiles returns the regular files, following symlinks, directly inside
// dir whose names end in .py, other than pythonPackageMarker, in name order.
// A dir with none is a bad-usage error.
func dirRuleFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".py" || e.Name() == pythonPackageMarker {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		return nil, qerrors.NewBadUsagef("directory %s holds no .py file to submit "+
			"(-f reads the .py files directly inside a directory, other than %s, "+
			"and not its subdirectories)", dir, pythonPackageMarker)
	}
	return files, nil
}

// checkRuleFileExt returns a bad-usage error unless path ends in .py. For a
// .yaml, .yml or .json path the error names the apply command.
func checkRuleFileExt(path string) error {
	ext := filepath.Ext(path)
	if ext == ".py" {
		return nil
	}
	msg := path + " is not a .py file (only .py files, or a directory of them, are supported)"
	if manifestExts[strings.ToLower(ext)] {
		msg += "\n\nTo apply a kind: Rule manifest, use:\n  qctl apply -f " + path
	}
	return qerrors.NewBadUsage(msg)
}

// getRulesOrgContext retrieves the current organization ID and name.
func getRulesOrgContext(ctx *cmdutil.CommandContext) (string, string) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = "Unknown"
	}
	return orgID, orgName
}

// importRuleFile handles importing a single Python rule file. It returns the
// server's answer, or nil when the prompt was declined.
func importRuleFile(client *rule_versions.Client, token, filePath, orgID, orgName string, opts rulesSubmitOptions, out, promptOut io.Writer) (any, error) {
	fileText, err := readAndValidateRuleFile(filePath)
	if err != nil {
		return nil, err
	}

	displayRulesImportConfirmation(out, filePath, orgID, orgName, utf8.RuneCountInString(fileText))

	if !opts.yes {
		confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Import rules from this file into the org rule library?")
		if err != nil {
			return nil, err
		}
		if !confirmed {
			fmt.Fprintf(out, "Skipping %s\n\n", filePath)
			return nil, nil
		}
	}

	result, err := submitRuleFile(client, token, filePath, fileText, opts, out)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// readAndValidateRuleFile reads a file and validates its character count.
func readAndValidateRuleFile(filePath string) (string, error) {
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

// displayRulesImportConfirmation prints the import summary for user confirmation.
func displayRulesImportConfirmation(out io.Writer, filePath, orgID, orgName string, fileLength int) {
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), terminal.Sanitize(orgID))
	fmt.Fprintf(out, "File         : %s\n", filepath.Base(filePath))
	fmt.Fprintf(out, "Size         : %d characters (limit %d)\n", fileLength, maxFileTextLength)
	fmt.Fprintln(out)
}

// submitRequest builds the body both submit and --check send for fileText.
func submitRequest(fileText string, opts rulesSubmitOptions) rule_versions.RuleVersionSubmitRequest {
	req := rule_versions.RuleVersionSubmitRequest{
		FileText: fileText,
		Force:    &opts.force,
	}
	if opts.verbose {
		verbose := true
		req.Verbose = &verbose
	}
	return req
}

// submitRuleFile submits a rule file to the server.
func submitRuleFile(client *rule_versions.Client, token, filePath, fileText string, opts rulesSubmitOptions, out io.Writer) (*rule_versions.RuleVersionSubmitResponse, error) {
	fmt.Fprintf(out, "Importing rules from %s...\n", filepath.Base(filePath))
	result, err := client.SubmitRuleVersion(token, submitRequest(fileText, opts))
	if err != nil {
		return nil, renderSubmitError(err, opts.verbose)
	}

	displayRulesImportResult(out, result)
	if opts.force {
		fmt.Fprintln(out, forceSkipNote)
	}
	printReviewBreadcrumbs(client, token, result, out)
	return result, nil
}

// checkRuleFile reports what submitting one rule file does, without
// submitting it, and returns the server's answer.
func checkRuleFile(client *rule_versions.Client, token, filePath, orgID, orgName string, opts rulesSubmitOptions, out io.Writer) (any, error) {
	fileText, err := readAndValidateRuleFile(filePath)
	if err != nil {
		return nil, err
	}

	displayRulesImportConfirmation(out, filePath, orgID, orgName, utf8.RuneCountInString(fileText))
	fmt.Fprintf(out, "Checking rules from %s...\n", filepath.Base(filePath))
	result, err := client.CheckRuleVersion(token, submitRequest(fileText, opts))
	if err != nil {
		return nil, renderSubmitError(err, opts.verbose)
	}

	displayRulesCheckResult(out, result)
	return result, nil
}

// renderSubmitError appends the full conflict diff (verbose runs only) and the
// bump-release hint to ERR_RULE_VERSION_CONFLICT errors; other errors pass
// through unchanged. %w wrapping preserves the exit-code mapping. The
// non-verbose message needs no diff of its own: the backend msg already
// embeds the compact diff.
func renderSubmitError(err error, verbose bool) error {
	var conflict *rule_versions.VersionConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	if verbose && conflict.Diff != "" {
		return fmt.Errorf("%w\n\nFull diff:\n%s\n\n%s", err, conflict.Diff, versionConflictHint)
	}
	return fmt.Errorf("%w\n\n%s", err, versionConflictHint)
}

// displayRulesImportResult prints the import result.
func displayRulesImportResult(out io.Writer, result *rule_versions.RuleVersionSubmitResponse) {
	if result.Message != "" {
		fmt.Fprintf(out, "\n%s\n\n", terminal.Sanitize(result.Message))
	}

	if len(result.Added) > 0 {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION")

		for _, rule := range result.Added {
			if len(rule) == 2 {
				fmt.Fprintf(w, "%s\t%s\n", terminal.Sanitize(rule[0]), terminal.Sanitize(rule[1]))
			}
		}

		w.Flush()
	}

	if len(result.NotChanged) > 0 {
		fmt.Fprintf(out, "Unchanged rule(s):\n")
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION")

		for _, rule := range result.NotChanged {
			if len(rule) == 2 {
				fmt.Fprintf(w, "%s\t%s\n", terminal.Sanitize(rule[0]), terminal.Sanitize(rule[1]))
			}
		}

		w.Flush()
		fmt.Fprintln(out)
	}
}

// checkActionLabels spells out the plan actions whose wire value alone
// misreads; the rest print as sent.
var checkActionLabels = map[api.RulePlanAction]string{
	api.RulePlanActionUpdated: "updated in place",
}

// displayRulesCheckResult prints the check result: the server's summary,
// one row per rule with a REASON column when any rule carries one, and
// checkOnlyNote.
func displayRulesCheckResult(out io.Writer, result *api.RuleRevisionCheckResponseSchema) {
	if result.Message != "" {
		fmt.Fprintf(out, "\n%s\n\n", terminal.Sanitize(result.Message))
	}

	if len(result.Rules) > 0 {
		withReason := false
		for _, rule := range result.Rules {
			if rule.Reason != nil && *rule.Reason != "" {
				withReason = true
			}
		}

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		if withReason {
			fmt.Fprintln(w, "NAME\tVERSION\tACTION\tREASON")
		} else {
			fmt.Fprintln(w, "NAME\tVERSION\tACTION")
		}
		for _, rule := range result.Rules {
			action, ok := checkActionLabels[rule.Action]
			if !ok {
				action = string(rule.Action)
			}
			row := terminal.Sanitize(rule.Name) + "\t" + terminal.Sanitize(rule.Release) + "\t" + terminal.Sanitize(action)
			if withReason {
				// Every row needs the cell: tabwriter aligns only tab-terminated cells.
				row += "\t"
				if rule.Reason != nil {
					row += terminal.Sanitize(*rule.Reason)
				}
			}
			fmt.Fprintln(w, row)
		}
		w.Flush()
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, checkOnlyNote)
}

// addedRevisionInfo is the state snapshot for one added (slug, release) pair.
type addedRevisionInfo struct {
	name  string
	state string
}

// printReviewBreadcrumbs prints the review-required next step for added
// revisions that landed in draft. Best-effort: any lookup failure prints
// nothing — a successful submit never fails over a breadcrumb. The added
// tuples carry the rule slug; the hint echoes the API's Name field.
func printReviewBreadcrumbs(client *rule_versions.Client, token string, result *rule_versions.RuleVersionSubmitResponse, out io.Writer) {
	if len(result.Added) == 0 {
		return
	}

	type revisionKey struct{ slug, release string }
	states := make(map[revisionKey]addedRevisionInfo)
	fetched := make(map[string]bool)
	for _, added := range result.Added {
		if len(added) != 2 || fetched[added[0]] {
			continue
		}
		slug := added[0]
		fetched[slug] = true
		revisions, err := client.GetAllRuleRevisions(token, rule_versions.GetRuleRevisionsParams{
			SearchQuery: &slug,
			OnlyDefault: rule_versions.BoolPtr(false), // drafts are never the default revision
			Limit:       1000,
		})
		if err != nil {
			return
		}
		for _, r := range revisions {
			if r.Slug != slug && r.Name != slug {
				continue
			}
			states[revisionKey{slug, r.Release}] = addedRevisionInfo{
				name:  r.Name,
				state: string(r.State),
			}
		}
	}

	printed := false
	for _, added := range result.Added {
		if len(added) != 2 {
			continue
		}
		info, ok := states[revisionKey{added[0], added[1]}]
		if !ok || info.state != "draft" {
			continue
		}
		name, release := terminal.Sanitize(info.name), terminal.Sanitize(added[1])
		fmt.Fprintf(out, "rule/%s@%s submitted (state: draft — review required before enable)\n", name, release)
		fmt.Fprintf(out, "Next: qctl review request %s --release %s\n", name, release)
		printed = true
	}
	if printed {
		fmt.Fprintln(out, "New to the review flow? Run: qctl guide request-review")
	}
}
