package table_series

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestMemberLockDetails(t *testing.T) {
	id := uuid.MustParse("bbbbbbbb-2222-3333-4444-555555555555")
	lockedAt := time.Date(2026, 10, 2, 11, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	for _, tc := range []struct {
		name, wantBy, wantAt string
		locked               bool
		user                 *api.UserInfoTinySchema
		at                   *time.Time
	}{
		{"named", "Ada Lovelace", "2026-10-02T09:00:00Z", true, &api.UserInfoTinySchema{ID: id, FirstName: "Ada", LastName: "Lovelace"}, &lockedAt},
		{"email", "ada@example.com", "-", true, &api.UserInfoTinySchema{ID: id, Email: "ada@example.com"}, nil},
		{"uuid", id.String(), "-", true, &api.UserInfoTinySchema{ID: id}, nil},
		{"unknown locker", "-", "2026-10-02T09:00:00Z", true, nil, &lockedAt},
		{"old lock", "-", "-", true, nil, nil},
		{"unlocked", "-", "-", false, nil, nil},
		{"unlocked stale metadata", "-", "-", false, &api.UserInfoTinySchema{ID: id, FirstName: "Ada"}, &lockedAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := Rows([]Series{{Members: []Member{{IsLocked: tc.locked, LockedBy: tc.user, LockedAt: tc.at}}}})
			if len(rows) != 1 {
				t.Fatalf("rows = %v", rows)
			}
			if got := rows[0]; got.LockedBy != tc.wantBy || got.LockedAt != tc.wantAt {
				t.Errorf("lock details = %q, %q; want %q, %q", got.LockedBy, got.LockedAt, tc.wantBy, tc.wantAt)
			}
		})
	}
}

func TestRowsCarryTheSeriesFrequency(t *testing.T) {
	first := "2026-Q3"
	rows := Rows([]Series{
		{Name: "Quarterly", Frequency: api.Quarterly, FirstPeriodKey: &first, Members: []Member{{PeriodKey: "2026-Q3"}, {PeriodKey: "2026-Q4"}}},
		{Name: "Empty", Frequency: api.Monthly, Members: []Member{}},
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	for i, want := range [][2]string{{"quarterly", "2026-Q3"}, {"quarterly", "2026-Q3"}, {"monthly", "-"}} {
		if got := [2]string{rows[i].Frequency, rows[i].FirstPeriodKey}; got != want {
			t.Errorf("row %d = %v, want %v", i, got, want)
		}
	}
}
