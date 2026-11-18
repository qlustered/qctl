package deliverables

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestRunRowsFullyPopulated(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { at := now.Add(-d); return &at }
	text := func(s string) *string { return &s }
	revision := uuid.MustParse(twinOneID)
	rendered := uuid.MustParse(twinTwoID)
	typed := map[string]api.RunParameterValue{"year": {Text: text("2026.0"), Type: api.Float}}
	run := Run{
		ID: 42, DeliverableID: uuid.MustParse(monthlyID), Sequence: 3, State: api.DeliverableRunStateStale,
		RevisionMode: "reissue_enabled", RunParams: map[string]interface{}{"year": 2026.0, "period": "2026-09"},
		RunParamsTyped: &typed, AttemptID: 4, RenderedAt: ago(2*time.Hour + 5*time.Minute),
		RenderError: text("Sheet \"Claims\":\n\tmissing column\x1b[31m"), StaleReason: text("manifest_drift"),
		RevisionID: revision, RenderedRevisionID: &rendered,
		SignedOffAt: ago(3*24*time.Hour + time.Hour), DeliveredAt: ago(5*time.Minute + 10*time.Second),
		CancelledAt: ago(4*time.Hour + time.Minute), CreatedAt: ago(15*24*time.Hour + time.Hour),
		UpdatedAt: ago(10*time.Minute + 5*time.Second),
	}
	want := RunDisplay{
		ID: "42", DeliverableID: monthlyID, Sequence: "3", State: "stale", RevisionMode: "reissue_enabled",
		Params: "period=2026-09, year=2026.0", Attempt: "4", RenderedAt: "2 hours ago",
		RenderError: `Sheet "Claims":\n\tmissing column\x1b[31m`, StaleReason: "manifest_drift",
		RevisionID: twinOneID, RenderedRevisionID: twinTwoID, SignedOffAt: "3 days ago", DeliveredAt: "5 minutes ago",
		CancelledAt: "4 hours ago", CreatedAt: "2 weeks ago", UpdatedAt: "10 minutes ago",
	}
	if got := RunRows([]Run{run}); !reflect.DeepEqual(got, []RunDisplay{want}) {
		t.Errorf("rows = %#v\nwant %#v", got, []RunDisplay{want})
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var organizationRun api.OrganizationDeliverableRunListItem
	if err := json.Unmarshal(data, &organizationRun); err != nil {
		t.Fatal(err)
	}
	organizationRun.DeliverableName = "Monthly\nclaims\x1b[31m"
	want.Deliverable = `Monthly\nclaims\x1b[31m`
	if got := OrganizationRunRows([]api.OrganizationDeliverableRunListItem{organizationRun}); !reflect.DeepEqual(got, []RunDisplay{want}) {
		t.Errorf("organization rows = %#v\nwant %#v", got, []RunDisplay{want})
	}
}

func TestRunRowsSparse(t *testing.T) {
	run := Run{ID: 7, DeliverableID: uuid.MustParse(monthlyID), Sequence: 1, State: api.DeliverableRunStateOpen,
		RevisionMode: "new_period", RevisionID: uuid.MustParse(twinOneID), RenderError: new(string)}
	want := RunDisplay{
		ID: "7", DeliverableID: monthlyID, Sequence: "1", State: "open", RevisionMode: "new_period", Params: "-",
		Attempt: "0", RenderedAt: "-", RenderError: "-", StaleReason: "-", RevisionID: twinOneID,
		RenderedRevisionID: "-", SignedOffAt: "-", DeliveredAt: "-", CancelledAt: "-", CreatedAt: "-", UpdatedAt: "-",
	}
	if got := RunRows([]Run{run}); !reflect.DeepEqual(got, []RunDisplay{want}) {
		t.Errorf("rows = %#v\nwant %#v", got, []RunDisplay{want})
	}
}

func TestRunColumnsAreRunDisplayFields(t *testing.T) {
	data, err := json.Marshal(RunDisplay{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, column := range strings.Split(RunColumns, ",") {
		if _, ok := fields[column]; !ok {
			t.Errorf("default column %q is not a RunDisplay field", column)
		}
	}
}
