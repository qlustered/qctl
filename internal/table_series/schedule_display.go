package table_series

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// ScheduleRow is the display text of one step or escalation of a deadline schedule. A cell the row does not have
// reads "-".
type ScheduleRow struct {
	Name string
	// Value is a step's days or an escalation's threshold.
	Value   string
	Enabled string
	Date    string
}

const noCell = "-"

// ScheduleSteps returns the rows of s's day values and reminder steps.
func ScheduleSteps(s *DeadlineSchedule) []ScheduleRow {
	d := s.Dates
	step := func(name string, v api.DeadlineStepSchema, date string) ScheduleRow {
		return ScheduleRow{name, overrideText("%d", v.Days, v.DefaultDays, v.OverrideDays), strconv.FormatBool(v.Enabled), date}
	}
	return []ScheduleRow{
		{"mga_deadline", overrideText("%d", s.MgaDeadline.Days, s.MgaDeadline.DefaultDays, s.MgaDeadline.OverrideDays), noCell, d.MgaDeadline.String()},
		{"broker_due", overrideText("%d", s.BrokerDue.Days, s.BrokerDue.DefaultDays, s.BrokerDue.OverrideDays), noCell, d.BrokerDue.String()},
		step("first_reminder", s.FirstReminder, d.FirstReminder.String()),
		{"due_day_reminder", noCell, strconv.FormatBool(s.DueDayReminder.Enabled), d.DueDayReminder.String()},
		step("late_notice", s.LateNotice, d.LateNotice.String()),
		step("late_repeat", s.LateRepeat, noCell),
		step("issues_open", s.IssuesOpen, noCell),
		step("at_risk", s.AtRisk, d.AtRisk.String()),
	}
}

// ScheduleEscalations returns the rows of s's escalations; each Date is "-".
func ScheduleEscalations(s *DeadlineSchedule) []ScheduleRow {
	e := s.Escalations
	row := func(name, value string, enabled bool) ScheduleRow {
		return ScheduleRow{name, value, strconv.FormatBool(enabled), noCell}
	}
	return []ScheduleRow{
		row("still_failing", thresholdText("%d files", e.StillFailing.Files)+", "+thresholdText("%d days", e.StillFailing.Days), e.StillFailing.Enabled),
		row("failing_share", thresholdText("%d%%", e.FailingShare.Percent), e.FailingShare.Enabled),
		row("exception_requested", noCell, e.ExceptionRequested.Enabled),
		row("rule_disputed", noCell, e.RuleDisputed.Enabled),
		row("ready_for_review", noCell, e.ReadyForReview.Enabled),
		row("not_loaded", thresholdText("%d days", e.NotLoaded.Days), e.NotLoaded.Enabled),
	}
}

func thresholdText(format string, t api.EscalationThresholdSchema) string {
	return overrideText(format, t.Value, t.DefaultValue, t.OverrideValue)
}

// overrideText renders value with format, marked "(default)", or "(override, default <def>)" when override is set.
func overrideText(format string, value, def int, override *int) string {
	if override == nil {
		return fmt.Sprintf(format+" (default)", value)
	}
	return fmt.Sprintf(format+" (override, default "+format+")", value, def)
}

// WriteSchedulePreview prints, under a header naming preview's period and time zone, one "<row> <column>: old → new"
// line for each cell of ScheduleSteps and ScheduleEscalations whose text differs between stored and preview. It
// writes nothing when no cell differs.
func WriteSchedulePreview(out io.Writer, stored, preview *DeadlineSchedule) error {
	var lines []string
	compare := func(value string, before, after []ScheduleRow) {
		for i := range before {
			for _, cell := range []struct{ column, before, after string }{
				{value, before[i].Value, after[i].Value},
				{"enabled", before[i].Enabled, after[i].Enabled},
				{"date", before[i].Date, after[i].Date},
			} {
				if cell.before != cell.after {
					lines = append(lines, fmt.Sprintf("  %s %s: %s → %s\n", before[i].Name, cell.column, cell.before, cell.after))
				}
			}
		}
	}
	compare("days", ScheduleSteps(stored), ScheduleSteps(preview))
	compare("threshold", ScheduleEscalations(stored), ScheduleEscalations(preview))
	if len(lines) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "Deadline schedule for period %s (%s), stored → after apply:\n",
		terminal.Sanitize(preview.Period.PeriodKey), terminal.Sanitize(preview.TimeZone)); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := io.WriteString(out, line); err != nil {
			return err
		}
	}
	return nil
}

// RenderDeadlines writes s's period, time zone, a STEP table of ScheduleSteps and, after a blank line, an ESCALATION
// table of ScheduleEscalations. collected marks the period as the one being collected.
func RenderDeadlines(out io.Writer, s *DeadlineSchedule, collected bool) error {
	header := fmt.Sprintf("Deadlines: period %s (%s to %s), time zone %s", terminal.Sanitize(s.Period.PeriodKey),
		s.Period.PeriodStart.String(), s.Period.PeriodEnd.String(), terminal.Sanitize(s.TimeZone))
	if collected {
		header += ", the period being collected"
	}
	if _, err := fmt.Fprintln(out, header); err != nil {
		return err
	}
	steps := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(steps, "  STEP\tDAYS\tENABLED\tDATE")
	for _, row := range ScheduleSteps(s) {
		fmt.Fprintf(steps, "  %s\t%s\t%s\t%s\n", row.Name, row.Value, row.Enabled, row.Date)
	}
	if err := steps.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	escalations := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(escalations, "  ESCALATION\tTHRESHOLD\tENABLED")
	for _, row := range ScheduleEscalations(s) {
		fmt.Fprintf(escalations, "  %s\t%s\t%s\n", row.Name, row.Value, row.Enabled)
	}
	return escalations.Flush()
}
