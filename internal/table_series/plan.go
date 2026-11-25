package table_series

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

const createdID = "<created-series-id>"

// Request is one ordered series, membership or deadline schedule write.
type Request struct {
	Method    string
	SeriesID  uuid.UUID
	DatasetID int
	// Schedule marks the deadline schedule PATCH.
	Schedule bool
	Body     map[string]any
}

// Plan holds the writes shared by apply and diff.
type Plan struct {
	Current  *Series
	Name     string
	Create   bool
	Removals []int
	Requests []Request
	// RenamedByName lists, in manifest order, the members spec.members names by table name that the plan adds, moves
	// or gives a new period_key.
	RenamedByName []NamedMember
	// Schedule is the stored deadline schedule Prepare read, replaced by the schedule PATCH response once Apply sends
	// it; nil when neither happened.
	Schedule *DeadlineSchedule
	// frequency is the series frequency the membership writes run under.
	frequency api.SeriesFrequency
	// refrequency reports that the plan changes an existing series' frequency.
	refrequency bool
}

// NamedMember is a spec.members entry that names its table by name.
type NamedMember struct {
	Index     int
	Table     string
	DatasetID int
}

// ScheduleRequest returns the planned deadline schedule PATCH, or nil when none is planned.
func (p *Plan) ScheduleRequest() *Request {
	if n := len(p.Requests); n > 0 && p.Requests[n-1].Schedule {
		return &p.Requests[n-1]
	}
	return nil
}

// SchedulePreviewable reports whether the planned deadline schedule PATCH can be previewed: one is planned, the plan
// is no create and read the stored schedule, and the series' frequency does not change.
func (p *Plan) SchedulePreviewable() bool {
	return p.ScheduleRequest() != nil && !p.Create && p.Schedule != nil && !p.refrequency
}

// Prepare resolves tables and plans the series and membership changes.
func (c *Client) Prepare(token string, m *Manifest) (*Plan, error) {
	items, err := c.List(token)
	if err != nil {
		return nil, err
	}
	p := &Plan{Name: m.Metadata.Name, Create: true}
	for i := range items {
		if items[i].Name == m.Metadata.Name {
			p.Current = &items[i]
			p.Create = false
			break
		}
	}
	current, locked := indexMembers(items)
	dc := datasets.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	wanted := make([]Member, 0, len(m.Spec.Members))
	seen := map[int]bool{}
	for i, spec := range m.Spec.Members {
		id, err := dc.ResolveID(token, spec.Table)
		if err != nil {
			return nil, fmt.Errorf("spec.members[%d]: %w", i, err)
		}
		if id <= 0 || seen[id] {
			return nil, qerrors.NewBadUsagef("spec.members[%d]: invalid or duplicate table ID %d", i, id)
		}
		seen[id] = true
		wanted = append(wanted, desiredMember(m, i, id))
	}
	p.refrequency, err = p.planFrequency(m, seen)
	if err != nil {
		return nil, err
	}
	series := p.seriesRequest(m)
	if series != nil && !p.refrequency {
		p.Requests = append(p.Requests, *series)
		series = nil
	}
	p.planMembers(wanted, current, series)
	if err := refuseLockedMembers(p.Requests, locked); err != nil {
		return nil, err
	}
	p.planRenamedByName(m, wanted)
	p.planPeriodColumn(m)
	if err := c.planDeadlineSchedule(token, p, m); err != nil {
		return nil, err
	}
	return p, nil
}

// planRenamedByName sets p.RenamedByName from the planned member POSTs and PATCHes.
func (p *Plan) planRenamedByName(m *Manifest, wanted []Member) {
	written := map[int]bool{}
	for _, r := range p.Requests {
		if r.DatasetID != 0 && r.Method != http.MethodDelete {
			written[r.DatasetID] = true
		}
	}
	for i, spec := range m.Spec.Members {
		if _, err := strconv.Atoi(spec.Table); err != nil && written[wanted[i].DatasetID] {
			p.RenamedByName = append(p.RenamedByName, NamedMember{Index: i, Table: spec.Table, DatasetID: wanted[i].DatasetID})
		}
	}
}

// planDeadlineSchedule appends the deadline schedule PATCH, reading the stored schedule of an existing series first.
// A manifest without spec.deadline_schedule plans nothing.
func (c *Client) planDeadlineSchedule(token string, p *Plan, m *Manifest) error {
	if m.Spec.DeadlineSchedule == nil {
		return nil
	}
	if p.Current != nil {
		stored, err := c.GetDeadlineSchedule(token, p.Current.ID, "")
		if err != nil {
			return err
		}
		p.Schedule = stored
	}
	body := scheduleBody(m.Spec.DeadlineSchedule, m.scheduleNulls(), p.Schedule)
	if len(body) == 0 {
		return nil
	}
	p.Requests = append(p.Requests, Request{Method: http.MethodPatch, SeriesID: p.targetID(), Schedule: true, Body: body})
	return nil
}

// planFrequency sets the plan's frequency and reports whether the manifest changes an existing series' frequency.
func (p *Plan) planFrequency(m *Manifest, wanted map[int]bool) (bool, error) {
	p.frequency = api.Monthly
	if p.Current != nil {
		p.frequency = p.Current.Frequency
	}
	f := m.Spec.Frequency
	if f == nil {
		return false, nil
	}
	p.frequency = *f
	if p.Current == nil || *f == p.Current.Frequency {
		return false, nil
	}
	if err := refuseKeptMembers(p.Current, wanted); err != nil {
		return true, err
	}
	return true, refuseStaleFirstPeriod(p.Current, m)
}

// refuseStaleFirstPeriod refuses, with exit 2, a frequency change that leaves current's set first_period_key as it is.
func refuseStaleFirstPeriod(current *Series, m *Manifest) error {
	if current.FirstPeriodKey == nil {
		return nil
	}
	if _, ok := m.present["first_period_key"]; ok && !reflect.DeepEqual(m.Spec.FirstPeriodKey, current.FirstPeriodKey) {
		return nil
	}
	return qerrors.NewBadUsagef("spec.first_period_key: %q is a %s period key and the frequency changes to %s; "+
		"set spec.first_period_key to a key of the new frequency, or to null", *current.FirstPeriodKey, current.Frequency, *m.Spec.Frequency)
}

// refuseKeptMembers refuses, with exit 2, a frequency change whose manifest keeps any of current's tables.
func refuseKeptMembers(current *Series, wanted map[int]bool) error {
	var kept []int
	for _, member := range current.Members {
		if wanted[member.DatasetID] {
			kept = append(kept, member.DatasetID)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return qerrors.NewBadUsagef("spec.frequency: a series' frequency changes only while it has no tables, and the manifest keeps table IDs %v; "+
		"apply the new frequency, and a spec.first_period_key of it or null, with those members removed, then add them back under the new frequency's period keys", kept)
}

// indexMembers returns every member of items by table ID, and the locked period of each locked one.
func indexMembers(items []Series) (map[int]Member, map[int]*datasets.LockedPeriod) {
	current, locked := map[int]Member{}, map[int]*datasets.LockedPeriod{}
	for _, series := range items {
		for _, member := range series.Members {
			current[member.DatasetID] = member
			if member.IsLocked {
				locked[member.DatasetID] = &datasets.LockedPeriod{SeriesID: series.ID, SeriesName: series.Name, PeriodKey: member.PeriodKey}
			}
		}
	}
	return current, locked
}

// refuseLockedMembers refuses, with exit 2, requests that PATCH or DELETE a member in locked, naming each such member
// once in request order.
func refuseLockedMembers(requests []Request, locked map[int]*datasets.LockedPeriod) error {
	var refused []string
	seen := map[int]bool{}
	for _, r := range requests {
		period := locked[r.DatasetID]
		if period == nil || seen[r.DatasetID] || (r.Method != http.MethodPatch && r.Method != http.MethodDelete) {
			continue
		}
		seen[r.DatasetID] = true
		refused = append(refused, fmt.Sprintf("\n  table %d is locked %s; unlock it with `%s`", r.DatasetID, period, period.UnlockCommand()))
	}
	if len(refused) == 0 {
		return nil
	}
	return qerrors.NewBadUsagef("spec.members: the manifest moves, re-keys or removes a member whose period is locked, "+
		"which the server refuses; nothing was written:%s", strings.Join(refused, ""))
}

func (p *Plan) planPeriodColumn(m *Manifest) {
	if _, ok := m.present["period_column"]; !ok {
		return
	}
	if p.Current == nil {
		if m.Spec.PeriodColumn == nil {
			return
		}
	} else if reflect.DeepEqual(m.Spec.PeriodColumn, p.Current.PeriodColumn) &&
		reflect.DeepEqual(m.Spec.PeriodColumnKind, p.Current.PeriodColumnKind) {
		return
	}
	p.Requests = append(p.Requests, Request{Method: http.MethodPatch, SeriesID: p.targetID(), Body: map[string]any{
		"period_column": m.Spec.PeriodColumn, "period_column_kind": m.Spec.PeriodColumnKind,
	}})
}

func desiredMember(m *Manifest, index, id int) Member {
	return Member{DatasetID: id, PeriodKey: m.Spec.Members[index].PeriodKey}
}

func (p *Plan) targetID() uuid.UUID {
	if p.Current == nil {
		return uuid.Nil
	}
	return p.Current.ID
}

// seriesRequest returns the series POST, or the PATCH of the changed description, frequency and first_period_key,
// or nil when nothing changes.
func (p *Plan) seriesRequest(m *Manifest) *Request {
	body := map[string]any{"name": m.Metadata.Name}
	if p.Create {
		body["description"] = m.Spec.Description
		if m.Spec.Frequency != nil {
			body["frequency"] = *m.Spec.Frequency
		}
		if _, ok := m.present["first_period_key"]; ok {
			body["first_period_key"] = m.Spec.FirstPeriodKey
		}
		return &Request{Method: http.MethodPost, Body: body}
	}
	if _, ok := m.present["description"]; ok && !reflect.DeepEqual(m.Spec.Description, p.Current.Description) {
		body["description"] = m.Spec.Description
	}
	if m.Spec.Frequency != nil && *m.Spec.Frequency != p.Current.Frequency {
		body["frequency"] = *m.Spec.Frequency
	}
	if _, ok := m.present["first_period_key"]; ok && !reflect.DeepEqual(m.Spec.FirstPeriodKey, p.Current.FirstPeriodKey) {
		body["first_period_key"] = m.Spec.FirstPeriodKey
	}
	if len(body) == 1 {
		return nil
	}
	return &Request{Method: http.MethodPatch, SeriesID: p.Current.ID, Body: body}
}

func memberBody(member Member) map[string]any {
	return map[string]any{"period_key": member.PeriodKey}
}

func (p *Plan) memberRequest(member Member, old Member) Request {
	body := memberBody(member)
	if old.DatasetID == 0 {
		body["dataset_id"] = member.DatasetID
		return Request{Method: http.MethodPost, SeriesID: p.targetID(), DatasetID: member.DatasetID, Body: body}
	}
	var target any = p.targetID()
	if p.Create {
		target = createdID
	}
	body["series_id"] = target
	return Request{Method: http.MethodPatch, SeriesID: old.SeriesID, DatasetID: member.DatasetID, Body: body}
}

// planMembers plans the membership writes. A non-nil series request is placed after every removal and before any
// addition or move.
func (p *Plan) planMembers(wanted []Member, current map[int]Member, series *Request) {
	occupied, desired := map[string]int{}, map[string]bool{}
	keep := map[int]bool{}
	for _, member := range wanted {
		desired[member.PeriodKey], keep[member.DatasetID] = true, true
	}
	removals := p.planRemovals(keep, desired, occupied)
	if series != nil {
		p.Requests = append(append(p.Requests, removals...), *series)
		removals = nil
	}
	pending := append([]Member(nil), wanted...)
	for len(pending) > 0 {
		progress := false
		for i := 0; i < len(pending); {
			member := pending[i]
			if holder := occupied[member.PeriodKey]; holder != 0 && holder != member.DatasetID {
				i++
				continue
			}
			old := current[member.DatasetID]
			if old.DatasetID == 0 || old.SeriesID != p.targetID() || !reflect.DeepEqual(memberBody(old), memberBody(member)) {
				p.Requests = append(p.Requests, p.memberRequest(member, old))
			}
			if old.SeriesID == p.targetID() {
				delete(occupied, old.PeriodKey)
			}
			occupied[member.PeriodKey] = member.DatasetID
			member.SeriesID = p.targetID()
			current[member.DatasetID] = member
			pending = append(pending[:i], pending[i+1:]...)
			progress = true
		}
		if progress {
			continue
		}
		old := current[pending[0].DatasetID]
		temporary := old
		for n := 0; ; n++ {
			key := temporaryPeriodKey(p.frequency, n)
			if occupied[key] == 0 && !desired[key] {
				temporary.PeriodKey = key
				break
			}
		}
		p.Requests = append(p.Requests, p.memberRequest(temporary, old))
		delete(occupied, old.PeriodKey)
		occupied[temporary.PeriodKey] = old.DatasetID
		current[old.DatasetID] = temporary
	}
	p.Requests = append(p.Requests, removals...)
}

// temporaryPeriodKey returns the nth period key of frequency, counting back from 9999-12 (monthly) or 9999-Q4
// (quarterly); any other frequency counts as monthly.
func temporaryPeriodKey(frequency api.SeriesFrequency, n int) string {
	if frequency == api.Quarterly {
		return fmt.Sprintf("%04d-Q%d", 9999-n/4, 4-n%4)
	}
	return fmt.Sprintf("%04d-%02d", 9999-n/12, 12-n%12)
}

func (p *Plan) planRemovals(keep map[int]bool, desired map[string]bool, occupied map[string]int) []Request {
	var removals []Request
	if p.Current != nil {
		for _, member := range p.Current.Members {
			occupied[member.PeriodKey] = member.DatasetID
			if keep[member.DatasetID] {
				continue
			}
			p.Removals = append(p.Removals, member.DatasetID)
			r := Request{Method: http.MethodDelete, SeriesID: p.Current.ID, DatasetID: member.DatasetID}
			if desired[member.PeriodKey] {
				p.Requests = append(p.Requests, r)
				delete(occupied, member.PeriodKey)
			} else {
				removals = append(removals, r)
			}
		}
	}
	return removals
}

// WriteRequests prints the ordered request paths and bodies.
func (p *Plan) WriteRequests(out io.Writer) error {
	for _, r := range p.Requests {
		path := "/table-series"
		if !p.Create || r.DatasetID != 0 || r.Method != http.MethodPost {
			id := r.SeriesID.String()
			if r.SeriesID == uuid.Nil {
				id = createdID
			}
			path += "/" + id
		}
		if r.Schedule {
			path += "/deadline-schedule"
		}
		if r.DatasetID != 0 {
			path += "/members"
			if r.Method != http.MethodPost {
				path += "/" + strconv.Itoa(r.DatasetID)
			}
		}
		if _, err := fmt.Fprintf(out, "%s %s\n", r.Method, path); err != nil {
			return err
		}
		if r.Body != nil {
			var body bytes.Buffer
			encoder := json.NewEncoder(&body)
			encoder.SetEscapeHTML(false)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(r.Body); err != nil {
				return err
			}
			if _, err := io.WriteString(out, terminal.Sanitize(body.String())); err != nil {
				return err
			}
		}
	}
	return nil
}
