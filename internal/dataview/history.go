package dataview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
)

func attachCellHistory(page *Page, schema *api.DataWithCellHistorySchema, layout columnLayout, opts NormalizeOptions) {
	page.ShowCellHistory = opts.ShowRowLogs
	for _, field := range layout.fields {
		page.RawRowLogs = page.RawRowLogs || field == rowLogsFieldName
	}
	if !page.ShowCellHistory {
		return
	}
	for i := range page.Rows {
		var logs map[string]interface{}
		if layout.logs >= 0 {
			value := schema.Results[i][layout.logs]
			logs, _ = value.(map[string]interface{})
			if value != nil && logs == nil {
				page.Rows[i].History = append(page.Rows[i].History, FieldHistory{
					Field: rowLogsFieldName, Lines: []string{"- Logs: " + historyValue(value)},
				})
			}
		}
		var events map[string][]api.CellFlagEventSchema
		if schema.CellFlagEvents != nil {
			events = (*schema.CellFlagEvents)[i]
		}
		page.Rows[i].History = append(page.Rows[i].History, fieldHistories(logs, events)...)
		for j, field := range layout.fields {
			if len(events[field]) > 0 {
				page.Rows[i].Cells[j].HasFlagHistory = true
				page.AnyFlagHistory = true
			}
		}
	}
}

func fieldHistories(logs map[string]interface{}, events map[string][]api.CellFlagEventSchema) []FieldHistory {
	fields := make(map[string]bool, len(logs)+len(events))
	for field := range logs {
		fields[field] = true
	}
	for field := range events {
		fields[field] = true
	}
	names := make([]string, 0, len(fields))
	for field := range fields {
		names = append(names, field)
	}
	sort.Strings(names)
	var history []FieldHistory
	for _, field := range names {
		h := FieldHistory{Field: historyText(field)}
		if entries, ok := logs[field].([]interface{}); ok {
			for _, entry := range entries {
				h.Lines = append(h.Lines, historyLog(entry)...)
			}
		} else if value := logs[field]; value != nil {
			h.Lines = append(h.Lines, "- Logs: "+historyValue(value))
		}
		for _, event := range events[field] {
			h.Lines = append(h.Lines, "- "+historyFlag(event))
		}
		if len(h.Lines) > 0 {
			history = append(history, h)
		}
	}
	return history
}

func historyLog(value interface{}) []string {
	entry, ok := value.(map[string]interface{})
	if !ok {
		return []string{"- Log: " + historyValue(value)}
	}
	base := make(map[string]interface{}, len(entry))
	for key, v := range entry {
		if key != "approvals" && key != "review_requests" {
			base[key] = v
		}
	}
	lines := []string{"- Log: " + historyValue(base)}
	for _, key := range []string{"approvals", "review_requests"} {
		value, exists := entry[key]
		if !exists {
			continue
		}
		items, ok := value.([]interface{})
		if !ok {
			lines = append(lines, "  - "+key+": "+historyValue(value))
			continue
		}
		for _, item := range items {
			obj, ok := item.(map[string]interface{})
			if !ok {
				lines = append(lines, "  - "+key+": "+historyValue(item))
				continue
			}
			var line string
			if key == "review_requests" {
				line = historyRequest(obj)
			} else {
				line = historyApproval(obj)
			}
			lines = append(lines, "  - "+line)
		}
	}
	return lines
}

func historyApproval(approval map[string]interface{}) string {
	label := "Approval (" + historyText(formatValue(approval["source"])) + ", " +
		historyText(formatValue(approval["decision"])) + ") by "
	if approval["decision"] == "approve" {
		switch approval["source"] {
		case "per_change":
			label = "Checked by "
		case "row":
			label = "Row approved by "
		}
	}
	line := label + historyActor(approval["actor"]) + " · " + historyText(formatValue(approval["decided_at"]))
	if comment := approval["comment"]; comment != nil && comment != "" {
		line += " — " + historyText(formatValue(comment))
	}
	return line
}

func historyRequest(request map[string]interface{}) string {
	return "Sent for approval: " + historyText(formatValue(request["title"])) +
		" · " + historyText(formatValue(request["sent_at"])) + " · by " + historyActor(request["requestor"]) +
		" · review-group " + historyText(formatValue(request["review_group_id"])) +
		" · data-review " + historyText(formatValue(request["data_review_id"]))
}

func historyFlag(event api.CellFlagEventSchema) string {
	var actor api.UserInfoTinyDictSchema
	if event.Actor != nil {
		actor = api.UserInfoTinyDictSchema(*event.Actor)
	}
	line := "Flag " + historyText(string(event.Kind)) + " " + historyText(string(event.Event)) +
		" · " + event.CreatedAt.Format(time.RFC3339Nano) + " · by " + historyUser(&actor) +
		" · flag " + event.ID.String()
	if event.DetachReason != nil {
		line += " · detach_reason=" + historyText(string(*event.DetachReason))
	}
	if event.ThreadID != nil {
		line += " · thread " + event.ThreadID.String()
	}
	line += " · observed=" + historyValue(event.ObservedValue)
	if event.Comment != "" {
		line += " — " + historyText(event.Comment)
	}
	return line
}

func historyActor(value interface{}) string {
	if value == nil {
		return unattributed
	}
	data, err := json.Marshal(value)
	if err != nil {
		return historyValue(value)
	}
	var user api.UserInfoTinyDictSchema
	if err := json.Unmarshal(data, &user); err != nil {
		return historyValue(value)
	}
	return historyUser(&user)
}

// unattributed names an actor the response leaves blank or null.
const unattributed = "(unattributed)"

func historyUser(user *api.UserInfoTinyDictSchema) string {
	if user.ID == uuid.Nil && user.FirstName == "" && user.LastName == "" && user.Email == "" {
		return unattributed
	}
	return historyText(comments.UserDisplayWithRole(user, user.ID))
}

func historyValue(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return historyText(fmt.Sprint(value))
	}
	return historyText(string(data))
}

func historyText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == 0x2028 || r == 0x2029 {
			return '\n'
		}
		return r
	}, value)
	return escapeCell(value)
}

func writeCellHistory(b *strings.Builder, page *Page) {
	if !page.ShowCellHistory {
		return
	}
	b.WriteString("\n## CELL HISTORY\n")
	anyHistory := false
	for i, row := range page.Rows {
		for _, history := range row.History {
			anyHistory = true
			key := rowKey(row, i)
			if row.ID != "" {
				key = "Row " + key
			}
			fmt.Fprintf(b, "\n### %s · %s\n", key, history.Field)
			for _, line := range history.Lines {
				b.WriteString(line + "\n")
			}
		}
	}
	if !anyHistory {
		if page.RawRowLogs {
			b.WriteString("- no flag events returned on this page; row_logs_ is shown in the grid\n")
		} else {
			b.WriteString("- none returned on this page\n")
		}
	}
}
