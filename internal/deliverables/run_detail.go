package deliverables

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ParseRunID parses a run id argument, refusing anything but a positive
// integer with exit 2.
func ParseRunID(arg string) (int, error) {
	id, err := strconv.Atoi(arg)
	if err != nil || id <= 0 {
		return 0, qerrors.NewBadUsagef("deliverable run id must be a positive integer, got %q", arg)
	}
	return id, nil
}

// RunDetail is a run, its readiness summary, and the deliverable and binding
// tree its text view names. Readiness is nil when it was not read.
type RunDetail struct {
	Run         *Run              `json:"run"`
	Readiness   *ReadinessSummary `json:"readiness"`
	Deliverable *Deliverable      `json:"-"`
	Revision    *RevisionDetail   `json:"-"`
}

// DescribeRun reads a run, its deliverable, its revision's binding tree and,
// unless the run is cancelled, its readiness summary. When the readiness read
// fails it returns the detail without readiness, and the error.
func (c *Client) DescribeRun(token string, runID int) (*RunDetail, error) {
	run, err := c.GetRun(token, runID)
	if err != nil {
		return nil, err
	}
	deliverable, err := c.Get(token, run.DeliverableID)
	if err != nil {
		return nil, err
	}
	revision, err := c.GetRevision(token, run.RevisionID)
	if err != nil {
		return nil, err
	}
	detail := &RunDetail{Run: run, Deliverable: deliverable, Revision: revision}
	if run.State == api.DeliverableRunStateCancelled {
		return detail, nil
	}
	readiness, err := c.Readiness(token, run.ID, ReadinessParams{Limit: 1})
	if err != nil {
		return detail, fmt.Errorf("read readiness of run %d: %w", run.ID, err)
	}
	detail.Readiness = &readiness.ReadinessSummary
	return detail, nil
}

// FormatRun renders a run's fields, its last render's totals, the table each
// of its bindings reads, and its readiness summary.
func FormatRun(d *RunDetail) string {
	run := d.Run
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-18s %s\n", label+":", value) }
	optional := func(label string, value *string) {
		if value != nil && *value != "" {
			line(label, cellText(*value))
		}
	}
	optionalTime := func(label string, value *time.Time) {
		if value != nil {
			line(label, value.Format(time.RFC3339))
		}
	}
	line("Run", strconv.Itoa(run.ID))
	line("Deliverable", runDeliverableText(d.Deliverable, run.DeliverableID))
	line("Issue", "#"+strconv.Itoa(run.Sequence))
	state := fmt.Sprintf("%s (%s)", cellText(string(run.State)), cellText(RunStateLabel(run.State)))
	if run.StaleReason != nil && *run.StaleReason != "" {
		state += ", reason " + cellText(*run.StaleReason)
	}
	line("State", state)
	line("Revision mode", cellText(string(run.RevisionMode)))
	line("Release", runReleaseText(d.Revision, run.RevisionID))
	if run.RenderedRevisionID != nil && *run.RenderedRevisionID != run.RevisionID {
		line("Rendered revision", run.RenderedRevisionID.String())
	}
	line("Params", cellText(RunParamsText(run.RunParamsTyped, run.RunParams)))
	line("Allow empty", yesNo(run.AllowEmpty))
	line("Render attempt", strconv.Itoa(run.AttemptID))
	rendered := "never"
	if run.RenderedAt != nil {
		rendered = run.RenderedAt.Format(time.RFC3339)
	}
	line("Rendered", rendered)
	optional("Render error", run.RenderError)
	optional("File signature", run.RenderedFileSignature)
	optional("Message", run.Msg)
	optionalTime("Signed off", run.SignedOffAt)
	optionalTime("Delivered", run.DeliveredAt)
	optional("Delivery note", run.DeliveryNote)
	optionalTime("Cancelled", run.CancelledAt)
	line("Created", timestamp(run.CreatedAt))
	line("Updated", timestamp(run.UpdatedAt))
	writeRunTotals(&b, run.ComputedTotals)
	writeRunSources(&b, run.Sources, newBindingNames(d.Revision))
	b.WriteString("\n")
	cancelled := run.State == api.DeliverableRunStateCancelled
	switch {
	case cancelled:
		b.WriteString("Readiness: not available for a cancelled run\n")
	case d.Readiness == nil:
		b.WriteString("Readiness: not read\n")
	default:
		b.WriteString(FormatReadinessSummary(d.Readiness))
	}
	if !cancelled {
		fmt.Fprintf(&b, "\nNext: qctl deliverable-run readiness %d\n", run.ID)
	}
	return b.String()
}

func runDeliverableText(item *Deliverable, id uuid.UUID) string {
	if item == nil {
		return id.String()
	}
	return fmt.Sprintf("%s (%s)", cellText(item.Slug), id)
}

func runReleaseText(revision *RevisionDetail, id uuid.UUID) string {
	if revision == nil || revision.DeliverableRevisionResponse == nil {
		return "revision " + id.String()
	}
	return fmt.Sprintf("%s (revision %s)", cellText(revision.Release), id)
}

const yesText = "yes"

func yesNo(v bool) string {
	if v {
		return yesText
	}
	return "no"
}

// writeRunTotals writes one line per computed total: a sheet's row count, or
// an aggregate cell's value.
func writeRunTotals(b *strings.Builder, totals *[]map[string]interface{}) {
	if totals == nil || len(*totals) == 0 {
		return
	}
	b.WriteString("\nTotals (last render):\n")
	for _, total := range *totals {
		sheet := totalText(total["sheet_name"])
		value := cellText(totalText(total["value"]))
		if total["cell_ref"] == nil {
			fmt.Fprintf(b, "  sheet %s: count = %s\n", readinessSheetName(sheet), value)
			continue
		}
		aggregate := cellText(totalText(total["func"]))
		if column := total["column"]; column != nil {
			aggregate += "(" + cellText(totalText(column)) + ")"
		}
		fmt.Fprintf(b, "  %s!%s: %s = %s\n", cellText(sheet), cellText(totalText(total["cell_ref"])), aggregate, value)
	}
}

func totalText(v interface{}) string {
	if v == nil {
		return nullText
	}
	return fmt.Sprint(v)
}

// bindingNames names a revision's sheet and cell bindings by id.
type bindingNames struct {
	sheets map[uuid.UUID]string
	cells  map[uuid.UUID]string
}

func newBindingNames(revision *RevisionDetail) bindingNames {
	names := bindingNames{sheets: map[uuid.UUID]string{}, cells: map[uuid.UUID]string{}}
	if revision == nil || revision.DeliverableRevisionResponse == nil {
		return names
	}
	for _, sheet := range copied(revision.SheetBindings) {
		names.sheets[sheet.ID] = "sheet " + readinessSheetName(sheet.SheetName)
	}
	for _, cell := range copied(revision.CellBindings) {
		names.cells[cell.ID] = "cell " + cellText(cell.SheetName) + "!" + cellText(cell.CellRef)
	}
	return names
}

// of names the binding source reads for.
func (n bindingNames) of(source api.RunSourceResponse) string {
	switch {
	case source.SheetBindingID != nil:
		if name, ok := n.sheets[*source.SheetBindingID]; ok {
			return name
		}
		return "sheet binding " + source.SheetBindingID.String()
	case source.CellBindingID != nil:
		if name, ok := n.cells[*source.CellBindingID]; ok {
			return name
		}
		return "cell binding " + source.CellBindingID.String()
	}
	return "-"
}

// writeRunSources writes a grid of the table each binding reads, then each
// source's warning.
func writeRunSources(b *strings.Builder, sources *[]api.RunSourceResponse, names bindingNames) {
	items := copied(sources)
	if len(items) == 0 {
		b.WriteString("\nSources: none\n")
		return
	}
	b.WriteString("\nSources:\n")
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  BINDING\tTABLE\tRESOLVED\tPERIOD\tFOLLOWS SERIES\tOVERRIDDEN")
	for _, source := range items {
		overridden := "-"
		if source.IsOverridden {
			overridden = yesText
			if source.OverrideReason != nil {
				overridden += `: "` + cellText(*source.OverrideReason) + `"`
			}
		}
		fmt.Fprintf(w, "  %s\t%d\t%d\t%s\t%s\t%s\n", names.of(source), source.DatasetID, source.ResolvedDatasetID,
			cellText(pointerText(source.PeriodKey)), yesNo(source.FollowsSeries), overridden)
	}
	_ = w.Flush()
	for _, source := range items {
		if source.Warning != nil && *source.Warning != "" {
			fmt.Fprintf(b, "  Warning (%s): %s\n", names.of(source), cellText(*source.Warning))
		}
	}
}
