package rule_families //nolint:revive // Established package path; renaming would break imports.

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestResolveSlugUsesExactSlugAcrossPages(t *testing.T) {
	for _, slug := range []string{"email-check", "ab"} {
		t.Run(slug, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			orgID, familyID := uuid.New(), uuid.New()
			var pages []string
			mock.RegisterHandler("GET", "/api/orgs/"+orgID.String()+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				pages = append(pages, q.Get("page"))
				if q.Get("order_by") != "slug" || (slug == "ab" && q.Has("search_query")) || (slug == "email-check" && q.Get("search_query") != slug) {
					t.Errorf("unexpected query: %v", q)
				}
				family := RuleFamilyItem{FamilyID: uuid.New(), Slug: slug + "-other", Name: slug}
				if q.Get("page") == "2" {
					family = RuleFamilyItem{FamilyID: familyID, Slug: slug, Name: "Different Display Name"}
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 2, "results": []RuleFamilyItem{family}})
			})
			got, err := NewClient(mock.Server.URL, orgID.String(), 0).ResolveSlug("token", slug)
			if err != nil || got != familyID || !reflect.DeepEqual(pages, []string{"1", "2"}) {
				t.Fatalf("id = %s, err = %v, pages = %v", got, err, pages)
			}
		})
	}
}

func TestResolveSlugPreservesHTTPExitCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	orgID := uuid.New()
	mock.RegisterHandler("GET", "/api/orgs/"+orgID.String()+"/rule-families", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "denied"})
	})
	_, err := NewClient(mock.Server.URL, orgID.String(), 0).ResolveSlug("token", "email-check")
	if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("err = %v, want exit 4", err)
	}
}
