package table_series

import (
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
)

// Columns are the default table columns for series memberships.
const Columns = "name,frequency,period_key,table,table_id,period_start,period_end,is_locked,locked_by,locked_at"

// Display is a series member's table row.
type Display struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Frequency      string `json:"frequency"`
	FirstPeriodKey string `json:"first_period_key"`
	PeriodKey      string `json:"period_key"`
	Table          string `json:"table"`
	TableID        string `json:"table_id"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	IsLocked       string `json:"is_locked"`
	LockedBy       string `json:"locked_by"`
	LockedAt       string `json:"locked_at"`
}

// Rows returns one row per member, or one row for an empty series.
func Rows(items []Series) []Display {
	rows := make([]Display, 0)
	for _, item := range items {
		base := Display{ID: item.ID.String(), Name: item.Name, Frequency: string(item.Frequency), FirstPeriodKey: "-"}
		if item.FirstPeriodKey != nil {
			base.FirstPeriodKey = *item.FirstPeriodKey
		}
		if len(item.Members) == 0 {
			rows = append(rows, base)
		}
		for _, member := range item.Members {
			row := base
			row.PeriodKey = member.PeriodKey
			row.Table = member.DatasetName
			row.TableID = strconv.Itoa(member.DatasetID)
			row.PeriodStart = dateText(member.PeriodStart)
			row.PeriodEnd = dateText(member.PeriodEnd)
			row.IsLocked = strconv.FormatBool(member.IsLocked)
			row.LockedBy, row.LockedAt = "-", "-"
			if member.IsLocked {
				row.LockedBy = lockerText(member.LockedBy)
				if member.LockedAt != nil {
					row.LockedAt = member.LockedAt.UTC().Format(time.RFC3339)
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func lockerText(user *api.UserInfoTinySchema) string {
	if user == nil {
		return "-"
	}
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name
	}
	if user.Email != "" {
		return user.Email
	}
	return user.ID.String()
}

func dateText(date openapi_types.Date) string {
	return date.Format("2006-01-02")
}

// SubmissionPeriodColumns are the default columns for accessible periods.
const SubmissionPeriodColumns = "period_key,dataset_id,period_start,period_end,is_locked"

// SubmissionPeriodDisplay is an accessible period's table row.
type SubmissionPeriodDisplay struct {
	PeriodKey   string `json:"period_key"`
	DatasetID   string `json:"dataset_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	IsLocked    bool   `json:"is_locked"`
	Creatable   bool   `json:"creatable"`
}

// periodTableText is a period's table id, or "-" for a period with no table.
func periodTableText(id *int) string {
	if id == nil {
		return "-"
	}
	return strconv.Itoa(*id)
}

// SubmissionPeriodRows returns the periods in their given order.
func SubmissionPeriodRows(items []api.TableSeriesSubmissionPeriodSchema) []SubmissionPeriodDisplay {
	rows := make([]SubmissionPeriodDisplay, 0, len(items))
	for _, item := range items {
		rows = append(rows, SubmissionPeriodDisplay{
			PeriodKey: item.PeriodKey, DatasetID: periodTableText(item.DatasetID),
			PeriodStart: dateText(item.PeriodStart), PeriodEnd: dateText(item.PeriodEnd),
			IsLocked: item.IsLocked, Creatable: item.Creatable,
		})
	}
	return rows
}

// ReloadableFileColumns are the default columns for a period's reloadable files.
const ReloadableFileColumns = "id,key,cloud_source_id,uploaded_at,ever_loaded,clean_rows,bad_rows"

// ReloadableFileDisplay is a reloadable file's table row.
type ReloadableFileDisplay struct {
	ID            int    `json:"id"`
	Key           string `json:"key"`
	CloudSourceID int    `json:"cloud_source_id"`
	UploadedAt    string `json:"uploaded_at"`
	EverLoaded    bool   `json:"ever_loaded"`
	CleanRows     string `json:"clean_rows"`
	BadRows       string `json:"bad_rows"`
}

// ReloadableFileRows returns the files in their given order.
func ReloadableFileRows(items []ReloadableFile) []ReloadableFileDisplay {
	rows := make([]ReloadableFileDisplay, 0, len(items))
	for _, item := range items {
		key := "-"
		if item.Key != nil {
			key = *item.Key
		}
		rows = append(rows, ReloadableFileDisplay{
			ID: item.ID, Key: key, CloudSourceID: item.DataSourceModelID,
			UploadedAt: item.CreatedAt.UTC().Format(time.RFC3339), EverLoaded: item.IsEverLoaded,
			CleanRows: periodTableText(item.CleanRowsCount), BadRows: periodTableText(item.BadRowsCount),
		})
	}
	return rows
}
