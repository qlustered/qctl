package get

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetTableRulesCommand_FilterColumn(t *testing.T) {
	description := "  Completed bookings  "
	for _, tt := range []struct {
		name   string
		filter *api.RowFilter
		args   []string
		want   []string
	}{
		{"no filter", nil, []string{"--columns", "filter"}, []string{"FILTER"}},
		{"undescribed filter", &api.RowFilter{}, []string{"--columns", "filter"}, []string{"FILTER", "Checks", "only", "some", "rows"}},
		{"described filter", &api.RowFilter{Description: &description}, []string{"--columns", "filter"}, []string{"FILTER", "Completed", "bookings"}},
		{"verbose opt-in", &api.RowFilter{Description: &description}, []string{"--columns", "filter", "-v"}, []string{"FILTER", "Completed", "bookings"}},
		{"default columns", &api.RowFilter{Description: &description}, nil, nil},
		{"verbose default columns", &api.RowFilter{Description: &description}, []string{"-v"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			rule := sampleDatasetRuleTiny()
			rule.RowFilter = tt.filter
			registerTableRulesListHandler(mock, "1", []dataset_rules.DatasetRuleTiny{rule})

			cmd := setupTableRulesTestCommand()
			cmd.SetArgs(append([]string{"get", "table-rules", "--table-id", "1"}, tt.args...))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if tt.want != nil {
				if got := strings.Fields(stdout.String()); !slices.Equal(got, tt.want) {
					t.Errorf("output fields = %v, want %v", got, tt.want)
				}
			} else if strings.Contains(stdout.String(), "FILTER") || strings.Contains(stdout.String(), "Completed bookings") {
				t.Errorf("default output includes the opt-in filter: %s", stdout.String())
			}
			wantRequests := []string{"GET /api/orgs/" + testOrgID + "/datasets/1/dataset-rules"}
			if got := mock.Requests(); !slices.Equal(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
		})
	}
}

func TestGetTableRuleCommand_FilterColumn(t *testing.T) {
	mock := newTableRulesTestEnv(t)
	detail := sampleDatasetRuleDetail()
	description := "Completed bookings"
	detail.RowFilter = &api.RowFilter{Description: &description}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", detail.ID.String(), "--table-id", "1", "--columns", "filter"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(stdout.String()), []string{"FILTER", "Completed", "bookings"}; !slices.Equal(got, want) {
		t.Errorf("output fields = %v, want %v", got, want)
	}
}
