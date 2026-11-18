package datasets

import (
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestSummarizeLockedPeriodWithoutLocker(t *testing.T) {
	series, period := SummarizeSeries(&api.TableSeriesTinySchema{ID: uuid.New(), Name: "Monthly"},
		&api.TableSeriesMemberPeriodSchema{PeriodKey: "2026-10", IsLocked: true})
	if series == nil || period == nil || period.LockedBy != nil || period.Display() != "2026-10 [locked]" {
		t.Fatalf("locked period without locker: series=%+v, period=%+v", series, period)
	}
}
