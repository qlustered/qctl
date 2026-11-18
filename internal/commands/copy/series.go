package copycmd

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

const (
	seriesFlag    = "series"
	periodKeyFlag = "period-key"
)

func readCopySeriesFlags(cmd *cobra.Command) (string, *api.DatasetCopySeriesRequest, error) {
	seriesGiven, keyGiven := cmd.Flags().Changed(seriesFlag), cmd.Flags().Changed(periodKeyFlag)
	if !seriesGiven && !keyGiven {
		return "", nil, nil
	}
	if !seriesGiven || !keyGiven {
		return "", nil, qerrors.NewBadUsage("--series and --period-key are required together")
	}
	for _, flag := range []string{nameFlag, tableNameFlag} {
		if cmd.Flags().Changed(flag) {
			return "", nil, qerrors.NewBadUsagef(
				"--%s cannot be used with --%s: the server names the new table from the series and its period", flag, seriesFlag)
		}
	}
	ref, _ := cmd.Flags().GetString(seriesFlag)
	if err := table_series.ValidateReference(ref); err != nil {
		return "", nil, fmt.Errorf("--series: %w", err)
	}
	key, _ := cmd.Flags().GetString(periodKeyFlag)
	if err := table_series.ValidatePeriodKey(key); err != nil {
		return "", nil, fmt.Errorf("--period-key: %w", err)
	}
	return ref, &api.DatasetCopySeriesRequest{PeriodKey: key}, nil
}
