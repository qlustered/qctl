package rule_versions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// newClientTestEnv wires temp HOME + mock transport + config/credential.
func newClientTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func TestSubmitRuleVersion_SendsVerbose(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"message": "ok",
				"added":   [][]interface{}{},
			})
		})

	client, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	verbose := true
	_, err = client.SubmitRuleVersion("test-token", RuleVersionSubmitRequest{
		FileText: "# rule",
		Verbose:  &verbose,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := gotBody["verbose"].(bool); !ok || !got {
		t.Errorf("request body verbose = %v, want true", gotBody["verbose"])
	}
}

func TestSubmitRuleVersion_VersionConflict(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Rule DedupeEmails v2.1.0 is in 'enabled' state and cannot be modified. Bump the release to create a new revision.\n\nChanges detected:\ncompact-diff-marker",
					"error_code": "ERR_RULE_VERSION_CONFLICT",
					"diff":       "full-diff-marker",
				},
			})
		})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.SubmitRuleVersion("test-token", RuleVersionSubmitRequest{FileText: "# rule"})
	if err == nil {
		t.Fatal("expected error")
	}
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *VersionConflictError, got %T: %v", err, err)
	}
	if conflict.Diff != "full-diff-marker" {
		t.Errorf("Diff = %q, want full-diff-marker", conflict.Diff)
	}
	if !strings.Contains(err.Error(), "cannot be modified") {
		t.Errorf("Error() should carry the backend msg, got: %v", err)
	}
}

func TestSubmitRuleVersion_NonConflictErrorPassesThrough(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg": "rule file rejected by preflight: import of os is not allowed",
				},
			})
		})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.SubmitRuleVersion("test-token", RuleVersionSubmitRequest{FileText: "import os"})
	if err == nil {
		t.Fatal("expected error")
	}
	var conflict *VersionConflictError
	if errors.As(err, &conflict) {
		t.Errorf("preflight failure must not be a VersionConflictError: %v", err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Errorf("expected preflight message, got: %v", err)
	}
}

// ruleRevisionSearchMock serves the rule-revision list the way atlas does:
// search_query is matched against name, slug and release, and NEVER against
// id (get_rule_revision_list's search_in_fields,
// ../atlas/capi/capi/views/rule_view.py). Any test that resolves a short ID
// against a mock which ignores search_query proves nothing.
func ruleRevisionSearchMock(t *testing.T, orgID string, all []RuleRevisionTiny) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	mock.RegisterHandler("GET", "/api/orgs/"+orgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("search_query")
		results := all
		if len(q) > 2 {
			results = nil
			for _, rev := range all {
				if strings.Contains(strings.ToLower(rev.Name), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Slug), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Release), strings.ToLower(q)) {
					results = append(results, rev)
				}
			}
		}
		total := len(results)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})
	t.Cleanup(mock.Close)
	return mock
}

func TestResolveRuleFullResolvesAShortIDTheBackendCannotSearch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const revID = "def8757c-1111-4222-8333-444444444444"
	all := []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "1.0.0", "enabled"),
	}
	mock := ruleRevisionSearchMock(t, testOrgID, all)

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resolved, err := c.ResolveRuleFull("token", "def8757c", "")
	if err != nil {
		t.Fatalf("ResolveRuleFull(short ID) = %v, want the full UUID", err)
	}
	if resolved.ID != revID {
		t.Errorf("resolved.ID = %q, want %q", resolved.ID, revID)
	}
}

func TestResolveRuleFullStillResolvesANameThroughTheServerSearch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const revID = "def8757c-1111-4222-8333-444444444444"
	all := []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "1.0.0", "enabled"),
		testutil.MakeRuleRevisionTiny("aaaa1111-2222-4333-8444-555555555555", "email_validator", "1.0.0", "enabled"),
	}
	mock := ruleRevisionSearchMock(t, testOrgID, all)

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resolved, err := c.ResolveRuleFull("token", "vehicle_aging_rule", "")
	if err != nil {
		t.Fatalf("ResolveRuleFull(name) = %v, want the full UUID", err)
	}
	if resolved.ID != revID {
		t.Errorf("resolved.ID = %q, want %q", resolved.ID, revID)
	}
}

func TestResolveRuleFullExactHexNameWinsBeforeBoundedPrefixScan(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const (
		prefixID = "decade00-1111-4222-8333-444444444444"
		namedID  = "bbbb1111-2222-4333-8444-555555555555"
	)
	prefixPage := make([]RuleRevisionTiny, resolutionListLimit)
	prefixPage[0] = testutil.MakeRuleRevisionTiny(prefixID, "decade-check", "1.0.0", "enabled")
	for i := 1; i < len(prefixPage); i++ {
		prefixPage[i] = testutil.MakeRuleRevisionTiny(
			fmt.Sprintf("bbbb%04d-1111-4222-8333-444444444444", i),
			fmt.Sprintf("rule_%d", i), "1.0.0", "enabled",
		)
	}

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var queries []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("search_query")
		queries = append(queries, query)
		results, total := prefixPage, resolutionListLimit+1
		if query == "decade" {
			results = []RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(namedID, "decade", "1.0.0", "enabled"),
			}
			total = 1
		}
		page := 1
		testutil.RespondJSON(w, http.StatusOK, RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resolved, err := c.ResolveRuleFull("token", "decade", "")
	if err != nil {
		t.Fatalf("ResolveRuleFull: %v", err)
	}
	if resolved.ID != namedID {
		t.Errorf("resolved.ID = %q, want exact-name revision %q", resolved.ID, namedID)
	}
	if got, want := strings.Join(queries, ","), "decade"; got != want {
		t.Errorf("search queries = %q, want %q (no bounded fallback after exact match)", got, want)
	}
}

func TestResolveRuleFullFindsANameOnALaterSearchPage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const revID = "def8757c-1111-4222-8333-444444444444"
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var pages []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		page := 1
		results := []RuleRevisionTiny{
			testutil.MakeRuleRevisionTiny("aaaa1111-2222-4333-8444-555555555555", "vehicle_aging_rule_helper", "1.0.0", "enabled"),
		}
		if r.URL.Query().Get("page") == "2" {
			page = 2
			results = []RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "2.0.0", "enabled"),
			}
		}
		total := 2
		testutil.RespondJSON(w, http.StatusOK, RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resolved, err := c.ResolveRuleFull("token", "vehicle_aging_rule", "2.0.0")
	if err != nil {
		t.Fatalf("ResolveRuleFull: %v", err)
	}
	if resolved.ID != revID {
		t.Errorf("resolved.ID = %q, want %q", resolved.ID, revID)
	}
	if got, want := strings.Join(pages, ","), "1,2"; got != want {
		t.Errorf("requested pages = %q, want %q", got, want)
	}
}

// After the exact-name search misses, a cache hit still avoids the bounded
// unfiltered catalog scan.
func TestResolveRuleFullFallsBackToTheShortIDCache(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const revID = "def8757c-1111-4222-8333-444444444444"
	mock := ruleRevisionSearchMock(t, testOrgID, nil)

	if err := aliasstore.RememberShortIDs(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, []string{revID}); err != nil {
		t.Fatalf("RememberShortIDs: %v", err)
	}

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resolved, err := c.ResolveRuleFull("token", "def8757c", "")
	if err != nil {
		t.Fatalf("ResolveRuleFull: %v", err)
	}
	if resolved.ID != revID {
		t.Errorf("resolved.ID = %q, want %q", resolved.ID, revID)
	}
}

// The mirror image of TestResolveRuleFullPrefersTheShortIDCache: an input
// shorter than a SHORT-ID is a name fragment, not an identifier, so the cache
// -- which stores UUIDs without names -- must not answer it. Here the catalog
// IS served, and "2024" must resolve to the rule named sales_2024 rather than
// to the cached revision whose UUID happens to start with those digits.
//
// It first checks for an exact "2024" name, then keeps the existing unfiltered
// fallback because sales_2024 is only a fuzzy match.
func TestResolveRuleFullDoesNotUseTheCacheBelowShortIDWidth(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const cachedID = "2024abcd-1111-4222-8333-444444444444"
	const namedID = "bbbb1111-2222-4333-8444-555555555555"
	mock := ruleRevisionSearchMock(t, testOrgID, []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(namedID, "sales_2024", "1.0.0", "enabled"),
	})

	// Wrap the catalog handler to record what the resolver asked for.
	const path = "/api/orgs/" + testOrgID + "/rule-revisions"
	catalog := mock.Handlers["GET "+path]
	var sentSearches []string
	mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		sentSearches = append(sentSearches, r.URL.Query().Get("search_query"))
		catalog(w, r)
	})

	if err := aliasstore.RememberShortIDs(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, []string{cachedID}); err != nil {
		t.Fatalf("RememberShortIDs: %v", err)
	}

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resolved, err := c.ResolveRuleFull("token", "2024", "")
	if err != nil {
		t.Fatalf("ResolveRuleFull: %v", err)
	}
	if resolved.ID != namedID {
		t.Errorf("resolved.ID = %q, want %q (the rule named sales_2024, not the cached UUID)", resolved.ID, namedID)
	}
	if got, want := strings.Join(sentSearches, ","), "2024,"; got != want {
		t.Errorf("search queries = %q, want %q", got, want)
	}
}

// Resolving from the catalog seeds the cache, so the next short-ID lookup can
// skip the bounded unfiltered scan after its exact-name check.
func TestResolveRuleFullSeedsTheShortIDCache(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const revID = "def8757c-1111-4222-8333-444444444444"
	all := []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "1.0.0", "enabled"),
	}
	mock := ruleRevisionSearchMock(t, testOrgID, all)

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.ResolveRuleFull("token", "def8757c", ""); err != nil {
		t.Fatalf("ResolveRuleFull: %v", err)
	}

	got, ok, err := aliasstore.ExpandShortID(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, "def8757c")
	if err != nil {
		t.Fatalf("ExpandShortID: %v", err)
	}
	if !ok || got != revID {
		t.Errorf("cache after resolution = (%q, %v), want (%q, true)", got, ok, revID)
	}
}

// ResolveRuleIDAny is the other closure over resolve(): `get rule` without
// --release goes through it, and it discards the release rather than demanding
// one when a name has several. Everything above drives ResolveRuleFull, so
// neither of resolve()'s short-ID branches is exercised through the lenient
// selector without this. Both are covered here.
func TestResolveRuleIDAnyExpandsAShortIDLocally(t *testing.T) {
	const revID = "def8757c-1111-4222-8333-444444444444"

	// The exact-name lookup returns nothing, after which the cache answers
	// without an unfiltered catalog scan.
	t.Run("from the short-ID cache", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		defer env.Cleanup()

		mock := ruleRevisionSearchMock(t, testOrgID, nil)

		if err := aliasstore.RememberShortIDs(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, []string{revID}); err != nil {
			t.Fatalf("RememberShortIDs: %v", err)
		}

		c, err := NewClient(mock.URL(), testOrgID, 0)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}

		got, err := c.ResolveRuleIDAny("token", "def8757c")
		if err != nil {
			t.Fatalf("ResolveRuleIDAny(short ID) = %v, want the full UUID", err)
		}
		if got != revID {
			t.Errorf("ResolveRuleIDAny = %q, want %q", got, revID)
		}
	})

	// Cold cache: an exact-name search misses, then the catalog is listed
	// unfiltered and the prefix is matched locally.
	t.Run("from the unfiltered catalog on a cold cache", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		defer env.Cleanup()

		mock := ruleRevisionSearchMock(t, testOrgID, []RuleRevisionTiny{
			testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "1.0.0", "enabled"),
			testutil.MakeRuleRevisionTiny("aaaa1111-2222-4333-8444-555555555555", "email_validator", "1.0.0", "enabled"),
		})

		const path = "/api/orgs/" + testOrgID + "/rule-revisions"
		catalog := mock.Handlers["GET "+path]
		var sentSearches []string
		mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
			sentSearches = append(sentSearches, r.URL.Query().Get("search_query"))
			catalog(w, r)
		})

		c, err := NewClient(mock.URL(), testOrgID, 0)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}

		got, err := c.ResolveRuleIDAny("token", "def8757c")
		if err != nil {
			t.Fatalf("ResolveRuleIDAny(short ID) = %v, want the full UUID", err)
		}
		if got != revID {
			t.Errorf("ResolveRuleIDAny = %q, want %q", got, revID)
		}
		if gotQueries, want := strings.Join(sentSearches, ","), "def8757c,"; gotQueries != want {
			t.Errorf("search queries = %q, want %q", gotQueries, want)
		}
	})
}

func TestResolveRuleIDAnyExactHexNameWinsBeforeCachedShortID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const (
		cachedID = "decade00-1111-4222-8333-444444444444"
		namedID  = "bbbb1111-2222-4333-8444-555555555555"
	)
	mock := ruleRevisionSearchMock(t, testOrgID, []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(namedID, "decade00", "1.0.0", "enabled"),
	})
	if err := aliasstore.RememberShortIDs(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, []string{cachedID}); err != nil {
		t.Fatalf("RememberShortIDs: %v", err)
	}

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.ResolveRuleIDAny("token", "decade00")
	if err != nil {
		t.Fatalf("ResolveRuleIDAny: %v", err)
	}
	if got != namedID {
		t.Errorf("ResolveRuleIDAny = %q, want exact-name revision %q", got, namedID)
	}
}

func TestResolveRuleFullExactNameProbeDoesNotMakeAnAmbiguousPrefixLookUnique(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := ruleRevisionSearchMock(t, testOrgID, []RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny("def8757c-1111-4222-8333-444444444444", "notes_def8757c", "1.0.0", "enabled"),
		testutil.MakeRuleRevisionTiny("def8757c-2222-4333-8444-555555555555", "email_validator", "1.0.0", "enabled"),
	})
	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.ResolveRuleFull("token", "def8757c", "")
	if err == nil {
		t.Fatal("expected the two UUID-prefix matches to be ambiguous")
	}
	if !strings.Contains(err.Error(), "Multiple rules match ID prefix 'def8757c'") {
		t.Errorf("error = %q, want ambiguous-prefix error", err)
	}
}

// Important finding 1: when the short-ID cache is cold and the org holds more
// rule revisions than resolutionListLimit, the unfiltered fallback listing is
// truncated. Resolving a short ID that only exists past the truncation point
// must not surface the plain "No rule found" message -- that message claims
// the rule does not exist, which is false; qctl simply never looked at it.
// The error must name the truncation and point at 'qctl get rules' so the
// user can seed the cache and retry.
func TestResolveRuleFullReportsTruncationInsteadOfAFalseNoMatch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// resolutionListLimit rows come back, but total_rows says the org holds
	// well over that -- the short ID resolved below is not among the rows
	// returned, only among the ones the truncation left unscanned.
	results := make([]RuleRevisionTiny, resolutionListLimit)
	for i := range results {
		results[i] = testutil.MakeRuleRevisionTiny(
			fmt.Sprintf("bbbb%04d-1111-4222-8333-444444444444", i),
			fmt.Sprintf("rule_%d", i), "1.0.0", "enabled")
	}

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search_query") != "" {
			total := 0
			page := 1
			testutil.RespondJSON(w, http.StatusOK, RuleRevisionList{TotalRows: &total, Page: &page})
			return
		}
		total := resolutionListLimit + 500
		page := 1
		testutil.RespondJSON(w, http.StatusOK, RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.ResolveRuleFull("token", "aaaa0000", "")
	if err == nil {
		t.Fatal("expected an error: 'aaaa0000' is not among the truncated listing")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", resolutionListLimit)) {
		t.Errorf("error should name the truncation (%d scanned), got: %v", resolutionListLimit, err)
	}
	if !strings.Contains(err.Error(), "get rules") {
		t.Errorf("error should point at 'qctl get rules' to seed the cache, got: %v", err)
	}
}

// The cache is scoped per org: a short ID learned in one org must not answer
// for another.
func TestResolveRuleFullDoesNotUseAnotherOrgsCache(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const otherOrg = "c3d4e5f6-a7b8-9012-cdef-345678901234"
	const revID = "def8757c-1111-4222-8333-444444444444"
	mock := ruleRevisionSearchMock(t, otherOrg, nil) // this org's catalog is empty

	if err := aliasstore.RememberShortIDs(mock.URL(), testOrgID, aliasstore.ResourceRuleRevision, []string{revID}); err != nil {
		t.Fatalf("RememberShortIDs: %v", err)
	}

	c, err := NewClient(mock.URL(), otherOrg, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.ResolveRuleFull("token", "def8757c", ""); err == nil {
		t.Error("ResolveRuleFull resolved a short ID cached under a different org")
	}
}

func TestGetRuleRevisionBindingPreviewSendsDatasetIDAndUpgradedFrom(t *testing.T) {
	mock := newClientTestEnv(t)
	const revID = "def8757c-1111-4222-8333-444444444444"
	const predecessorID = "def8757c-1111-4222-8333-555555555555"
	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+revID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": revID})
		})

	client, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	predecessor := uuid.MustParse(predecessorID)
	if _, err := client.GetRuleRevisionBindingPreview("test-token", revID, 42, &predecessor); err != nil {
		t.Fatalf("GetRuleRevisionBindingPreview: %v", err)
	}
	if got := query.Get("dataset_id"); got != "42" {
		t.Errorf("dataset_id = %q, want 42", got)
	}
	if got := query.Get("upgraded_from"); got != predecessorID {
		t.Errorf("upgraded_from = %q, want %s", got, predecessorID)
	}

	if _, err := client.GetRuleRevisionBindingPreview("test-token", revID, 42, nil); err != nil {
		t.Fatalf("GetRuleRevisionBindingPreview without upgradedFrom: %v", err)
	}
	if query.Has("upgraded_from") {
		t.Errorf("upgraded_from = %q sent for a nil upgradedFrom", query.Get("upgraded_from"))
	}
}
