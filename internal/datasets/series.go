package datasets

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// SeriesSummary identifies a table's series.
type SeriesSummary struct {
	ID   uuid.UUID `json:"id" yaml:"id"`
	Name string    `json:"name" yaml:"name"`
}

// PeriodSummary describes a table's period and lock.
type PeriodSummary struct {
	PeriodKey   string        `json:"period_key" yaml:"period_key"`
	PeriodStart string        `json:"period_start" yaml:"period_start"`
	PeriodEnd   string        `json:"period_end" yaml:"period_end"`
	IsLocked    bool          `json:"is_locked" yaml:"is_locked"`
	LockedAt    string        `json:"locked_at,omitempty" yaml:"locked_at,omitempty"`
	LockedBy    *PeriodLocker `json:"locked_by,omitempty" yaml:"locked_by,omitempty"`
}

// PeriodLocker identifies the user who locked a period.
type PeriodLocker struct {
	ID    uuid.UUID `json:"id" yaml:"id"`
	Name  string    `json:"name" yaml:"name"`
	Email string    `json:"email" yaml:"email"`
}

// SummarizeSeries projects a table's membership, or returns nil for no series.
func SummarizeSeries(series *api.TableSeriesTinySchema, period *api.TableSeriesMemberPeriodSchema) (*SeriesSummary, *PeriodSummary) {
	if series == nil || series.ID == uuid.Nil || period == nil {
		return nil, nil
	}
	p := &PeriodSummary{PeriodKey: period.PeriodKey, IsLocked: period.IsLocked}
	p.PeriodStart = period.PeriodStart.Format("2006-01-02")
	p.PeriodEnd = period.PeriodEnd.Format("2006-01-02")
	if period.IsLocked {
		if period.LockedAt != nil {
			p.LockedAt = period.LockedAt.UTC().Format(time.RFC3339)
		}
		if user := period.LockedBy; user != nil && user.ID != uuid.Nil {
			p.LockedBy = &PeriodLocker{ID: user.ID, Name: strings.TrimSpace(user.FirstName + " " + user.LastName), Email: user.Email}
		}
	}
	return &SeriesSummary{ID: series.ID, Name: series.Name}, p
}

// Display returns the period key with a marker when locked.
func (p *PeriodSummary) Display() string {
	if p.IsLocked {
		return p.PeriodKey + " [locked]"
	}
	return p.PeriodKey
}

func renderSeriesSummary(out io.Writer, series *SeriesSummary, period *PeriodSummary) {
	state := "unlocked"
	if period.IsLocked {
		state = "locked"
	}
	fmt.Fprintf(out, "%-13s %s (id: %s) — period %s (%s)\n",
		"Series:", terminal.Sanitize(series.Name), series.ID, terminal.Sanitize(period.PeriodKey), state)
	fmt.Fprintf(out, "%-13s %s — %s\n", "Period dates:", period.PeriodStart, period.PeriodEnd)
	if period.IsLocked {
		locker := "(unknown)"
		if user := period.LockedBy; user != nil {
			locker = fmt.Sprintf("%s <%s> (id: %s)", user.Name, user.Email, user.ID)
		}
		fmt.Fprintf(out, "%-13s %s\n", "Locked by:", terminal.Sanitize(locker))
		fmt.Fprintf(out, "%-13s %s\n", "Locked at:", knownOrUnknown(period.LockedAt))
	}
}

func knownOrUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}

// LockedPeriod is a table's series period while it is locked.
type LockedPeriod struct {
	SeriesID   uuid.UUID
	SeriesName string
	PeriodKey  string
}

// LockedPeriodOf returns d's series period when it is locked, and nil when d
// is in no series or its period is unlocked.
func LockedPeriodOf(d *DatasetFull) *LockedPeriod {
	if d == nil || d.Series == nil || d.Series.ID == uuid.Nil || d.Period == nil || !d.Period.IsLocked {
		return nil
	}
	return &LockedPeriod{SeriesID: d.Series.ID, SeriesName: d.Series.Name, PeriodKey: d.Period.PeriodKey}
}

// String is `period "<key>" of series "<name>"`, sanitized.
func (l *LockedPeriod) String() string {
	return terminal.Sanitize(fmt.Sprintf("period %q of series %q", l.PeriodKey, l.SeriesName))
}

// UnlockCommand is the qctl command that unlocks the period, sanitized.
func (l *LockedPeriod) UnlockCommand() string {
	return terminal.Sanitize(fmt.Sprintf("qctl table-series unlock %s --period %s", l.SeriesID, l.PeriodKey))
}
