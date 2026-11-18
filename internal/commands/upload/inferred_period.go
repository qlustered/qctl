package upload

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
)

// inferPeriod returns the period_key fileName names for cloud source cs and
// what the server read it from. The key is read in the frequency of cs's
// series when the caller's uploadable series list cs, and in the frequency
// the file name suggests otherwise. A file name that names no single period
// is an exit-2 error.
func inferPeriod(ctx *cmdutil.CommandContext, cs cmdutil.ResolvedCloudSource, fileName string) (string, *api.PeriodSource, error) {
	client := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	uploadable, err := client.ListUploadable(token)
	if err != nil {
		return "", nil, fmt.Errorf("failed to list uploadable table series: %w", err)
	}
	var frequency *api.SeriesFrequency
	for _, series := range uploadable {
		if series.DataSourceModelID == cs.ID {
			f := series.Frequency
			frequency = &f
			break
		}
	}
	suggestion, err := client.SuggestPeriod(token, fileName, frequency)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read a period from file name '%s': %w", fileName, err)
	}
	if suggestion.PeriodKey == nil {
		return "", nil, qerrors.NewBadUsagef(
			"file name '%s' names no single %s period; pass --period, or --table with the period's table",
			fileName, suggestion.Frequency)
	}
	return *suggestion.PeriodKey, suggestion.PeriodSource, nil
}

// inferredPeriodText renders where an inferred period_key was read from.
func inferredPeriodText(source *api.PeriodSource) string {
	if source != nil && *source == api.PeriodSourceExportDate {
		return "inferred from the export date in the file name"
	}
	return "inferred from the file name"
}
