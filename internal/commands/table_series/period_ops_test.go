package table_series //nolint:revive // Package name mirrors the command.

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestStayedSummaryStewardFixes(t *testing.T) {
	zero, three := 0, 3
	for name, tc := range map[string]struct {
		stayed api.SeriesPeriodStayedWithReplacedTableSchema
		want   string
	}{
		"none stayed":          {api.SeriesPeriodStayedWithReplacedTableSchema{StewardFixes: &zero}, ""},
		"fixes counted":        {api.SeriesPeriodStayedWithReplacedTableSchema{RowIgnores: 1, StewardFixes: &three}, "1 row ignores, 3 cells with steward fixes"},
		"fixes not counted":    {api.SeriesPeriodStayedWithReplacedTableSchema{CommentThreads: 2}, "2 comment threads, steward fixes not counted"},
		"only fixes uncounted": {api.SeriesPeriodStayedWithReplacedTableSchema{}, "steward fixes not counted"},
	} {
		if got := stayedSummary(tc.stayed); got != tc.want {
			t.Errorf("%s: stayedSummary = %q, want %q", name, got, tc.want)
		}
	}
}
