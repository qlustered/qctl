package deliverables

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// Columns are the default table columns.
const Columns = "name,slug,receiver,enabled,latest_run,latest_run_params,updated_at"

const noneText = "none"
const nullText = "null"

// TrashColumns are the default table columns of a listing that includes the
// trash.
const TrashColumns = Columns + ",archived_at"

// Display is one deliverable's table row.
type Display struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Receiver          string `json:"receiver"`
	FilenamePattern   string `json:"filename_pattern"`
	PeriodParam       string `json:"period_param"`
	Enabled           string `json:"enabled"`
	EnabledRevisionID string `json:"enabled_revision_id"`
	LatestRun         string `json:"latest_run"`
	LatestRunID       string `json:"latest_run_id"`
	LatestRunParams   string `json:"latest_run_params"`
	ArchivedAt        string `json:"archived_at"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// Rows returns one row per list item.
func Rows(items []ListItem) []Display {
	rows := make([]Display, len(items))
	for i, item := range items {
		rows[i] = row(Deliverable{
			ID: item.ID, Name: item.Name, Slug: item.Slug, ReceiverLabel: item.ReceiverLabel,
			FilenamePattern: item.FilenamePattern, PeriodParam: item.PeriodParam,
			EnabledRevisionID: item.EnabledRevisionID, ArchivedAt: item.ArchivedAt,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
		if run := item.LatestRun; run != nil {
			rows[i].LatestRun = string(run.State)
			rows[i].LatestRunID = strconv.Itoa(run.ID)
			rows[i].LatestRunParams = RunParamsText(run.RunParamsTyped, run.RunParams)
		}
	}
	return rows
}

// DetailRow returns the row of one deliverable and its latest run, which may
// be nil.
func DetailRow(item *Deliverable, run *Run) Display {
	out := row(*item)
	if run != nil {
		out.LatestRun = string(run.State)
		out.LatestRunID = strconv.Itoa(run.ID)
		out.LatestRunParams = RunParamsText(run.RunParamsTyped, run.RunParams)
	}
	return out
}

func row(item Deliverable) Display {
	out := Display{
		ID: item.ID.String(), Name: item.Name, Slug: item.Slug, Receiver: dash(item.ReceiverLabel),
		FilenamePattern: item.FilenamePattern, PeriodParam: "-", Enabled: "no", EnabledRevisionID: "-",
		LatestRun: "-", LatestRunID: "-", LatestRunParams: "-", ArchivedAt: relative(item.ArchivedAt),
		CreatedAt: relative(item.CreatedAt), UpdatedAt: relative(item.UpdatedAt),
	}
	if item.PeriodParam != nil {
		out.PeriodParam = *item.PeriodParam
	}
	if item.EnabledRevisionID != nil {
		out.Enabled = "yes"
		out.EnabledRevisionID = item.EnabledRevisionID.String()
	}
	return out
}

// RunParamsText renders run parameters as key=value pairs sorted by key,
// taking each value from typed when it has the key, or "-" when there are
// none.
func RunParamsText(typed *map[string]api.RunParameterValue, raw map[string]interface{}) string {
	values := map[string]string{}
	for key, value := range raw {
		values[key] = nullText
		if value != nil {
			values[key] = fmt.Sprint(value)
		}
	}
	if typed != nil {
		for key, value := range *typed {
			values[key] = nullText
			if value.Text != nil {
				values[key] = *value.Text
			}
		}
	}
	if len(values) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, key := range keys {
		pairs[i] = key + "=" + values[key]
	}
	return strings.Join(pairs, ", ")
}

func relative(t *time.Time) string {
	return dash(timeutil.FormatRelativePtr(t))
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// FormatDetail renders a deliverable, its releases and its latest run for a
// person.
func FormatDetail(d *Detail) string {
	item := d.Deliverable
	var b strings.Builder
	line := func(label, value string) {
		fmt.Fprintf(&b, "%-17s %s\n", label+":", value)
	}
	line("Name", terminal.Sanitize(item.Name))
	line("Slug", terminal.Sanitize(item.Slug))
	line("ID", item.ID.String())
	line("Receiver", terminal.Sanitize(dash(item.ReceiverLabel)))
	line("Filename pattern", terminal.Sanitize(item.FilenamePattern))
	periodParam := "-"
	if item.PeriodParam != nil {
		periodParam = terminal.Sanitize(*item.PeriodParam)
	}
	line("Period param", periodParam)
	inTrash := "no"
	if item.ArchivedAt != nil {
		inTrash = "yes, since " + item.ArchivedAt.Format(time.RFC3339)
	}
	line("In trash", inTrash)
	line("Enabled release", enabledRelease(item, d.Revisions))
	var drafts []string
	for _, revision := range d.Revisions {
		if revision.State == api.DeliverableRevisionStateDraft {
			drafts = append(drafts, terminal.Sanitize(revision.Release))
		}
	}
	line("Draft releases", dash(strings.Join(drafts, ", ")))
	line("Latest run", latestRun(d.LatestRun))
	line("Created", timestamp(item.CreatedAt))
	line("Updated", timestamp(item.UpdatedAt))
	return b.String()
}

func enabledRelease(item *Deliverable, revisions []Revision) string {
	if item.EnabledRevisionID == nil {
		return noneText
	}
	for _, revision := range revisions {
		if revision.ID == *item.EnabledRevisionID {
			text := terminal.Sanitize(revision.Release)
			if revision.EnabledAt != nil {
				text += ", enabled " + revision.EnabledAt.Format(time.RFC3339)
			}
			return text
		}
	}
	return "revision " + item.EnabledRevisionID.String()
}

func latestRun(run *Run) string {
	if run == nil {
		return noneText
	}
	text := fmt.Sprintf("%d, %s, sequence %d", run.ID, run.State, run.Sequence)
	if params := RunParamsText(run.RunParamsTyped, run.RunParams); params != "-" {
		text += ", " + terminal.Sanitize(params)
	}
	if run.UpdatedAt != nil {
		text += ", updated " + run.UpdatedAt.Format(time.RFC3339)
	}
	return text
}

func timestamp(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}
