package diff

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/ruletemplate"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_templates"
	"github.com/spf13/cobra"
)

// templateOnlyFlags are the flags only a TableRuleTemplate diff takes.
var templateOnlyFlags = []string{"table", "table-id", "write-mappings"}

// refuseTemplateOptions refuses, exit 2, a flag of templateOnlyFlags or
// -o json|yaml on a diff of kind.
func refuseTemplateOptions(cmd *cobra.Command, kind string) error {
	for _, name := range templateOnlyFlags {
		if cmd.Flags().Changed(name) {
			return qerrors.NewBadUsagef("--%s applies only to kind %s, not %s", name, rule_templates.Kind, kind)
		}
	}
	if format := outputFormat(cmd); format == output.FormatJSON || format == output.FormatYAML {
		return qerrors.NewBadUsagef("-o %s applies only to kind %s; diff of %s prints text only", format, rule_templates.Kind, kind)
	}
	return nil
}

func outputFormat(cmd *cobra.Command) output.Format {
	format, _ := cmd.Flags().GetString("output")
	return output.Format(format)
}

// diffTableRuleTemplate previews the TableRuleTemplate at path on the table
// --table or --table-id names. It prints the plan and what each open decision
// needs, and returns a silent exit 2 when an item is a conflict, else
// ErrDiffDifferences when an item is created or replaced, else nil after
// writing "nothing to do" and the next steps to stderr. Under -o json|yaml the
// preview's body goes to stdout and every other line to stderr.
func diffTableRuleTemplate(cmd *cobra.Command, path string) error {
	file, err := ruletemplate.Load(path)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	writeMappings, _ := cmd.Flags().GetBool("write-mappings")
	if writeMappings {
		if err := file.CheckMappingsWritable(); err != nil {
			return err
		}
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	preview, err := ruletemplate.NewPreview(ctx, cmd, file)
	if err != nil {
		return err
	}
	plan := ruletemplate.Classify(preview.Merge.Items)

	format := outputFormat(cmd)
	document := format == output.FormatJSON || format == output.FormatYAML
	human := cmd.OutOrStdout()
	if document || (!plan.HasConflicts() && !plan.HasChanges()) {
		human = cmd.ErrOrStderr()
	}

	if err := writeTemplateReport(human, preview, plan, path, writeMappings); err != nil {
		return err
	}
	if document {
		if err := writeDocument(cmd.OutOrStdout(), format, preview.Body); err != nil {
			return err
		}
	}
	return templateVerdict(plan)
}

// writeTemplateReport writes the plan, or "Nothing to do" when nothing is
// created, replaced or a conflict, then the next steps, the column_mapping
// keys to add (or, with writeMappings, the keys it wrote into the file at
// path, which it opens only for an unmapped item) and the resolution document
// for the other conflicts.
func writeTemplateReport(w io.Writer, preview *ruletemplate.Preview, plan ruletemplate.Plan, path string, writeMappings bool) error {
	if plan.HasConflicts() || plan.HasChanges() {
		if err := rule_merges.RenderTemplateMerge(w, preview.Merge, preview.Names); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "Nothing to do: %s (rule merge %d).\n", plan.Counts(), preview.Merge.ID)
	}
	preview.WriteNextSteps(w, plan)
	if writeMappings {
		var adds []rule_templates.MappingsToAdd
		if len(plan.Unmapped) > 0 {
			var err error
			if adds, err = rule_templates.WriteMappings(path, plan.Unmapped, preview.Enriches); err != nil {
				return err
			}
		}
		preview.WriteMappingsAdded(w, plan, adds)
	} else {
		preview.WriteMappingsToAdd(w, plan, fmt.Sprintf("qctl diff -f %s %s --write-mappings",
			cmdutil.ShellArg(path), preview.TableFlag()))
	}
	preview.WriteResolutionSkeleton(w, plan)
	return nil
}

// templateVerdict is diff's exit for plan: a silent exit 2 when an item is a
// conflict, else ErrDiffDifferences when an item is created or replaced, else
// nil.
func templateVerdict(plan ruletemplate.Plan) error {
	switch {
	case plan.HasConflicts():
		return qerrors.NewSilent(qerrors.ExitBadUsage)
	case plan.HasChanges():
		return qerrors.ErrDiffDifferences
	}
	return nil
}

// writeDocument writes body as received, indented JSON or YAML by format.
func writeDocument(w io.Writer, format output.Format, body []byte) error {
	if format == output.FormatYAML {
		return output.WriteJSONAsYAML(w, body)
	}
	return output.WriteIndentedJSON(w, body)
}
