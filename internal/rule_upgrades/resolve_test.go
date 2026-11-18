//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
)

func revision(id uuid.UUID, release string, isDefault bool) api.RuleRevisionTinySchema {
	return api.RuleRevisionTinySchema{
		ID:        id,
		FamilyID:  testFamilyID,
		Name:      "phone_format",
		Slug:      "phone_format",
		Release:   release,
		IsDefault: isDefault,
		State:     api.RuleState("enabled"),
	}
}

func registerFamily(t *testing.T, mock *testutil.MockAPIServer, revs []api.RuleRevisionTinySchema) {
	t.Helper()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{
				Results:   revs,
				TotalRows: testutil.IntPtr(len(revs)),
			})
		})
	// Registered for EVERY revision id: which one the lenient resolver lands on
	// among a family's releases is its business, not this test's.
	for _, rev := range revs {
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String(),
			func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionsListSchema{
					FamilyID: testFamilyID, Name: "phone_format", Results: revs,
				})
			})
	}
}

// A family with several releases must resolve WITHOUT --release: both
// questions this package asks are family-scoped, so the strict resolver's
// "which release did you mean?" would be a demand for an irrelevant answer.
func TestResolveFamilyAcceptsAMultiReleaseFamily(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	old := uuid.MustParse("0190aaaa-9999-2222-3333-444444444444")
	registerFamily(t, mock, []api.RuleRevisionTinySchema{
		revision(old, "2.1.0", false),
		revision(testRevisionID, "3.0.0", true),
	})

	rv, err := rule_versions.NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	got, err := ResolveFamily(rv, "test-token", "phone_format")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != testFamilyID.String() {
		t.Errorf("family id = %q, want %q", got.ID, testFamilyID.String())
	}
	if got.Name != "phone_format" {
		t.Errorf("family name = %q", got.Name)
	}
	if d := got.Default(); d == nil || d.Release != "3.0.0" {
		t.Errorf("default release = %+v, want 3.0.0", d)
	}
	if r := got.ByRelease("2.1.0"); r == nil || r.IsDefault {
		t.Errorf("ByRelease(2.1.0) = %+v, want the non-default revision", r)
	}
	if got.ByRelease("9.9.9") != nil {
		t.Error("ByRelease of an absent release must be nil")
	}
}

// A family with no default revision is a real state (the backend returns
// new_default_revision_id: null for it), so Default() must say so rather than
// hand back an arbitrary release.
func TestResolveFamilyWithNoDefaultReturnsNil(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	registerFamily(t, mock, []api.RuleRevisionTinySchema{revision(testRevisionID, "1.0.0", false)})

	rv, err := rule_versions.NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	got, err := ResolveFamily(rv, "test-token", "phone_format")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Default() != nil {
		t.Errorf("Default() = %+v, want nil", got.Default())
	}
}
