package get

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
)

type tablesOverviewDisplay struct {
	Reference               string `json:"reference"`
	Kind                    string `json:"kind"`
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Series                  string `json:"series"`
	Period                  string `json:"period"`
	LatestTable             string `json:"latest_table"`
	PeriodCount             *int   `json:"period_count"`
	PeriodsNeedingAttention *int   `json:"periods_needing_attention"`
	State                   string `json:"state"`
	DestinationName         string `json:"destination_name"`
	CleanRowsCount          *int   `json:"clean_rows_count"`
	BadRowsCount            *int   `json:"bad_rows_count"`
	AttentionScore          int    `json:"attention_score"`
	TopAlert                string `json:"top_alert"`
	AlertTable              string `json:"alert_table"`
	AlertPeriod             string `json:"alert_period"`
	UnresolvedAlerts        int    `json:"unresolved_alerts"`
	ApprovedRowsCount       *int   `json:"approved_rows_count"`
	OnHoldRowsCount         *int   `json:"on_hold_rows_count"`
	RowsWithIssuesCount     *int   `json:"rows_with_issues_count"`
	NeedsApproval           bool   `json:"needs_approval"`
}

func tablesOverviewRows(items []api.TablesOverviewListSchema_Results_Item) ([]tablesOverviewDisplay, error) {
	rows := make([]tablesOverviewDisplay, 0, len(items))
	for _, item := range items {
		body, err := item.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("decode overview row: %w", err)
		}
		var tag struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &tag); err != nil {
			return nil, fmt.Errorf("decode overview row kind: %w", err)
		}
		var row tablesOverviewDisplay
		switch tag.Kind {
		case "table": //nolint:goconst // Keep the row-kind tags beside the switch that decodes them.
			table, err := item.AsTablesOverviewTableSchema()
			if err != nil {
				return nil, fmt.Errorf("decode overview table: %w", err)
			}
			row = overviewTableRow(table)
		case "series":
			series, err := item.AsTablesOverviewSeriesSchema()
			if err != nil {
				return nil, fmt.Errorf("decode overview series: %w", err)
			}
			row = overviewSeriesRow(series)
		default:
			return nil, fmt.Errorf("unexpected tables overview row kind %q", tag.Kind)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func overviewTableRow(table api.TablesOverviewTableSchema) tablesOverviewDisplay {
	row := tablesOverviewDisplay{
		Reference: fmt.Sprintf("table/%d", table.ID), Kind: table.Kind, ID: strconv.Itoa(table.ID), Name: table.Name,
		Series: "-", Period: "-", LatestTable: "-", State: string(table.State), DestinationName: table.DestinationName,
		CleanRowsCount: table.CleanRowsCount, BadRowsCount: table.BadRowsCount, AttentionScore: table.AttentionScore,
		TopAlert: topAlertSummary(table.TopAlert), AlertTable: "-", AlertPeriod: "-", UnresolvedAlerts: table.UnresolvedAlerts,
		ApprovedRowsCount: table.ApprovedRowsCount, OnHoldRowsCount: table.OnHoldRowsCount,
		RowsWithIssuesCount: table.RowsWithIssuesCount, NeedsApproval: table.NeedsApproval,
	}
	series, period := datasets.SummarizeSeries(table.Series, table.Period)
	if series != nil {
		row.Series, row.Period = series.Name, period.Display()
	}
	if table.TopAlert != nil && table.TopAlert.AlertItemID > 0 {
		row.AlertTable = row.Reference
		if series != nil {
			row.AlertPeriod = period.PeriodKey
		}
	}
	return row
}

func overviewSeriesRow(series api.TablesOverviewSeriesSchema) tablesOverviewDisplay {
	period := datasets.PeriodSummary{PeriodKey: series.LatestPeriod.PeriodKey, IsLocked: series.LatestPeriod.IsLocked}
	alert := series.TopAlert
	summary := "-"
	if alert != nil {
		summary = topAlertSummary(&api.DataSetTopAlertSchema{
			AlertItemID: alert.AlertItemID, IssueType: alert.IssueType, Count: alert.Count, BlocksIngestion: alert.BlocksIngestion,
		})
	}
	row := tablesOverviewDisplay{
		Reference: "table-series/" + series.ID.String(), Kind: series.Kind, ID: series.ID.String(), Name: series.Name,
		Series: series.Name, Period: period.Display(), LatestTable: fmt.Sprintf("table/%d", series.LatestPeriod.DatasetID),
		PeriodCount: &series.PeriodCount, PeriodsNeedingAttention: &series.PeriodsNeedingAttention,
		State: "-", DestinationName: "-", CleanRowsCount: series.CleanRowsCount, BadRowsCount: series.BadRowsCount,
		AttentionScore: series.AttentionScore, TopAlert: summary, AlertTable: "-", AlertPeriod: "-",
		UnresolvedAlerts: series.UnresolvedAlerts, ApprovedRowsCount: series.ApprovedRowsCount, OnHoldRowsCount: series.OnHoldRowsCount,
		RowsWithIssuesCount: series.RowsWithIssuesCount, NeedsApproval: series.NeedsApproval,
	}
	if alert != nil && alert.AlertItemID > 0 {
		row.AlertTable, row.AlertPeriod = fmt.Sprintf("table/%d", alert.DatasetID), alert.PeriodKey
		row.TopAlert += fmt.Sprintf(" [%s, period %s]", row.AlertTable, row.AlertPeriod)
	}
	return row
}

func topAlertSummary(alert *api.DataSetTopAlertSchema) string {
	if alert == nil || alert.AlertItemID <= 0 {
		return "-"
	}
	summary := string(alert.IssueType)
	if alert.BlocksIngestion {
		summary += " [new files on hold]"
	}
	return summary
}
