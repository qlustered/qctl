package cloud_sources

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// overrideSuffix ends the name of every per-source settings override on the
// wire.
const overrideSuffix = "_override"

// excludedRowSignatures is the signatures of the source's deleted rows, kept
// by row operations. It ends in _override on the wire but is not a setting.
const excludedRowSignatures = "excluded_row_signatures"

// Summary is one cloud source's identity, configuration and ingestion status.
//
// Overrides holds names only, never values. The routing fields and Intake are
// set only for a source that feeds a table series; EmailIntake only for a
// portal source that does.
type Summary struct {
	Name               string              `json:"name" yaml:"name"`
	ID                 int                 `json:"id" yaml:"id"`
	DataSourceType     string              `json:"data_source_type" yaml:"data_source_type"`
	Table              string              `json:"table" yaml:"table"`
	TableID            int                 `json:"table_id" yaml:"table_id"`
	TableSeriesID      *uuid.UUID          `json:"table_series_id,omitempty" yaml:"table_series_id,omitempty"`
	RoutingMode        string              `json:"routing_mode,omitempty" yaml:"routing_mode,omitempty"`
	RoutingPattern     string              `json:"routing_pattern,omitempty" yaml:"routing_pattern,omitempty"`
	RoutingColumn      string              `json:"routing_column,omitempty" yaml:"routing_column,omitempty"`
	Intake             *Intake             `json:"intake,omitempty" yaml:"intake,omitempty"`
	EmailIntake        *EmailIntake        `json:"email_intake,omitempty" yaml:"email_intake,omitempty"`
	State              string              `json:"state" yaml:"state"`
	Schedule           string              `json:"schedule" yaml:"schedule"`
	LastSync           *time.Time          `json:"last_sync" yaml:"last_sync"`
	CleanRowsCount     *int                `json:"clean_rows_count" yaml:"clean_rows_count"`
	BadRowsCount       *int                `json:"bad_rows_count" yaml:"bad_rows_count"`
	UnresolvedAlerts   int                 `json:"unresolved_alerts" yaml:"unresolved_alerts"`
	DeltaAppliedCursor *int                `json:"delta_applied_cursor" yaml:"delta_applied_cursor"`
	PeriodDeltaCursors []PeriodDeltaCursor `json:"period_delta_cursors" yaml:"period_delta_cursors"`
	Overrides          []string            `json:"overrides" yaml:"overrides"`

	QuarantineByValidation *datasets.QuarantineValidation `json:"quarantine_by_validation,omitempty" yaml:"quarantine_by_validation,omitempty"`
	QuarantineByHold       *datasets.QuarantineHolds      `json:"quarantine_by_hold,omitempty" yaml:"quarantine_by_hold,omitempty"`
	QuarantineNotLoaded    *int                           `json:"quarantine_not_loaded,omitempty" yaml:"quarantine_not_loaded,omitempty"`
}

// Intake is a series source's own period bounds and broker due days, as
// stored on the source. A nil field is null on the wire.
type Intake struct {
	StartPeriodKey *string `json:"start_period_key" yaml:"start_period_key"`
	EndPeriodKey   *string `json:"end_period_key" yaml:"end_period_key"`
	BrokerDueDays  *int    `json:"broker_due_days" yaml:"broker_due_days"`
}

// EmailIntake is a portal series source's email intake state. Address is nil
// when intake has never been enabled.
type EmailIntake struct {
	Enabled              bool    `json:"enabled" yaml:"enabled"`
	Address              *string `json:"address" yaml:"address"`
	AutoSendIssuesReport bool    `json:"auto_send_issues_report" yaml:"auto_send_issues_report"`
}

// NewSummary builds a Summary from the two reads of one cloud source: the
// source's identity and configuration from full, its overrides from body, the
// JSON full was decoded from, and its last finished ingestion, row counts and
// unresolved alerts from tiny.
func NewSummary(full *CloudSourceFull, body []byte, tiny *CloudSourceTiny) (*Summary, error) {
	overrides, err := SetOverrides(body)
	if err != nil {
		return nil, err
	}
	s := &Summary{
		Name:               derefString(full.Name),
		ID:                 derefInt(full.ID),
		Table:              derefString(full.DatasetName),
		TableID:            derefInt(full.DatasetID),
		State:              string(derefDataSourceState(full.State)),
		Schedule:           derefString(full.Schedule),
		LastSync:           tiny.LastSync,
		CleanRowsCount:     tiny.CleanRowsCount,
		BadRowsCount:       tiny.BadRowsCount,
		UnresolvedAlerts:   tiny.UnresolvedAlerts,
		DeltaAppliedCursor: full.DeltaAppliedCursor,
		PeriodDeltaCursors: periodDeltaCursors(full.PeriodDeltaCursors),
		Overrides:          overrides,
	}
	if full.DataSourceType != nil {
		s.DataSourceType = string(*full.DataSourceType)
	}
	if full.TableSeriesID != nil {
		s.TableSeriesID = full.TableSeriesID
		s.RoutingMode = derefString(full.RoutingMode)
		s.RoutingPattern = derefString(full.RoutingPattern)
		s.RoutingColumn = derefString(full.RoutingColumn)
		s.Intake = &Intake{StartPeriodKey: full.StartPeriodKey, EndPeriodKey: full.EndPeriodKey, BrokerDueDays: full.BrokerDueDays}
		if s.DataSourceType == string(DataSourceTypeQlusterUI) {
			s.EmailIntake = &EmailIntake{
				Enabled:              derefBool(full.EmailIntakeEnabled),
				Address:              full.EmailIntakeAddress,
				AutoSendIssuesReport: derefBool(full.AutoSendIssuesReport),
			}
		}
	}
	return s, nil
}

// SetOverrides names the settings overrides in body, a cloud source's JSON
// object: every member whose key ends in _override and whose value is not
// null, with the suffix removed, sorted. excluded_row_signatures is never
// named. The result is never nil.
func SetOverrides(body []byte) ([]string, error) {
	members, err := nonNullMembers(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read the cloud source's overrides: %w", err)
	}
	names := []string{}
	for key := range members {
		name, ok := strings.CutSuffix(key, overrideSuffix)
		if !ok || name == excludedRowSignatures {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// RenderSummary writes s as aligned text, followed by the commands for the
// manifest and the table's settings.
func RenderSummary(out io.Writer, s *Summary) {
	line := func(label, value string) {
		fmt.Fprintf(out, "%-13s %s\n", label, terminal.Sanitize(value))
	}
	line("Cloud source:", fmt.Sprintf("%s (id: %d)", s.Name, s.ID))
	line("Type:", s.DataSourceType)
	line("Table:", fmt.Sprintf("%s (id: %d)", s.Table, s.TableID))
	if s.TableSeriesID != nil {
		line("Series:", s.TableSeriesID.String())
		line("Routing:", routing(s))
		if s.Intake != nil {
			line("Intake:", s.Intake.bounds())
			line("Broker due:", s.Intake.brokerDue())
		}
		if s.EmailIntake != nil {
			line("Email intake:", s.EmailIntake.state()+"; "+s.EmailIntake.issuesReport())
		}
	}
	line("State:", s.State)
	line("Schedule:", orNone(s.Schedule))
	lastIngest := "never"
	if s.LastSync != nil {
		lastIngest = timeutil.FormatRelative(*s.LastSync)
	}
	line("Last ingest:", lastIngest)
	rows := fmt.Sprintf("%s clean / %s quarantined", formatCount(s.CleanRowsCount), formatCount(s.BadRowsCount))
	if q := s.QuarantineByValidation; q != nil {
		rows += fmt.Sprintf(" (%d passed validation, %d failed validation)", q.PassedValidation, q.FailedValidation)
	}
	line("Rows:", rows)
	if h := s.QuarantineByHold; h != nil {
		line("Quarantine:", datasets.FormatQuarantineHolds(*h))
	}
	if n := s.QuarantineNotLoaded; n != nil && *n > 0 {
		line("Not loaded:", datasets.FormatQuarantineNotLoaded(*n))
	}
	line("Alerts:", fmt.Sprintf("%d unresolved", s.UnresolvedAlerts))
	if s.DeltaAppliedCursor != nil {
		line("Delta cursor:", fmt.Sprintf("%d", *s.DeltaAppliedCursor))
	}
	overrides := "none"
	if len(s.Overrides) > 0 {
		overrides = strings.Join(s.Overrides, ", ")
	}
	line("Overrides:", overrides)
	if len(s.PeriodDeltaCursors) > 0 {
		fmt.Fprintln(out, "\nPeriod delta cursors:")
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  PERIOD\tTABLE\tSETTINGS MODEL\tDELTA CURSOR")
		for _, cursor := range s.PeriodDeltaCursors {
			fmt.Fprintf(w, "  %s\t%d\t%d\t%s\n", terminal.Sanitize(cursor.PeriodKey), cursor.DatasetID,
				cursor.SettingsModelID, formatCount(cursor.DeltaAppliedCursor))
		}
		w.Flush()
	}

	manifest := fmt.Sprintf("qctl get cloud-source %d -o yaml", s.ID)
	settings := fmt.Sprintf("qctl get table-config %d", s.TableID)
	senders := fmt.Sprintf("qctl get email-senders --cloud-source-id %d", s.ID)
	width := max(len(manifest), len(settings))
	if s.EmailIntake != nil {
		width = max(width, len(senders))
	}
	fmt.Fprintf(out, "\n  %-*s  # the applyable manifest\n", width, manifest)
	fmt.Fprintf(out, "  %-*s  # the table's settings\n", width, settings)
	if s.EmailIntake != nil {
		fmt.Fprintf(out, "  %-*s  # who may send files to the intake\n", width, senders)
	}
}

// state is "on — <address>", or "off" with the address it keeps when it has
// one.
func (e *EmailIntake) state() string {
	switch {
	case e.Enabled && e.Address != nil:
		return "on — " + *e.Address
	case e.Enabled:
		return "on"
	case e.Address != nil:
		return "off (keeps " + *e.Address + ")"
	}
	return "off"
}

func derefBool(v *bool) bool {
	return v != nil && *v
}

func (e *EmailIntake) issuesReport() string {
	if e.AutoSendIssuesReport {
		return "issues report sent automatically for each loaded file"
	}
	return "issues report not sent automatically"
}

// unset is the summary's text for a value that is not set.
const unset = "(none)"

// bounds is the intake's own start_period_key and end_period_key.
func (in *Intake) bounds() string {
	start, end := unset, unset
	if in.StartPeriodKey != nil {
		start = *in.StartPeriodKey
	}
	if in.EndPeriodKey != nil {
		end = *in.EndPeriodKey
	}
	return "start " + start + ", end " + end
}

// brokerDue is the intake's broker due days, or the series' schedule when
// it has none.
func (in *Intake) brokerDue() string {
	if in.BrokerDueDays == nil {
		return "the series' schedule"
	}
	unit := "days"
	if *in.BrokerDueDays == 1 {
		unit = "day"
	}
	return fmt.Sprintf("%d %s after each period ends, a weekend date moved to the Friday before", *in.BrokerDueDays, unit)
}

// Routing modes whose routing names a pattern or a column.
const (
	routingModeFileName = "file_name"
	routingModeColumn   = "column"
)

// routing is s's routing mode, followed by the file-name pattern or the
// column it routes on when the mode uses one.
func routing(s *Summary) string {
	mode := orNone(s.RoutingMode)
	switch {
	case s.RoutingMode == routingModeFileName && s.RoutingPattern != "":
		return mode + " " + s.RoutingPattern
	case s.RoutingMode == routingModeColumn && s.RoutingColumn != "":
		return mode + " " + s.RoutingColumn
	}
	return mode
}

func orNone(v string) string {
	if v == "" {
		return unset
	}
	return v
}

func formatCount(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *n)
}
