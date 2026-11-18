package copycmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
)

// writeCopyResult writes the copy's response: as received under -o json|yaml,
// table/<new id> under -o name, otherwise the new table, the items the copy
// carried with the secrets each data source copy lacks, and Next: lines that
// describe the new table and turn on each draft data source copy.
func writeCopyResult(w io.Writer, format string, plan *copyTablePlan, result *datasets.CopyResult, raw []byte) error {
	switch output.Format(format) {
	case output.FormatJSON:
		return output.WriteIndentedJSON(w, raw)
	case output.FormatYAML:
		return output.WriteJSONAsYAML(w, raw)
	case output.FormatName:
		_, err := fmt.Fprintf(w, "table/%d\n", result.ID)
		return err
	}
	items := result.DatasetCopy.Items
	lacking := make([][]string, len(items))
	anyLacking := false
	for i, item := range items {
		if item.ItemType == api.DatasetCopyItemTypeDataSource {
			lacking[i] = secretsToResupply(item.Context)
			anyLacking = anyLacking || len(lacking[i]) > 0
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nCreated table %s (id %d), a copy of %s.\n",
		strconv.Quote(result.Name), result.ID, plan.source)
	header := []string{"ITEM TYPE", "ACTION", "SOURCE", "TARGET"}
	if anyLacking {
		header = append(header, "SECRETS TO RESUPPLY")
	}
	rows := [][]string{append(header, "NOTE")}
	for i, item := range items {
		note := "-"
		if item.Note != nil && *item.Note != "" {
			note = strings.Join(strings.Fields(apierror.ToPlainText(*item.Note)), " ")
		}
		row := []string{
			planCell(string(item.ItemType)), planCell(string(item.Action)),
			objectIDCell(item.SourceObjectID), objectIDCell(item.TargetObjectID),
		}
		if anyLacking {
			row = append(row, planNames(lacking[i]))
		}
		rows = append(rows, append(row, note))
	}
	writePlanTable(&b, rows)
	fmt.Fprintf(&b, "Next: qctl describe table %d\n", result.ID)
	states := dataSourceCopyStates(plan, items)
	for i, item := range items {
		id, ok := objectID(item.TargetObjectID)
		if ok && states[i] == string(api.DataSourceStateDraft) {
			writeActivateNext(&b, id, lacking[i])
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// secretsToResupply returns the field names context lists under
// secrets_to_resupply, each rendered for a cell; nil when it lists none.
func secretsToResupply(context *map[string]interface{}) []string {
	if context == nil {
		return nil
	}
	listed, _ := (*context)["secrets_to_resupply"].([]interface{})
	var fields []string
	for _, field := range listed {
		if name, ok := field.(string); ok && name != "" {
			fields = append(fields, planCell(name))
		}
	}
	return fields
}

// dataSourceCopyStates returns, by index into items, the state each
// data_source item's copy was saved in: its entry's state, else its source's
// as offered; "" when its entry is not known. An item's entry is any entry
// copying its source when those agree on the state, else the entry at the
// item's position among the data_source items if that entry copies its source.
func dataSourceCopyStates(plan *copyTablePlan, items []api.DatasetCopyItemSchema) []string {
	states := make([]string, len(items))
	if plan.request.DataSources == nil {
		return states
	}
	entries := *plan.request.DataSources
	offered := make(map[int]string, len(plan.offered.DataSources))
	for _, source := range plan.offered.DataSources {
		offered[source.ID] = string(source.State)
	}
	entryState := func(entry api.DatasetCopyDataSourceRequest) string {
		if entry.State != nil {
			return string(*entry.State)
		}
		return offered[entry.SourceID]
	}
	position := -1
	for i, item := range items {
		if item.ItemType != api.DatasetCopyItemTypeDataSource {
			continue
		}
		position++
		sourceID, ok := objectID(item.SourceObjectID)
		if !ok {
			continue
		}
		var shared string
		seen, agree := false, true
		for _, entry := range entries {
			if entry.SourceID != sourceID {
				continue
			}
			state := entryState(entry)
			agree = agree && (!seen || state == shared)
			shared, seen = state, true
		}
		switch {
		case agree:
			states[i] = shared
		case position < len(entries) && entries[position].SourceID == sourceID:
			states[i] = entryState(entries[position])
		}
	}
	return states
}

// writeActivateNext writes the Next: line that turns on the draft data source
// copy id, after it is given the secrets it lacks.
func writeActivateNext(b *strings.Builder, id int, lacks []string) {
	if len(lacks) == 0 {
		fmt.Fprintf(b, "Next: qctl enable cloud-source %d\n", id)
		return
	}
	fmt.Fprintf(b, "Next: cloud source %d is a draft without %s: %s\n",
		id, planNames(lacks), activateSteps(strconv.Itoa(id), lacks))
}

// activateSteps returns the steps that give the draft cloud source id the
// secrets lacks names, then turn it on.
func activateSteps(id string, lacks []string) string {
	return fmt.Sprintf("add %s to the spec of 'qctl get cloud-source %s -o yaml', apply that with 'qctl apply -f', "+
		"then run 'qctl enable cloud-source %s'", secretsPronoun(lacks), id, id)
}

// objectIDCell renders a copy item's object id, "-" for nil.
func objectIDCell(id *string) string {
	if id == nil {
		return "-"
	}
	return planCell(*id)
}

// objectID returns a copy item's object id as a positive integer; ok is false
// for nil or anything else.
func objectID(id *string) (int, bool) {
	if id == nil {
		return 0, false
	}
	n, err := strconv.Atoi(*id)
	return n, err == nil && n > 0 && strconv.Itoa(n) == *id
}
