package get

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewRuleCommand creates the get rule command for fetching a single rule revision
func NewRuleCommand() *cobra.Command {
	var release string

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Display a single rule revision",
		Long: `Display a rule by name, short ID, or full UUID.

When --release is specified, shows a single revision for that release.
When --release is omitted and the rule has multiple releases, shows all releases.

With -o yaml or -o json, a single-release output uses kind: Rule (apply-compatible
manifest), while multi-release output uses kind: RuleFamily.

With -o code, prints only the raw source code (no headers or formatting).

Examples:
  qctl get rule email_validator
  qctl get rule email_validator --release 1.0.0
  qctl get rule 550e8400
  qctl get rule email_validator -o yaml > rule.yaml
  qctl get rule email_validator -o code > myrule.py`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			if release != "" {
				return getRuleSingleRelease(cmd, client, ctx, input, release)
			}
			return getRuleAllReleases(cmd, client, ctx, input)
		},
	}

	cmd.Flags().StringVar(&release, "release", "", "Specify which release when a rule has multiple releases")
	return cmd
}

// getRuleSingleRelease handles the --release path: resolve to one revision, output kind: Rule.
func getRuleSingleRelease(cmd *cobra.Command, client *rule_versions.Client, ctx *cmdutil.CommandContext, input string, release string) error {
	ruleRevisionID, err := client.ResolveRuleID(ctx.Credential.AccessToken, input, release)
	if err != nil {
		return err
	}

	outputFormat, _ := cmd.Flags().GetString("output")

	switch outputFormat {
	case "json", "yaml":
		detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, ruleRevisionID)
		if err != nil {
			return fmt.Errorf("failed to get rule details: %w", err)
		}
		manifest := rule_versions.FullResponseToGetManifest(detail)
		return encodeStructured(cmd, outputFormat, manifest)

	case "code":
		detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, ruleRevisionID)
		if err != nil {
			return fmt.Errorf("failed to get rule details: %w", err)
		}
		return writeCode(cmd, detail)

	default:
		family, err := client.GetRuleRevisionAllReleases(ctx.Credential.AccessToken, ruleRevisionID)
		if err != nil {
			return fmt.Errorf("failed to get rule: %w", err)
		}
		var revision *rule_versions.RuleRevisionTiny
		for i := range family.Results {
			if family.Results[i].ID.String() == ruleRevisionID {
				revision = &family.Results[i]
				break
			}
		}
		if revision == nil {
			return fmt.Errorf("rule revision %s not found in results", ruleRevisionID)
		}
		defaultCols := "slug,release,state,tags,short_id"
		if ctx.Verbosity >= 1 {
			defaultCols = "slug,release,state,tags,id,description,affected_columns"
		}
		computeUpgradeAvailable(family.Results)
		displayResults := rule_versions.ToDisplayList([]rule_versions.RuleRevisionTiny{*revision})
		return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
	}
}

// getRuleAllReleases handles the no-release path: resolve to any revision, fetch family, show all releases.
// When the input is a UUID/short-ID that resolves to a specific revision, only that revision is shown.
func getRuleAllReleases(cmd *cobra.Command, client *rule_versions.Client, ctx *cmdutil.CommandContext, input string) error {
	ruleRevisionID, err := client.ResolveRuleIDAny(ctx.Credential.AccessToken, input)
	if err != nil {
		return err
	}

	family, err := client.GetRuleRevisionAllReleases(ctx.Credential.AccessToken, ruleRevisionID)
	if err != nil {
		return fmt.Errorf("failed to get rule: %w", err)
	}

	// When the user provided a UUID or short ID, they asked for a specific revision —
	// filter results to only that revision instead of showing the whole family.
	singleByID := false
	results := family.Results
	if org.IsUUIDLike(input) {
		for i := range results {
			if results[i].ID.String() == ruleRevisionID {
				results = []rule_versions.RuleRevisionTiny{results[i]}
				singleByID = true
				break
			}
		}
	}

	outputFormat, _ := cmd.Flags().GetString("output")

	switch outputFormat {
	case "json", "yaml":
		if singleByID || len(results) == 1 {
			detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, results[0].ID.String())
			if err != nil {
				return fmt.Errorf("failed to get rule details: %w", err)
			}
			manifest := rule_versions.FullResponseToGetManifest(detail)
			return encodeStructured(cmd, outputFormat, manifest)
		}
		// Multiple revisions — structured output is ambiguous, require --release
		return fmt.Errorf("rule '%s' has %d releases; use --release to select one for %s output, or use table format to see all", input, len(results), outputFormat)

	case "code":
		if singleByID || len(results) == 1 {
			detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, results[0].ID.String())
			if err != nil {
				return fmt.Errorf("failed to get rule details: %w", err)
			}
			return writeCode(cmd, detail)
		}
		return fmt.Errorf("rule '%s' has %d releases; use --release to select one for %s output, or use table format to see all", input, len(results), outputFormat)

	default:
		defaultCols := "slug,release,state,tags,short_id"
		if ctx.Verbosity >= 1 {
			defaultCols = "slug,release,state,tags,id,description,affected_columns"
		}
		computeUpgradeAvailable(results)
		displayResults := rule_versions.ToDisplayList(results)
		return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
	}
}

// writeCode prints only the raw code from a rule revision detail.
func writeCode(cmd *cobra.Command, detail *rule_versions.RuleRevisionFull) error {
	if detail.Code == nil || *detail.Code == "" {
		return fmt.Errorf("rule %q has no code", detail.Name)
	}
	code := *detail.Code
	// Ensure exactly one trailing newline
	code = strings.TrimRight(code, "\n") + "\n"
	_, err := fmt.Fprint(cmd.OutOrStdout(), code)
	return err
}

// encodeStructured writes data as JSON or YAML to the command's output.
func encodeStructured(cmd *cobra.Command, format string, data interface{}) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)
	case "yaml":
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(data)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// computeUpgradeAvailable sets UpgradeAvailable on non-default revisions when a
// default exists, enabling "Update available" / "Newer than default" tags.
func computeUpgradeAvailable(revisions []rule_versions.RuleRevisionTiny) {
	if len(revisions) <= 1 {
		return
	}
	hasDefault := false
	for _, r := range revisions {
		if r.IsDefault {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		return
	}
	t := true
	for i := range revisions {
		if !revisions[i].IsDefault {
			revisions[i].UpgradeAvailable = &t
		}
	}
}

