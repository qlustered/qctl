// Package explain provides the explain command for displaying schema documentation.
package explain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/spf13/cobra"
)

const explainJSONOutputFormat = "json"

const arrayType = "array"

// anyType is the text view's name for a schema that sets no type: JSON
// Schema's "any value".
const anyType = "any"

// resourceDocsPath maps CLI resource names to API docs anchor paths.
var resourceDocsPath = map[string]string{
	"table":          "/datasets",
	"dataset":        "/datasets",
	"cloud-source":   "/data-sources",
	"destination":    "/destinations",
	"alert":          "/alerts",
	"warning":        "/warnings",
	"file":           "/stored-items",
	"error-incident": "/errors",
}

// NewCommand creates the explain command
func NewCommand() *cobra.Command {
	var recursive bool
	var page pageOptions

	cmd := &cobra.Command{
		Use:   "explain RESOURCE[.FIELD]",
		Short: "Show documentation for a resource or field",
		Long: `Display documentation for a resource type or specific field from the API schema.
A name that is a command rather than a resource, such as apply, prints that command's help.

This command retrieves schema information from the API server's OpenAPI specification,
providing accurate type information, descriptions, constraints, and allowed values.
The schema is cached locally and refreshed when the API version changes; manifest
resources like table-rule are documented statically and work offline.

A resource page prints its GOTCHAS as a count; --gotchas prints them in full and
--grep TERM prints only the ones that contain TERM.

Examples:
  # Show all fields for the table resource
  qctl explain table

  # Show specific field information
  qctl explain table.migration_policy

  # Show all fields recursively (including nested objects)
  qctl explain table --recursive

  # Print every gotcha, or only the ones that mention a word
  qctl explain alert --gotchas
  qctl explain alert --grep severity

  # Output schema as JSON
  qctl explain table -o json

  # Output schema as YAML
  qctl explain table -o yaml

Run "qctl api-resources" for every resource this command can explain.`,
		Args: cobra.ExactArgs(1),
		// runExplain routes only json/yaml to the schema; every other value
		// prints the page.
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table", "json", "yaml"),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("output")
			return runExplain(cmd, args[0], format, recursive, page)
		},
	}

	cmd.Flags().BoolVar(&recursive, "recursive", false, "Show all nested fields")
	cmd.Flags().BoolVar(&page.gotchas, "gotchas", false, "Print the page's GOTCHAS in full instead of counting them")
	cmd.Flags().StringVar(&page.grep, "grep", "", "Print only the GOTCHAS that contain `TERM`, ignoring case")

	return cmd
}

// runExplain answers for every resource in the catalog, and for every
// top-level command whose name is not also a resource's.
//
// Machine output (-o json/-o yaml) and dotted field paths emit the schema
// document alone, and fail when no schema exists. Only `-o table` for a bare
// resource name prints the page.
func runExplain(cmd *cobra.Command, resourcePath, format string, recursive bool, page pageOptions) error {
	name, field, hasField := strings.Cut(resourcePath, ".")
	res, found := catalog.ResourceNamed(cmd.Root(), name)
	schemaOnly := format == explainJSONOutputFormat || format == "yaml" || hasField //nolint:goconst // Keep format names explicit here.

	if err := page.validate(cmd, schemaOnly); err != nil {
		return err
	}

	if !found {
		if c := topLevelCommand(cmd.Root(), name); c != nil {
			if pageFlagsSet(cmd) {
				return qerrors.NewBadUsagef("%q is a command, not a resource, and has no GOTCHAS: run '%s --help'",
					name, c.CommandPath())
			}
			return explainCommand(cmd, c, name, schemaOnly)
		}
	}

	if schemaOnly {
		return explainSchemaOnly(cmd, res, found, name, field, hasField, format, recursive)
	}

	if !found {
		return unknownResource(cmd, name)
	}

	out := cmd.OutOrStdout()
	if cmd.Flags().Changed("grep") {
		printMatchingGotchas(out, res, page.grep)
		return nil
	}
	printPage(out, res, page.gotchas)

	info, serverURL, err := loadSchema(cmd, res)
	switch {
	case err != nil:
		// Decision 10: a schema fetch failure degrades, it does not fail. An
		// agent that concludes a resource does not exist because the server
		// was down is the exact failure this command exists to prevent.
		errOut := cmd.ErrOrStderr()
		fmt.Fprintf(errOut, "warning: %s; showing the offline page only\n", degradeReason(err))
		if verbosity, _ := cmd.Flags().GetCount("verbose"); verbosity > 0 {
			fmt.Fprintf(errOut, "%v\n", err)
		}
		return nil
	case info == nil:
		return nil // the resource declares no schema; the page is the answer
	}
	fmt.Fprintln(out)
	docsURL := ""
	if path, ok := resourceDocsPath[strings.ToLower(res.Name)]; ok && serverURL != "" {
		docsURL = serverURL + "/api/docs#" + path
	}
	return printSchemaText(out, info, recursive, docsURL)
}

// noServerError marks a schema load that never reached the network because no
// API server is configured. Error() is the wrapped message verbatim: the
// machine path (Decision 9) folds this error into its own text, so a prefix
// here would change frozen output. Only the -o table degrade path, which
// matches on the type, says anything different.
type noServerError struct{ err error }

func (e noServerError) Error() string { return e.err.Error() }
func (e noServerError) Unwrap() error { return e.err }

// degradeReason condenses a failed schema load into one line. The bootstrap
// error is a 14-line onboarding message; interpolating it turned Decision 10's
// warning — the first thing anyone hits with no context configured — into an
// unreadable paragraph. The full text is one -v away.
func degradeReason(err error) string {
	var noServer noServerError
	if errors.As(err, &noServer) {
		return "no API server configured"
	}
	return "live schema unavailable (" + firstLine(err.Error()) + ")"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimRight(s[:i], " ")
	}
	return s
}

// loadSchema resolves the page's namespaced schema reference. A manifest
// schema is served from the binary and never touches the network; only an
// openapi reference bootstraps and fetches. info is nil when the page declares
// no schema.
func loadSchema(cmd *cobra.Command, res catalog.Resource) (*schema.SchemaInfo, string, error) {
	ns, ref, ok := res.SchemaRef()
	if !ok {
		return nil, "", nil
	}
	if ns == resources.SchemaNamespaceManifest {
		// A page naming a manifest schema that does not ship is a build-time
		// mistake (TestFrontmatterReferences catches it), but returning a nil
		// document would print an empty page in -o table, encode null in
		// -o json, and panic in findManifestField for a dotted path.
		info, ok := manifestSchemas[ref]
		if !ok {
			return nil, "", fmt.Errorf("no static manifest schema %q", ref)
		}
		return info, "", nil
	}
	ctx, err := cmdutil.BootstrapWithoutAuth(cmd)
	if err != nil {
		return nil, "", noServerError{err}
	}
	schema.SetAPIEndpoint(ctx.ServerURL)
	info, err := schema.GetSchema(ref)
	if err != nil {
		return nil, ctx.ServerURL, err
	}
	if write, ok := res.WriteSchemaRef(); ok {
		// A live spec without the write component leaves the read as it is.
		if writeInfo, err := schema.GetSchema(write); err == nil {
			schema.MergeWriteSchema(info, writeInfo)
		}
	}
	markCreateOnly(info, res.CreateOnly)
	return info, ctx.ServerURL, nil
}

// markCreateOnly marks the named top-level fields and their create values.
func markCreateOnly(info *schema.SchemaInfo, fields resources.CreateOnlyFields) {
	for name, values := range fields {
		for i := range info.Fields {
			if info.Fields[i].Name == name {
				info.Fields[i].CreateOnly = true
				info.Fields[i].CreateOnlyValues = values
			}
		}
	}
}

// openAPIFieldProps resolves path as info, the loaded document, lists its
// top-level field: through the page's writeSchema under a write-only field,
// with that field's write-only mark, and with its read-only and create-only
// marks when path is the field itself.
func openAPIFieldProps(res catalog.Resource, info *schema.SchemaInfo, ref, path string) (*schema.FieldInfo, error) {
	top, _, _ := strings.Cut(path, ".")
	var listed *schema.FieldInfo
	for i := range info.Fields {
		if info.Fields[i].Name == top {
			listed = &info.Fields[i]
			break
		}
	}
	component := ref
	if listed != nil && listed.WriteOnly {
		component, _ = res.WriteSchemaRef()
	}
	fieldInfo, err := schema.GetFieldProps(component, path)
	if err != nil {
		return nil, err
	}
	if listed != nil {
		fieldInfo.WriteOnly = listed.WriteOnly
		if path == top {
			fieldInfo.ReadOnly = listed.ReadOnly
			fieldInfo.CreateOnly = listed.CreateOnly
			fieldInfo.CreateOnlyValues = listed.CreateOnlyValues
		}
	}
	return fieldInfo, nil
}

// explainSchemaOnly prints the schema document with no page (Decision 9).
func explainSchemaOnly(cmd *cobra.Command, res catalog.Resource, found bool,
	name, field string, hasField bool, format string, recursive bool,
) error {
	if !found {
		return fmt.Errorf("failed to get schema for %q: %w", name,
			qerrors.NewBadUsagef("unknown resource %q", name))
	}
	if res.Schema == "" {
		return fmt.Errorf("failed to get schema for %q: %w", name,
			qerrors.NewBadUsagef("resource %q has no schema; 'qctl explain %s' prints its page", name, name))
	}
	info, serverURL, err := loadSchema(cmd, res)
	if err != nil {
		return fmt.Errorf("failed to get schema for %q: %w", name, err)
	}
	if hasField {
		// Text only: -o json / -o yaml carry the schema alone (Decision 9).
		var dec fieldDecoration
		if format != explainJSONOutputFormat && format != "yaml" {
			dec = decorationFor(strings.ToLower(res.Name), field)
		}
		ns, ref, _ := res.SchemaRef()
		if ns == resources.SchemaNamespaceManifest {
			fieldInfo, err := findManifestField(info, field)
			if err != nil {
				return err
			}
			return outputFieldInfo(cmd.OutOrStdout(), fieldInfo, format, recursive, name+"."+field, dec)
		}
		fieldInfo, err := openAPIFieldProps(res, info, ref, field)
		if err != nil {
			return fmt.Errorf("failed to get field %q: %w", field, err)
		}
		return outputFieldInfo(cmd.OutOrStdout(), fieldInfo, format, recursive, name+"."+field, dec)
	}
	docsURL := ""
	if path, ok := resourceDocsPath[strings.ToLower(name)]; ok && serverURL != "" {
		docsURL = serverURL + "/api/docs#" + path
	}
	return outputSchemaInfo(cmd.OutOrStdout(), info, format, recursive, docsURL)
}

// topLevelCommand returns the child of root that name, ignoring case and
// surrounding space, names or aliases; nil when there is none.
func topLevelCommand(root *cobra.Command, name string) *cobra.Command {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range root.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// explainCommand prints c's help on cmd's stdout or, when schemaOnly is set,
// returns a usage error naming c's --help.
func explainCommand(cmd, c *cobra.Command, name string, schemaOnly bool) error {
	if schemaOnly {
		return qerrors.NewBadUsagef("%q is a command, not a resource: run '%s --help'"+
			"\n\nRun 'qctl api-resources' to list every resource", name, c.CommandPath())
	}
	c.InitDefaultHelpFlag()
	c.SetOut(cmd.OutOrStdout())
	defer c.SetOut(nil)
	return c.Help()
}

// unknownResource redirects instead of erroring bare.
func unknownResource(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown resource %q", name)
	if s := catalog.Suggest(cmd.Root(), name); len(s) > 0 {
		msg += "\n\nDid you mean: " + strings.Join(s, ", ") + "?"
	}
	return qerrors.NewBadUsagef("%s\n\nRun 'qctl api-resources' to list every resource", msg)
}

// pageOptions holds the flags that shape a resource page's GOTCHAS section.
type pageOptions struct {
	gotchas bool
	grep    string
}

// pageFlagsSet reports whether --gotchas or --grep was given.
func pageFlagsSet(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("gotchas") || cmd.Flags().Changed("grep")
}

// validate refuses an empty --grep, and either flag where no page is printed.
func (p pageOptions) validate(cmd *cobra.Command, schemaOnly bool) error {
	if cmd.Flags().Changed("grep") && strings.TrimSpace(p.grep) == "" {
		return qerrors.NewBadUsagef("--grep needs a non-empty term")
	}
	if schemaOnly && pageFlagsSet(cmd) {
		return qerrors.NewBadUsagef("--gotchas and --grep print a resource page; " +
			"-o json, -o yaml and RESOURCE.FIELD print the schema alone")
	}
	return nil
}

// printPage renders the hand-written half of a resource's documentation. The
// GOTCHAS section is a count unless allGotchas is set.
func printPage(w io.Writer, r catalog.Resource, allGotchas bool) {
	fmt.Fprintf(w, "RESOURCE: %s\n", r.Name)
	if r.Summary != "" {
		fmt.Fprintf(w, "SUMMARY:  %s\n", r.Summary)
	}
	if len(r.Aliases) > 0 {
		fmt.Fprintf(w, "ALIASES:  %s\n", strings.Join(r.Aliases, ", "))
	}
	if len(r.Usage) > 0 {
		fmt.Fprintf(w, "\nUSAGE:\n")
		for _, u := range r.Usage {
			fmt.Fprintf(w, "  %s\n", u)
		}
	}
	if len(r.Examples) > 0 {
		fmt.Fprintf(w, "\nEXAMPLES:\n")
		for _, ex := range r.Examples {
			if ex.Description != "" {
				fmt.Fprintf(w, "  # %s\n", ex.Description)
			}
			fmt.Fprintf(w, "  %s\n", ex.Command)
		}
	}
	switch {
	case len(r.Gotchas) == 0:
	case allGotchas:
		fmt.Fprintf(w, "\nGOTCHAS:\n")
		printGotchas(w, r.Gotchas)
	default:
		fmt.Fprintf(w, "\nGOTCHAS:  %d, not shown: 'qctl explain %s --gotchas' prints them, "+
			"'--grep TERM' only the ones that mention TERM\n", len(r.Gotchas), r.Name)
	}
	if len(r.SeeAlso) > 0 {
		fmt.Fprintf(w, "\nSEE ALSO:\n")
		for _, s := range r.SeeAlso {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}

// printMatchingGotchas prints the resource's gotchas that contain term,
// ignoring case.
func printMatchingGotchas(w io.Writer, r catalog.Resource, term string) {
	needle := strings.ToLower(strings.TrimSpace(term))
	var matched []resources.Gotcha
	for _, g := range r.Gotchas {
		if strings.Contains(strings.ToLower(g.Text), needle) {
			matched = append(matched, g)
		}
	}
	fmt.Fprintf(w, "RESOURCE: %s\n", r.Name)
	fmt.Fprintf(w, "GOTCHAS matching %q: %d of %d\n", strings.TrimSpace(term), len(matched), len(r.Gotchas))
	printGotchas(w, matched)
}

func printGotchas(w io.Writer, gotchas []resources.Gotcha) {
	for _, g := range gotchas {
		fmt.Fprintf(w, "  - %s\n", g.Text)
	}
}

func outputSchemaInfo(w io.Writer, info *schema.SchemaInfo, format string, recursive bool, docsURL string) error {
	switch format {
	case explainJSONOutputFormat:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	case "yaml":
		encoder := jsonnum.NewYAMLEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(info)
	default:
		return printSchemaText(w, info, recursive, docsURL)
	}
}

func outputFieldInfo(w io.Writer, info *schema.FieldInfo, format string, recursive bool, path string, dec fieldDecoration) error {
	switch format {
	case explainJSONOutputFormat:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	case "yaml":
		encoder := jsonnum.NewYAMLEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(info)
	default:
		printFieldText(w, info, 0, dec)
		if len(info.Properties) > 0 || (info.Items != nil && len(info.Items.Properties) > 0) {
			fmt.Fprintln(w, "FIELDS:")
			printFieldSummaryChildren(w, info, "", recursive, 0)
			if len(info.Properties) > 0 {
				fmt.Fprintf(w, "To explain a child field: qctl explain %s\n",
					cmdutil.ShellArg(path+"."+info.Properties[0].Name))
			}
		}
		return nil
	}
}

func printSchemaText(w io.Writer, info *schema.SchemaInfo, recursive bool, docsURL string) error {
	fmt.Fprintf(w, "KIND:     %s\n", info.Name)
	fmt.Fprintf(w, "VERSION:  qluster.ai/v1\n")
	fmt.Fprintln(w)

	if info.Description != "" {
		fmt.Fprintf(w, "DESCRIPTION:\n")
		printWrapped(w, info.Description, "  ", 78)
		fmt.Fprintln(w)
	}

	if docsURL != "" {
		fmt.Fprintf(w, "DOCS:     %s\n", docsURL)
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "FIELDS:\n")
	for _, field := range info.Fields {
		printFieldSummary(w, &field, "  ", recursive, 0)
	}

	return nil
}

func printFieldText(w io.Writer, info *schema.FieldInfo, indent int, dec fieldDecoration) { //nolint:unparam // indent predates this change and stays 0 at its one call site; not introduced here.
	prefix := strings.Repeat("  ", indent)

	fmt.Fprintf(w, "%sFIELD:    %s\n", prefix, info.Name)

	typeStr := formatType(info)
	fmt.Fprintf(w, "%sTYPE:     %s\n", prefix, typeStr)

	if info.Required {
		fmt.Fprintf(w, "%sREQUIRED: true\n", prefix)
	}

	if info.ReadOnly {
		fmt.Fprintf(w, "%sREADONLY: true\n", prefix)
	}

	if info.WriteOnly {
		fmt.Fprintf(w, "%sWRITEONLY: true\n", prefix)
	}

	if info.CreateOnly {
		fmt.Fprintf(w, "%sCREATEONLY: true\n", prefix)
		if len(info.CreateOnlyValues) > 0 {
			fmt.Fprintf(w, "%sCREATEONLY VALUES: %s\n", prefix, strings.Join(info.CreateOnlyValues, ", "))
		}
	}

	if info.Default != nil {
		fmt.Fprintf(w, "%sDEFAULT:  %v\n", prefix, info.Default)
	}

	fmt.Fprintln(w)

	if info.Description != "" {
		fmt.Fprintf(w, "%sDESCRIPTION:\n", prefix)
		printWrapped(w, info.Description, prefix+"  ", 78-len(prefix))
		fmt.Fprintln(w)
	}

	if len(info.Enum) > 0 {
		fmt.Fprintf(w, "%sALLOWED VALUES:\n", prefix)
		for _, v := range info.Enum {
			dec.printAllowedValue(w, prefix, v)
		}
	}
	dec.printActions(w, len(info.Enum) > 0)
}

func printFieldSummary(w io.Writer, info *schema.FieldInfo, prefix string, recursive bool, depth int) {
	fmt.Fprintf(w, "%s%s <%s>%s\n", prefix, info.Name, formatType(info), fieldSummaryMarkers(info))
	printFieldSummaryDetails(w, info, prefix)
	fmt.Fprintln(w)
	printFieldSummaryChildren(w, info, prefix, recursive, depth)
}

func fieldSummaryMarkers(info *schema.FieldInfo) string {
	var markers []string
	if info.Required {
		markers = append(markers, "required")
	}
	if info.ReadOnly {
		markers = append(markers, "read-only")
	}
	if info.WriteOnly {
		markers = append(markers, "write-only")
	}
	if info.CreateOnly {
		marker := "create-only"
		if len(info.CreateOnlyValues) > 0 {
			marker += ": " + strings.Join(info.CreateOnlyValues, ", ")
		}
		markers = append(markers, marker)
	}
	if len(markers) == 0 {
		return ""
	}
	return " <" + strings.Join(markers, ", ") + ">"
}

func printFieldSummaryDetails(w io.Writer, info *schema.FieldInfo, prefix string) {
	if info.Description != "" {
		desc := info.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		fmt.Fprintf(w, "%s  %s\n", prefix, desc)
	}
	if len(info.Enum) > 0 && len(info.Enum) <= 5 {
		fmt.Fprintf(w, "%s  Allowed: %s\n", prefix, strings.Join(info.Enum, ", "))
	} else if len(info.Enum) > 5 {
		fmt.Fprintf(w, "%s  Allowed: %s, ... (%d total)\n", prefix, strings.Join(info.Enum[:3], ", "), len(info.Enum))
	}
}

func printFieldSummaryChildren(w io.Writer, info *schema.FieldInfo, prefix string, recursive bool, depth int) {
	if !recursive && depth >= 1 {
		return
	}
	// Static array schemas also mirror item keys in Properties for dotted paths.
	if info.Items != nil && len(info.Items.Properties) > 0 {
		fmt.Fprintf(w, "%s  [array item properties]\n", prefix)
		for _, prop := range info.Items.Properties {
			printFieldSummary(w, &prop, prefix+"    ", recursive, depth+1)
		}
		return
	}
	for _, prop := range info.Properties {
		printFieldSummary(w, &prop, prefix+"  ", recursive, depth+1)
	}
}

func formatType(info *schema.FieldInfo) string {
	typeStr := typeName(info)
	if info.Nullable {
		typeStr += ", nullable"
	}
	return typeStr
}

// typeName is formatType without the nullable marker. A union joins its
// alternatives with " | ".
func typeName(info *schema.FieldInfo) string {
	switch {
	case info.Type == arrayType && info.Items != nil:
		return "[]" + itemTypeName(info.Items)
	case info.Ref != "" && info.Type != arrayType:
		// Prefer ref name over generic "object" type
		return info.Ref
	case isUnion(info):
		alts := info.AnyOf
		if len(alts) == 0 {
			alts = info.OneOf
		}
		names := make([]string, len(alts))
		for i := range alts {
			names[i] = typeName(&alts[i])
		}
		return strings.Join(names, " | ")
	case info.Format != "" && info.Ref == "":
		return fmt.Sprintf("%s(%s)", info.Type, info.Format)
	case info.Type == "":
		return anyType
	}
	return info.Type
}

// itemTypeName names an array's item type. Only a nested array or a union is
// rendered in full; any other item type is named without its format.
func itemTypeName(items *schema.FieldInfo) string {
	switch {
	case items.Ref != "":
		return items.Ref
	case items.Type == arrayType:
		return typeName(items)
	case isUnion(items):
		return "(" + typeName(items) + ")"
	case items.Type != "":
		return items.Type
	}
	return anyType
}

func isUnion(info *schema.FieldInfo) bool {
	return len(info.AnyOf) > 0 || len(info.OneOf) > 0
}

func printWrapped(w io.Writer, text, prefix string, width int) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return
	}

	line := prefix
	for _, word := range words {
		if len(line)+len(word)+1 > width && line != prefix {
			fmt.Fprintln(w, line)
			line = prefix + word
		} else {
			if line == prefix {
				line += word
			} else {
				line += " " + word
			}
		}
	}
	if line != prefix {
		fmt.Fprintln(w, line)
	}
}
