package copycmd

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
)

// copyTablePlan is one copy of a table: what the server offered, what the
// command line asked, and the request that follows from both.
type copyTablePlan struct {
	// source names the source table for the reader.
	source  string
	offered *datasets.CopyOptions
	// offeredBody is the GET response offered was decoded from, as received.
	offeredBody []byte
	flags       copyTableFlags
	request     datasets.CopyRequest
}

// newName returns the display name of the new table: the request's, or with
// a series the name the server previewed.
func (plan *copyTablePlan) newName() string {
	if plan.request.Name != nil {
		return *plan.request.Name
	}
	return plan.offered.Name
}

// newTableName returns the table name of the new table: the request's, or with
// a series the table name the server previewed.
func (plan *copyTablePlan) newTableName() string {
	if plan.request.TableName != nil {
		return *plan.request.TableName
	}
	return plan.offered.TableName
}

// copyTableDryRun is the document --dry-run writes under -o json|yaml.
type copyTableDryRun struct {
	Options json.RawMessage      `json:"options"`
	Request datasets.CopyRequest `json:"request"`
}

// unsetValue stands for a null value in a plan cell.
const unsetValue = "(unset)"

// withheldValue stands for a value the offer withholds in a plan cell.
const withheldValue = "(withheld)"

// suppliedValue stands for a supplied secret in a plan cell.
const suppliedValue = "(supplied)"

// dryRunSecretMask replaces each supplied secret in the --dry-run document.
const dryRunSecretMask = "••••••••"

// writeCopyPlan writes the plan: the new table's names and location, the
// copy options, the data sources the table offers, and the secrets of each
// data source copy.
func writeCopyPlan(w io.Writer, plan *copyTablePlan) error {
	var b strings.Builder
	writeCopyPlanHeader(&b, plan)
	b.WriteString("\nOptions:\n")
	writeCopyPlanOptions(&b, plan)
	if len(plan.offered.DataSources) == 0 {
		b.WriteString("\nData sources: none that a copy can take.\n")
	} else {
		b.WriteString("\nData sources:\n")
		writeCopyPlanDataSources(&b, plan)
		if len(plan.flags.dataSources) == 0 {
			fmt.Fprintf(&b, "No data source is copied: a data source is copied only when --%s lists it.\n", dataSourcesFileFlag)
		}
		writeCopyPlanSecrets(&b, plan)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeCopyPlanHeader writes the source table and each of the new table's
// names and location, marked with where its value comes from.
func writeCopyPlanHeader(b *strings.Builder, plan *copyTablePlan) {
	offered, flags := plan.offered, plan.flags
	fmt.Fprintf(b, "Copy %s into a new table:\n", plan.source)
	destinationID := offered.DestinationID
	if flags.destinationID != nil {
		destinationID = *flags.destinationID
	}
	name, tableName := plan.newName(), plan.newTableName()
	nameOrigin := valueOrigin(flags.name != nil, nameFlag, "suggested")
	tableNameOrigin := valueOrigin(flags.tableName != nil, tableNameFlag, "suggested")
	if flags.series != nil {
		nameOrigin, tableNameOrigin = "(set by the series)", "(set by the series)"
	}
	rows := [][]string{
		{"Name:", planValue(&name), nameOrigin},
		{"Table name:", planValue(&tableName), tableNameOrigin},
		{"Destination id:", strconv.Itoa(destinationID), valueOrigin(flags.destinationID != nil, destinationIDFlag, "source's")},
		{"Database:", planValue(stringOr(flags.databaseName, offered.DatabaseName)),
			valueOrigin(flags.databaseName != nil, databaseNameFlag, "source's")},
		{"Schema:", planValue(stringOr(flags.schemaName, offered.SchemaName)),
			valueOrigin(flags.schemaName != nil, schemaNameFlag, "source's")},
	}
	writePlanTable(b, rows)
	if flags.series != nil && (flags.destinationID != nil || flags.databaseName != nil || flags.schemaName != nil) {
		b.WriteString("  The table name is previewed at the source table's location; at the new location\n" +
			"  its _N suffix may differ.\n")
	}
	if series := plan.request.Series; series != nil {
		b.WriteString("\nSeries membership:\n")
		writePlanTable(b, [][]string{
			{"Series:", planValue(&flags.seriesRef), series.SeriesID.String()},
			{"Period key:", planValue(&series.PeriodKey)},
		})
	}
}

// writeCopyPlanOptions writes one row per copy option offered.
func writeCopyPlanOptions(b *strings.Builder, plan *copyTablePlan) {
	rows := [][]string{{"OPTION", "INCLUDED", "DEFAULT", "ALWAYS", "ITEMS"}}
	for _, info := range plan.offered.Options {
		included := plan.request.Include != nil && slices.Contains(*plan.request.Include, info.Option)
		rows = append(rows, []string{
			planCell(string(info.Option)), yesNo(included), yesNo(info.Default), yesNo(info.Always), strconv.Itoa(info.Count),
		})
	}
	writePlanTable(b, rows)
}

// writeCopyPlanDataSources writes one row per data source offered, with the
// names of the secrets it holds and what the plan copies of it.
func writeCopyPlanDataSources(b *strings.Builder, plan *copyTablePlan) {
	rows := [][]string{{"ID", "NAME", "TYPE", "STATE", "LOCATION", "DISTINGUISHING FIELDS", "SECRETS", "COPY"}}
	for _, source := range plan.offered.DataSources {
		location := "-"
		switch {
		case readsSimpleURL(source):
			location = dsKeySimpleURL + "=" + withheldValue
		case source.LocationField != nil:
			location = planCell(*source.LocationField) + "=" + planValue(source.Location)
		}
		fields := make([]string, 0, len(source.DistinguishingFields))
		for _, key := range sortedDistinguishingFields(source) {
			fields = append(fields, planCell(key)+"="+planValue(source.DistinguishingFields[key]))
		}
		distinguishing := "-"
		if len(fields) > 0 {
			distinguishing = strings.Join(fields, ", ")
		}
		rows = append(rows, []string{
			strconv.Itoa(source.ID), planCell(source.Name), planCell(string(source.DataSourceType)),
			planCell(string(source.State)), location, distinguishing, planNames(source.SecretsToResupply),
			copyCell(source.ID, plan.flags.dataSources),
		})
	}
	writePlanTable(b, rows)
}

// planNames renders backend names for one plan cell, comma-separated, or "-"
// when there are none.
func planNames(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	cells := make([]string, 0, len(names))
	for _, name := range names {
		cells = append(cells, planCell(name))
	}
	return strings.Join(cells, ", ")
}

// writeCopyPlanSecrets writes copySecretsLine for each copy whose source holds
// a secret or that supplies one, and nothing when there is none.
func writeCopyPlanSecrets(b *strings.Builder, plan *copyTablePlan) {
	var lines []string
	for _, spec := range plan.flags.dataSources {
		source, ok := offeredDataSource(plan.offered.DataSources, spec.ID)
		if !ok || (len(source.SecretsToResupply) == 0 && len(spec.Secrets) == 0) {
			continue
		}
		lines = append(lines, dataSourceEntryLabel(spec.Entry, spec.ID)+": "+copySecretsLine(source, spec))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nSecrets of the copies:\n")
	for _, line := range lines {
		fmt.Fprintf(b, "  %s\n", line)
	}
}

// copyCell says how specs copy the data source id: "no", or for each copy
// "yes" and the keys it sets, sorted, each secret as suppliedValue.
func copyCell(id int, specs []dataSourceSpec) string {
	var copies []string
	for _, spec := range specs {
		if spec.ID != id {
			continue
		}
		keys := slices.Concat(slices.Collect(maps.Keys(spec.Values)), slices.Collect(maps.Keys(spec.Secrets)))
		sort.Strings(keys)
		if len(keys) == 0 {
			copies = append(copies, "yes")
			continue
		}
		set := make([]string, 0, len(keys))
		for _, key := range keys {
			value, given := spec.Values[key]
			if !given {
				set = append(set, key+"="+suppliedValue)
				continue
			}
			set = append(set, key+"="+planValue(&value))
		}
		copies = append(copies, "yes: "+strings.Join(set, ", "))
	}
	if len(copies) == 0 {
		return "no"
	}
	return strings.Join(copies, "; ")
}

// writeCopyDryRun writes what --dry-run prints after the plan: under
// -o json|yaml the offered options and the request, otherwise a line saying
// nothing was copied.
func writeCopyDryRun(w io.Writer, format string, plan *copyTablePlan) error {
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(w, format, copyTableDryRun{Options: plan.offeredBody, Request: maskSecrets(plan.request)})
	}
	_, err := fmt.Fprintln(w, "\nDry run: nothing was copied.")
	return err
}

// maskSecrets returns a copy of request in which each supplied secret of its
// data sources is dryRunSecretMask.
func maskSecrets(request datasets.CopyRequest) datasets.CopyRequest {
	if request.DataSources == nil {
		return request
	}
	sources := slices.Clone(*request.DataSources)
	for i := range sources {
		for _, key := range dataSourceSecretKeys {
			if field := secretRequestField(&sources[i], key); field != nil && *field != nil {
				mask := dryRunSecretMask
				*field = &mask
			}
		}
	}
	request.DataSources = &sources
	return request
}

// writePlanTable writes rows as aligned columns, each row indented by two
// spaces.
func writePlanTable(b *strings.Builder, rows [][]string) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintf(tw, "  %s\n", strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// planCellReplacer shows the line breaks and tabs Sanitize keeps as spaces.
var planCellReplacer = strings.NewReplacer("\t", " ", "\n", " ", "\u2028", " ", "\u2029", " ")

// planCell renders a backend string for one table cell.
func planCell(s string) string {
	return planCellReplacer.Replace(terminal.Sanitize(s))
}

// planValue renders a value for a plan cell: unsetValue for nil, and Go-quoted
// when it is empty or holds a space, a comma, a quote or a character a cell
// cannot show.
func planValue(v *string) string {
	switch {
	case v == nil:
		return unsetValue
	case *v == "" || strings.ContainsAny(*v, " ,\"") || planCell(*v) != *v:
		return strconv.Quote(*v)
	}
	return *v
}

// valueOrigin marks a header value as coming from --flag or from fallback.
func valueOrigin(given bool, flag, fallback string) string {
	if given {
		return "(--" + flag + ")"
	}
	return "(" + fallback + ")"
}

// stringOr returns v, or fallback when v is nil.
func stringOr(v *string, fallback string) *string {
	if v != nil {
		return v
	}
	return &fallback
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
