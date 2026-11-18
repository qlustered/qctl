package table_series

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// DeadlineSchedule is a series' deadline schedule as the API returns it.
type DeadlineSchedule = api.TableSeriesDeadlineScheduleSchema

// DeadlineScheduleSpec is spec.deadline_schedule: each stored override, null when not overridden, and each switch,
// keyed as the PATCH body is.
type DeadlineScheduleSpec struct {
	MgaDeadlineDays           *int  `yaml:"mga_deadline_days" json:"mga_deadline_days"`
	BrokerDueDays             *int  `yaml:"broker_due_days" json:"broker_due_days"`
	FirstReminderDays         *int  `yaml:"first_reminder_days" json:"first_reminder_days"`
	FirstReminderEnabled      *bool `yaml:"first_reminder_enabled,omitempty" json:"first_reminder_enabled,omitempty"`
	DueDayReminderEnabled     *bool `yaml:"due_day_reminder_enabled,omitempty" json:"due_day_reminder_enabled,omitempty"`
	LateNoticeDays            *int  `yaml:"late_notice_days" json:"late_notice_days"`
	LateNoticeEnabled         *bool `yaml:"late_notice_enabled,omitempty" json:"late_notice_enabled,omitempty"`
	LateRepeatDays            *int  `yaml:"late_repeat_days" json:"late_repeat_days"`
	LateRepeatEnabled         *bool `yaml:"late_repeat_enabled,omitempty" json:"late_repeat_enabled,omitempty"`
	IssuesOpenDays            *int  `yaml:"issues_open_days" json:"issues_open_days"`
	IssuesOpenEnabled         *bool `yaml:"issues_open_enabled,omitempty" json:"issues_open_enabled,omitempty"`
	AtRiskDays                *int  `yaml:"at_risk_days" json:"at_risk_days"`
	AtRiskEnabled             *bool `yaml:"at_risk_enabled,omitempty" json:"at_risk_enabled,omitempty"`
	StillFailingFiles         *int  `yaml:"still_failing_files" json:"still_failing_files"`
	StillFailingDays          *int  `yaml:"still_failing_days" json:"still_failing_days"`
	StillFailingEnabled       *bool `yaml:"still_failing_enabled,omitempty" json:"still_failing_enabled,omitempty"`
	FailingSharePercent       *int  `yaml:"failing_share_percent" json:"failing_share_percent"`
	FailingShareEnabled       *bool `yaml:"failing_share_enabled,omitempty" json:"failing_share_enabled,omitempty"`
	ExceptionRequestedEnabled *bool `yaml:"exception_requested_enabled,omitempty" json:"exception_requested_enabled,omitempty"`
	RuleDisputedEnabled       *bool `yaml:"rule_disputed_enabled,omitempty" json:"rule_disputed_enabled,omitempty"`
	ReadyForReviewEnabled     *bool `yaml:"ready_for_review_enabled,omitempty" json:"ready_for_review_enabled,omitempty"`
	NotLoadedDays             *int  `yaml:"not_loaded_days" json:"not_loaded_days"`
	NotLoadedEnabled          *bool `yaml:"not_loaded_enabled,omitempty" json:"not_loaded_enabled,omitempty"`
}

// ScheduleSpecFromAPI returns the stored overrides and the switches of s.
func ScheduleSpecFromAPI(s *DeadlineSchedule) *DeadlineScheduleSpec {
	e := s.Escalations
	return &DeadlineScheduleSpec{
		MgaDeadlineDays:           copyInt(s.MgaDeadline.OverrideDays),
		BrokerDueDays:             copyInt(s.BrokerDue.OverrideDays),
		FirstReminderDays:         copyInt(s.FirstReminder.OverrideDays),
		FirstReminderEnabled:      boolPtr(s.FirstReminder.Enabled),
		DueDayReminderEnabled:     boolPtr(s.DueDayReminder.Enabled),
		LateNoticeDays:            copyInt(s.LateNotice.OverrideDays),
		LateNoticeEnabled:         boolPtr(s.LateNotice.Enabled),
		LateRepeatDays:            copyInt(s.LateRepeat.OverrideDays),
		LateRepeatEnabled:         boolPtr(s.LateRepeat.Enabled),
		IssuesOpenDays:            copyInt(s.IssuesOpen.OverrideDays),
		IssuesOpenEnabled:         boolPtr(s.IssuesOpen.Enabled),
		AtRiskDays:                copyInt(s.AtRisk.OverrideDays),
		AtRiskEnabled:             boolPtr(s.AtRisk.Enabled),
		StillFailingFiles:         copyInt(e.StillFailing.Files.OverrideValue),
		StillFailingDays:          copyInt(e.StillFailing.Days.OverrideValue),
		StillFailingEnabled:       boolPtr(e.StillFailing.Enabled),
		FailingSharePercent:       copyInt(e.FailingShare.Percent.OverrideValue),
		FailingShareEnabled:       boolPtr(e.FailingShare.Enabled),
		ExceptionRequestedEnabled: boolPtr(e.ExceptionRequested.Enabled),
		RuleDisputedEnabled:       boolPtr(e.RuleDisputed.Enabled),
		ReadyForReviewEnabled:     boolPtr(e.ReadyForReview.Enabled),
		NotLoadedDays:             copyInt(e.NotLoaded.Days.OverrideValue),
		NotLoadedEnabled:          boolPtr(e.NotLoaded.Enabled),
	}
}

func copyInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func boolPtr(b bool) *bool { return &b }

// scheduleKey returns the wire key of a DeadlineScheduleSpec field.
func scheduleKey(field reflect.StructField) string {
	key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return key
}

// scheduleBody returns the schedule PATCH body that makes stored match spec, or, when stored is nil, every non-null
// key of spec. A key left nil is sent only when nulls names it and stored has an override for it; any other key is
// sent when it differs from stored.
func scheduleBody(spec *DeadlineScheduleSpec, nulls map[string]bool, stored *DeadlineSchedule) map[string]any {
	var current *DeadlineScheduleSpec
	if stored != nil {
		current = ScheduleSpecFromAPI(stored)
	}
	body := map[string]any{}
	want := reflect.ValueOf(spec).Elem()
	fields := want.Type()
	for i := range fields.NumField() {
		value, key := want.Field(i), scheduleKey(fields.Field(i))
		if value.IsNil() && (current == nil || !nulls[key]) {
			continue
		}
		if current == nil {
			body[key] = value.Elem().Interface()
			continue
		}
		old := reflect.ValueOf(current).Elem().Field(i)
		if reflect.DeepEqual(value.Interface(), old.Interface()) {
			continue
		}
		if value.IsNil() {
			body[key] = nil
		} else {
			body[key] = value.Elem().Interface()
		}
	}
	return body
}

// GetDeadlineSchedule reads a series' deadline schedule with the dates of periodKey, or of the period being collected
// when periodKey is empty.
func (c *Client) GetDeadlineSchedule(token string, id uuid.UUID, periodKey string) (*DeadlineSchedule, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	params := &api.GetTableSeriesDeadlineScheduleParams{}
	if periodKey != "" {
		params.PeriodKey = &periodKey
	}
	resp, err := apiClient.API.GetTableSeriesDeadlineScheduleWithResponse(context.Background(), c.orgID, id, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get table series deadline schedule failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get table series deadline schedule failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series deadline schedule response")
	}
	return resp.JSON200, nil
}

// PreviewDeadlineSchedule returns the deadline schedule a PATCH of body would leave, with the dates of periodKey, or
// of the period being collected under the previewed schedule when periodKey is empty.
func (c *Client) PreviewDeadlineSchedule(token string, id uuid.UUID, periodKey string, body map[string]any) (*DeadlineSchedule, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode table series deadline schedule preview: %w", err)
	}
	params := &api.PreviewTableSeriesDeadlineScheduleParams{}
	if periodKey != "" {
		params.PeriodKey = &periodKey
	}
	resp, err := apiClient.API.PreviewTableSeriesDeadlineScheduleWithBodyWithResponse(context.Background(), c.orgID, id, params,
		"application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "preview table series deadline schedule failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "preview table series deadline schedule failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series deadline schedule preview response")
	}
	return resp.JSON200, nil
}
