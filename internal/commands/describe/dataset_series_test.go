package describe

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeTableSeriesAndPeriod(t *testing.T) {
	const series = `"series":{"id":"11111111-2222-3333-4444-555555555555","name":"Monthly reports"},`
	for _, tc := range []struct {
		name, membership string
		want             []string
		locked, member   bool
	}{
		{
			name: "locked with metadata", member: true, locked: true,
			membership: series + `"period":{"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
			"is_locked":true,"locked_at":"2026-10-02T11:00:00+02:00",
			"locked_by":{"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com"}}`,
			want: []string{"Series:       Monthly reports (id: 11111111-2222-3333-4444-555555555555) — period 2026-09 (locked)",
				"Period dates: 2026-09-01 — 2026-09-30", "Locked by:    Ada Lovelace <ada@example.com>", "Locked at:    2026-10-02T09:00:00Z"},
		},
		{
			name: "locked without metadata", member: true, locked: true,
			membership: series + `"period":{"period_key":"2026-08","period_start":"2026-08-01","period_end":"2026-08-31","is_locked":true,"locked_by":null,"locked_at":null}`,
			want:       []string{"period 2026-08 (locked)", "Period dates: 2026-08-01 — 2026-08-31", "Locked by:    (unknown)", "Locked at:    (unknown)"},
		},
		{
			name: "unlocked", member: true,
			membership: series + `"period":{"period_key":"2026-Q3","period_start":"2026-07-01","period_end":"2026-09-30","is_locked":false,"locked_by":null,"locked_at":null}`,
			want:       []string{"period 2026-Q3 (unlocked)", "Period dates: 2026-07-01 — 2026-09-30"},
		},
		{name: "standalone null", membership: `"series":null,"period":null`},
		{name: "older server without membership"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			if tc.membership != "" {
				body := strings.Replace(summaryDatasetJSON, "{", "{"+tc.membership+",", 1)
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
				})
			}
			out, _, err := runDescribeTable(t, "12")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q: %s", want, out)
				}
			}
			if strings.Contains(out, "Series:") != tc.member || strings.Contains(out, "Locked by:") != tc.locked {
				t.Errorf("membership or lock shown when absent: %s", out)
			}
			for _, format := range []string{"json", "yaml"} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				payload := structuredPayload(t, format, out)
				if !tc.member {
					if payload["series"] != nil || payload["period"] != nil {
						t.Fatalf("standalone membership = %v, %v", payload["series"], payload["period"])
					}
					continue
				}
				seriesInfo, ok := payload["series"].(map[string]any)
				if !ok || seriesInfo["name"] != "Monthly reports" {
					t.Fatalf("series = %v", payload["series"])
				}
				period, ok := payload["period"].(map[string]any)
				if !ok || period["is_locked"] != tc.locked || period["period_key"] == nil {
					t.Fatalf("period = %v", payload["period"])
				}
				if tc.name == "locked with metadata" {
					locker, ok := period["locked_by"].(map[string]any)
					if !ok || locker["email"] != "ada@example.com" || period["locked_at"] != "2026-10-02T09:00:00Z" || period["period_start"] != "2026-09-01" {
						t.Fatalf("lock metadata lost: %v", period)
					}
				} else if period["locked_by"] != nil || period["locked_at"] != nil {
					t.Fatalf("unknown lock metadata fabricated: %v", period)
				}
			}
		})
	}
}
