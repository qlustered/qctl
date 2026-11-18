package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

func TestResolveDatasetRuleIDExactNameUsesUniqueFilteredLookup(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("instance_name"); got != "email_check" {
			t.Errorf("instance_name = %q, want email_check", got)
		}
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Errorf("limit = %q, want 1 for the uniqueness-bounded lookup", got)
		}
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, DatasetRuleList{
			Results: []DatasetRuleTiny{
				makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
			},
			TotalRows: &total,
			Page:      &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.ResolveDatasetRuleID("token", 42, "email_check")
	if err != nil {
		t.Fatalf("ResolveDatasetRuleID: %v", err)
	}
	if want := "550e8400-e29b-41d4-a716-446655440000"; got != want {
		t.Errorf("resolved ID = %q, want %q", got, want)
	}
}

func TestResolveDatasetRuleIDHexNameUsesExactLookupBeforePrefixMatching(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	requests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("instance_name"); got != "cafe" {
			t.Errorf("instance_name = %q, want cafe", got)
		}
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, DatasetRuleList{
			Results: []DatasetRuleTiny{
				makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "cafe", "1.0.0"),
			},
			TotalRows: &total,
			Page:      &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.ResolveDatasetRuleID("token", 42, "cafe")
	if err != nil {
		t.Fatalf("ResolveDatasetRuleID: %v", err)
	}
	if want := "660e8400-e29b-41d4-a716-446655440001"; got != want {
		t.Errorf("resolved ID = %q, want %q", got, want)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (no prefix listing after exact match)", requests)
	}
}

func TestResolveDatasetRuleIDFindsShortIDOnALaterPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var pages []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("instance_name"); got != "" {
			if got != "550e8400" {
				t.Errorf("instance_name = %q, want 550e8400", got)
			}
			total, page := 0, 1
			testutil.RespondJSON(w, http.StatusOK, DatasetRuleList{TotalRows: &total, Page: &page})
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		page := 1
		rule := makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "phone_check", "1.0.0")
		if r.URL.Query().Get("page") == "2" {
			page = 2
			rule = makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0")
		}
		total := 2
		testutil.RespondJSON(w, http.StatusOK, DatasetRuleList{
			Results: []DatasetRuleTiny{rule}, TotalRows: &total, Page: &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.ResolveDatasetRuleID("token", 42, "550e8400")
	if err != nil {
		t.Fatalf("ResolveDatasetRuleID: %v", err)
	}
	if want := "550e8400-e29b-41d4-a716-446655440000"; got != want {
		t.Errorf("resolved ID = %q, want %q", got, want)
	}
	if gotPages, want := strings.Join(pages, ","), "1,2"; gotPages != want {
		t.Errorf("requested pages = %q, want %q", gotPages, want)
	}
}
