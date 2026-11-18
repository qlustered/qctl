package guide

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
)

func TestTopicsRegistryLoads(t *testing.T) {
	if len(topics) == 0 {
		t.Fatal("expected at least one registered topic")
	}
	for _, top := range topics {
		if top.Name == "" {
			t.Error("topic has empty Name")
		}
		if top.Summary == "" {
			t.Errorf("topic %q has empty Summary", top.Name)
		}
		body, err := top.body()
		if err != nil {
			t.Errorf("topic %q body failed to load: %v", top.Name, err)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("topic %q has empty body", top.Name)
		}
	}
}

func TestNoOrphanTopicFiles(t *testing.T) {
	entries, err := topicsFS.ReadDir("topics")
	if err != nil {
		t.Fatalf("reading embedded topics dir: %v", err)
	}
	referenced := map[string]bool{}
	for _, top := range topics {
		referenced[top.File] = true
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := "topics/" + e.Name()
		if !referenced[path] {
			t.Errorf("embedded topic file %q is not referenced by any registry entry", path)
		}
	}
}

func TestLookupUnknownReturnsFalse(t *testing.T) {
	if _, ok := lookup("does-not-exist"); ok {
		t.Error("expected lookup of unknown topic to return false")
	}
}

func TestOnboardTableSummaryMentionsDataset(t *testing.T) {
	top, ok := lookup("onboard-table")
	if !ok {
		t.Fatal("onboard-table topic should exist")
	}
	if !strings.Contains(top.Summary, "dataset") {
		t.Errorf("onboard-table summary should mention dataset; got %q", top.Summary)
	}
}

func TestUnblockTableTopicIsRegisteredAndRenders(t *testing.T) {
	var found *topic
	for i := range topics {
		if topics[i].Name == "unblock-table" {
			found = &topics[i]
			break
		}
	}
	if found == nil {
		t.Fatal("unblock-table is not in the topics registry")
	}

	body, err := found.body()
	if err != nil {
		t.Fatalf("unblock-table body: %v", err)
	}
	for _, want := range []string{
		"qctl describe table-schema",
		"qctl get rule-suggestions",
		"qctl get latest-alert",
		"qctl alert resolve",
		"PENDING_SUGGESTIONS_BLOCK_MIGRATION",
		"SEVERE_RULE_BUG_RULE_STILL_ACTIVE",
		"--action disable_rule",
		"unless any draft migration exists",
		"even a confirmed draft already queued",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("topic must name %q -- it is the sequence's whole point", want)
		}
	}
}

// TestLookupResolvesAliases: "add-rule-to-column" is what a reader looking for
// this task types. It resolves to the topic rather than dead-ending.
func TestLookupResolvesAliases(t *testing.T) {
	got, ok := lookup("add-rule-to-column")
	if !ok {
		t.Fatal("lookup(add-rule-to-column) found nothing")
	}
	if got.Name != "instantiate-rule" {
		t.Errorf("lookup(add-rule-to-column) = %q, want instantiate-rule", got.Name)
	}
}

// TestAliasesStayOutOfTheIndex: an alias exists so a reasonable guess resolves,
// not to grow the menu a reader has to scan.
func TestAliasesStayOutOfTheIndex(t *testing.T) {
	for _, name := range TopicNames() {
		if name == "add-rule-to-column" {
			t.Error("an alias leaked into TopicNames(); the guide index would list it twice")
		}
	}
}

// TestNoAliasShadowsATopicName: an alias colliding with a canonical name would
// make lookup order decide which topic renders.
func TestNoAliasShadowsATopicName(t *testing.T) {
	canonical := map[string]bool{}
	for _, t2 := range topics {
		canonical[t2.Name] = true
	}
	for _, t2 := range topics {
		for _, a := range t2.Aliases {
			if canonical[a] {
				t.Errorf("topic %q has alias %q, which is another topic's name", t2.Name, a)
			}
		}
	}
}

// TestDiscoverCapabilitiesNamesTheInbox: the inbox is the one surface that
// tells an agent it has been ASKED something, and the cold-start topic is
// where an agent with no priors looks. A capability nothing points at is a
// capability nobody uses.
func TestDiscoverCapabilitiesNamesTheInbox(t *testing.T) {
	top, ok := lookup("discover-capabilities")
	if !ok {
		t.Fatal("discover-capabilities topic should exist")
	}
	body, err := top.body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	for _, want := range []string{"qctl get notifications --count", "qctl guide check-inbox"} {
		if !strings.Contains(body, want) {
			t.Errorf("discover-capabilities must name %q", want)
		}
	}
}

// TestCheckInboxTopicCoversBothAxes: a guide that documents only one of read
// and dismissed teaches the coupling the product does not have.
func TestCheckInboxTopicCoversBothAxes(t *testing.T) {
	top, ok := lookup("check-inbox")
	if !ok {
		t.Fatal("check-inbox is not in the topics registry")
	}
	body, err := top.body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	for _, want := range []string{
		"qctl get notifications",
		"qctl get notifications --count",
		"qctl notification read --all",
		"qctl notification dismiss --all --yes",
		"no un-dismiss",
		"needs_you",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("check-inbox must name %q -- it is the topic's whole point", want)
		}
	}
}

// TestCheckInboxRoutesDataReviewTypes: the five data_review_* types do not
// share a target. Only two carry a data_review_id; a group assignment is
// reached through its review_group_id and a clean-affected nudge through its
// history_id, so pointing every one of them at describe data-review would
// send a reader to a command they cannot fill in.
func TestCheckInboxRoutesDataReviewTypes(t *testing.T) {
	top, ok := lookup("check-inbox")
	if !ok {
		t.Fatal("check-inbox is not in the topics registry")
	}
	body, err := top.body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	for _, want := range []string{
		"qctl describe data-review <data_review_id>",
		"qctl get data-reviews --table-id <dataset_id> --group <review_group_id>",
		"qctl get data <dataset_id> --history-ids <history_id>",
		"data_review_group_assigned",
		"data_review_clean_affected",
		"qctl guide review-data",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("check-inbox must name %q", want)
		}
	}
}

// guide authenticate is where the precedence chain is explained, so it is
// where the consequence of that chain belongs: the scope is fixed, and a
// failure inside it is not a reason to try another organization.
func TestAuthenticateTopicStatesOrgScopeIsFixed(t *testing.T) {
	top, ok := lookup("authenticate")
	if !ok {
		t.Fatal("authenticate topic should exist")
	}
	body, err := top.body()
	if err != nil {
		t.Fatalf("authenticate body: %v", err)
	}
	// The three precedence phrases come from the constants the note itself
	// renders, not from copies of their text: rewording a config.OrgSource
	// then has to reword the topic too, instead of leaving this test green
	// against prose that no longer matches what qctl prints.
	want := []string{
		"no cross-organization fallback",
		"Scoped to organization",
		"scope expansion nobody asked for",
	}
	for _, src := range []config.OrgSource{
		config.OrgSourceFlag, config.OrgSourceEnv, config.OrgSourceContext,
	} {
		want = append(want, string(src))
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("authenticate topic must say %q -- it is what stops the wandering", w)
		}
	}
}

// TestEditDataTopicWalksTheWholeLoop: the topic exists to teach the sequence
// the four data-editing commands make together — find, preview, write, pick a
// value, check the key, see what the write set in motion, then read what it
// recorded — so each leg has to be named by its runnable line, and the two
// facts that cost real time (the mandatory preview and the countless write)
// have to be said outright.
func TestEditDataTopicWalksTheWholeLoop(t *testing.T) {
	top, ok := lookup("edit-data")
	if !ok {
		t.Fatal("edit-data is not in the topics registry")
	}
	body, err := top.body()
	if err != nil {
		t.Fatalf("edit-data body: %v", err)
	}
	for _, want := range []string{
		"qctl get data orders --is-bad-data --search-query",
		"qctl rows search-replace orders --search \"N/A\" --replace \"\" --dry-run",
		"--field notes --yes",
		"qctl rows set-cell orders --row-id 1042 --row-id 1043 --field state --value CA",
		"qctl get value-recommendations orders --field state --value Calif. --alert-item-id 8817",
		"qctl rows check-unique orders --column region --column order_id",
		"qctl describe table orders",
		"qctl get history --table orders --model TableValueOverwrite",
		"qctl get data orders --is-bad-data --show-row-logs",
		"The preview is the",
		"reports no count of any kind",
		"queued for re-validation",
		"qctl guide triage-alerts",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edit-data must name %q -- it is one leg of the loop", want)
		}
	}
}

// TestEditDataSitsAfterInspectData: registry order is index order, and the
// editing guide belongs beside the reading guide it starts from rather than
// wherever the slice happened to end.
func TestEditDataSitsAfterInspectData(t *testing.T) {
	names := TopicNames()
	pos := map[string]int{}
	for i, n := range names {
		pos[n] = i
	}
	inspect, ok1 := pos["inspect-data"]
	edit, ok2 := pos["edit-data"]
	if !ok1 || !ok2 {
		t.Fatalf("inspect-data registered: %v, edit-data registered: %v", ok1, ok2)
	}
	if edit != inspect+1 {
		t.Errorf("edit-data is at index %d, inspect-data at %d; want edit-data immediately after", edit, inspect)
	}
}

// TestSummariesCarryTheWordsAgentsSearchFor: the index is what an agent scans
// to pick a guide, by the words of its task. A summary without them hides its
// guide; triage-alerts' once named none of "alert", "warning" or "resolve".
func TestSummariesCarryTheWordsAgentsSearchFor(t *testing.T) {
	for name, want := range map[string][]string{
		"triage-alerts":        {"alert", "warning", "resolve"},
		"inspect-data":         {"rows", "quarantined", "export"},
		"manage-files":         {"upload", "download", "ingest"},
		"request-review":       {"rule review"},
		"review-rules":         {"rule review"},
		"review-data":          {"data review"},
		"data-review-policy":   {"data review"},
		"output-and-scripting": {"exit codes", "--yes", "timeout"},
		"run-jobs":             {"ingestion", "job states"},
	} {
		top, ok := lookup(name)
		if !ok {
			t.Fatalf("%s is not a topic", name)
		}
		for _, w := range want {
			if !strings.Contains(strings.ToLower(top.Summary), w) {
				t.Errorf("%s summary %q does not say %q", name, top.Summary, w)
			}
		}
	}
}
