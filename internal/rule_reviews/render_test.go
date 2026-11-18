package rule_reviews //nolint:revive // Match the established package under test.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func sampleDetail(status api.ReviewRequestStatus) *ReviewDetail {
	reviewID := uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	revisionID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	requestorID := uuid.MustParse("aaaaaaaa-1111-2222-3333-444444444444")
	datasetName := "orders"
	reviewers := []api.UserInfoTinyDictSchema{
		{ID: uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444"), Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
	}
	return &ReviewDetail{
		ID:                reviewID,
		Title:             "Review dedupe-emails@2.1.0",
		Status:            status,
		CurrentApprovals:  1,
		RequiredApprovals: 2,
		RequestorID:       requestorID,
		Requestor:         &api.UserInfoTinyDictSchema{ID: requestorID, Email: "sep@corp.com", FirstName: "Sep", LastName: "Dehpour"},
		AssignedReviewers: &reviewers,
		DatasetID:         testutil.IntPtr(42),
		DatasetName:       &datasetName,
		RuleRevisionID:    revisionID,
		RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
			ID: revisionID, Name: "dedupe-emails", Release: "2.1.0",
		},
		EffectivePolicy: &api.EffectivePolicyResponseSchema{
			RequiresReview: true, MinReviewers: 2, RequireDryRun: true,
			RequireDryRunSuccess: true, AutoEnableOnApproval: true, Source: "dataset",
		},
	}
}

func TestRenderDetailSections(t *testing.T) {
	out := RenderDetail(sampleDetail(api.ReviewRequestStatusChangesRequested))
	for _, want := range []string{
		"# RULE REVIEW 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182",
		"Title: Review dedupe-emails@2.1.0",
		"Rule: dedupe-emails @ 2.1.0 (revision 550e8400-e29b-41d4-a716-446655440000)",
		"Scope: table orders (42)",
		"Status: changes_requested | Approvals: 1/2 | Requestor: Sep Dehpour",
		"Reviewers: Alice Chen",
		"Dry run: (none linked)",
		"## POLICY",
		"requires_review: true | min_reviewers: 2 | require_dry_run: true",
		"require_dry_run_success: true | auto_enable_on_approval: true | source: dataset",
		"## NEXT ACTIONS",
		"qctl review close 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderDetailOrgWideAndNoReviewers(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	d.DatasetID = nil
	d.DatasetName = nil
	d.AssignedReviewers = nil
	out := RenderDetail(d)
	if !strings.Contains(out, "Scope: org-wide") {
		t.Errorf("missing org-wide scope\n%s", out)
	}
	if !strings.Contains(out, "Reviewers: (none assigned)") {
		t.Errorf("missing (none assigned)\n%s", out)
	}
}

func TestRenderDetailDryRunLine(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	d.DryRunJobID = testutil.IntPtr(512)
	state := api.IngestionJobStateFinished
	d.DryRunJobStatus = &state
	out := RenderDetail(d)
	if !strings.Contains(out, "Dry run: job 512 (finished)") {
		t.Errorf("missing dry-run line\n%s", out)
	}
}

func TestRenderDryRunGateVariants(t *testing.T) {
	// base() carries a policy with RequireDryRun + RequireDryRunSuccess true
	// and no linked job (from sampleDetail).
	base := func() *ReviewDetail { return sampleDetail(api.ReviewRequestStatusOpen) }

	t.Run("none linked but policy requires one", func(t *testing.T) {
		d := base()
		out := renderDryRunLine(d)
		for _, want := range []string{
			"Dry run: (none linked)",
			"gate UNMET: policy requires a linked dry run",
			"qctl review link-dry-run " + d.ID.String() + " <job-id>",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %q", want, out)
			}
		}
	})

	t.Run("none linked and policy does not require one", func(t *testing.T) {
		d := base()
		d.EffectivePolicy.RequireDryRun = false
		d.EffectivePolicy.RequireDryRunSuccess = false
		if out := renderDryRunLine(d); out != "Dry run: (none linked)" {
			t.Errorf("want plain none-linked, got %q", out)
		}
	})

	t.Run("linked finished with require_success", func(t *testing.T) {
		d := base()
		d.DryRunJobID = testutil.IntPtr(512)
		st := api.IngestionJobStateFinished
		d.DryRunJobStatus = &st
		out := renderDryRunLine(d)
		for _, want := range []string{
			"Dry run: job 512 (finished)",
			"gate: require_dry_run_success",
			"job state satisfies it",
			"success also needs no rule load errors, rule-bug or sandbox-failure findings",
			"qctl inspect dry-run-job 512",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %q", want, out)
			}
		}
	})

	t.Run("linked running with require_success", func(t *testing.T) {
		d := base()
		d.DryRunJobID = testutil.IntPtr(512)
		st := api.IngestionJobStateRunning
		d.DryRunJobStatus = &st
		out := renderDryRunLine(d)
		for _, want := range []string{
			"Dry run: job 512 (running)",
			"gate: require_dry_run_success",
			"does NOT yet satisfy it",
			"needs state=finished with no rule load errors, rule-bug or sandbox-failure findings",
			"qctl inspect dry-run-job 512",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %q", want, out)
			}
		}
	})

	t.Run("linked with require_dry_run only", func(t *testing.T) {
		d := base()
		d.EffectivePolicy.RequireDryRunSuccess = false // require_dry_run stays true
		d.DryRunJobID = testutil.IntPtr(512)
		st := api.IngestionJobStateRunning
		d.DryRunJobStatus = &st
		out := renderDryRunLine(d)
		for _, want := range []string{"gate: require_dry_run;", "a linked dry run satisfies it"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %q", want, out)
			}
		}
		if strings.Contains(out, "require_dry_run_success") {
			t.Errorf("must not mention the success gate: %q", out)
		}
	})

	t.Run("linked with no dry-run requirement", func(t *testing.T) {
		d := base()
		d.EffectivePolicy.RequireDryRun = false
		d.EffectivePolicy.RequireDryRunSuccess = false
		d.DryRunJobID = testutil.IntPtr(512)
		st := api.IngestionJobStateFinished
		d.DryRunJobStatus = &st
		if out := renderDryRunLine(d); !strings.Contains(out, "policy does not require a dry run") {
			t.Errorf("want no-requirement note, got %q", out)
		}
	})

	t.Run("policy absent falls back to plain form", func(t *testing.T) {
		d := base()
		d.EffectivePolicy = nil
		d.DryRunJobID = testutil.IntPtr(512)
		st := api.IngestionJobStateFinished
		d.DryRunJobStatus = &st
		if out := renderDryRunLine(d); out != "Dry run: job 512 (finished)" {
			t.Errorf("want plain form without policy, got %q", out)
		}
	})
}

func TestRenderDetailNextActionsByStatus(t *testing.T) {
	cases := []struct {
		status api.ReviewRequestStatus
		want   string
		absent string
	}{
		{api.ReviewRequestStatusOpen, "qctl review assign", "qctl review reopen"},
		{api.ReviewRequestStatusRejected, "qctl review reopen 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182", "qctl review close"},
		{api.ReviewRequestStatusApproved, "qctl enable rule dedupe-emails --release 2.1.0", "qctl review close"},
		{api.ReviewRequestStatusClosed, "review is closed", "qctl review reopen"},
	}
	for _, tc := range cases {
		out := RenderDetail(sampleDetail(tc.status))
		if !strings.Contains(out, tc.want) {
			t.Errorf("[%s] missing %q\n%s", tc.status, tc.want, out)
		}
		if strings.Contains(out, tc.absent) {
			t.Errorf("[%s] must not contain %q\n%s", tc.status, tc.absent, out)
		}
	}
}

func TestRenderDetailPolicyMissing(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	d.EffectivePolicy = nil
	out := RenderDetail(d)
	if !strings.Contains(out, "## POLICY") {
		t.Errorf("POLICY section must render even without data\n%s", out)
	}
	if !strings.Contains(out, "(not returned by the backend)") {
		t.Errorf("missing policy placeholder\n%s", out)
	}
}

func sampleDecisions() []api.ReviewDecisionResponseSchema {
	alice := api.UserInfoTinyDictSchema{
		ID:    uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444"),
		Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen",
	}
	bob := api.UserInfoTinyDictSchema{
		ID:    uuid.MustParse("cccccccc-1111-2222-3333-444444444444"),
		Email: "bob@corp.com", FirstName: "Bob", LastName: "Roe",
	}
	return []api.ReviewDecisionResponseSchema{
		{
			ID:       uuid.MustParse("0190aaaa-0000-0000-0000-000000000001"),
			Decision: api.ReviewDecisionEnumApprove, Reviewer: &alice, ReviewerID: alice.ID,
			CreatedAt: time.Now().Add(-2 * time.Hour),
			Comment:   testutil.StringPtr("LGTM after the null fix"),
		},
		{
			ID:       uuid.MustParse("0190aaaa-0000-0000-0000-000000000002"),
			Decision: api.ReviewDecisionEnumRequestChanges, Reviewer: &bob, ReviewerID: bob.ID,
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
	}
}

func TestMentionLink(t *testing.T) {
	id := uuid.MustParse("018e0000-0000-0000-0000-000000000001")
	got := MentionLink("Alice Chen", id)
	want := "[@Alice Chen](mention:018e0000-0000-0000-0000-000000000001)"
	if got != want {
		t.Errorf("MentionLink = %q, want %q", got, want)
	}
}

func TestDeRenderMentions(t *testing.T) {
	in := "ping [@Alice Chen](mention:018e0000-0000-0000-0000-000000000001) and [@Bob](mention:018e0000-0000-0000-0000-000000000002)"
	want := "ping @Alice Chen and @Bob"
	if got := DeRenderMentions(in); got != want {
		t.Errorf("DeRenderMentions = %q, want %q", got, want)
	}
	// Non-mention markdown links pass through untouched.
	plain := "see [the docs](https://example.com)"
	if got := DeRenderMentions(plain); got != plain {
		t.Errorf("mangled non-mention link: %q", got)
	}
}

func TestRenderDetailDecisionsSection(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	decisions := sampleDecisions()
	d.Decisions = &decisions
	out := RenderDetail(d)
	for _, want := range []string{
		"## DECISIONS (2)",
		`- APPROVE by Alice Chen (2 hours ago): "LGTM after the null fix"`,
		"- REQUEST_CHANGES by Bob Roe (1 day ago)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	// No-comment decision must not render a dangling quote.
	if strings.Contains(out, `Bob Roe (1 day ago):`) {
		t.Errorf("comment-less decision must not have a trailing colon\n%s", out)
	}
}

func TestRenderDetailDecisionsEmpty(t *testing.T) {
	out := RenderDetail(sampleDetail(api.ReviewRequestStatusOpen))
	if !strings.Contains(out, "## DECISIONS (0)") {
		t.Errorf("DECISIONS section must render with a zero count\n%s", out)
	}
	// Frozen order: POLICY < DECISIONS < NEXT ACTIONS.
	if strings.Index(out, "## POLICY") >= strings.Index(out, "## DECISIONS") ||
		strings.Index(out, "## DECISIONS") >= strings.Index(out, "## NEXT ACTIONS") {
		t.Errorf("section order broken\n%s", out)
	}
}

func sampleCommentTree() []api.CommentResponseSchema {
	bob := api.UserInfoTinyDictSchema{
		ID:    uuid.MustParse("cccccccc-1111-2222-3333-444444444444"),
		Email: "bob@corp.com", FirstName: "Bob", LastName: "Roe",
	}
	sep := api.UserInfoTinyDictSchema{
		ID:    uuid.MustParse("aaaaaaaa-1111-2222-3333-444444444444"),
		Email: "sep@corp.com", FirstName: "Sep", LastName: "Dehpour",
	}
	job := 512
	deleted := true
	reply2 := api.CommentResponseSchema{
		ID:     uuid.MustParse("0190dddd-0000-0000-0000-000000000000"),
		Author: &bob, AuthorID: bob.ID,
		Body:      "Confirmed, thanks.",
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	reply1 := api.CommentResponseSchema{
		ID:     uuid.MustParse("0190cccc-0000-0000-0000-000000000000"),
		Author: &sep, AuthorID: sep.ID,
		Body:      "Fixed in the resubmission, PTAL [@Bob Roe](mention:cccccccc-1111-2222-3333-444444444444)",
		IsEdited:  true,
		CreatedAt: time.Now().Add(-23 * time.Hour),
		Replies:   &[]api.CommentResponseSchema{reply2},
	}
	top1 := api.CommentResponseSchema{
		ID:     uuid.MustParse("0190bbbb-0000-0000-0000-000000000000"),
		Author: &bob, AuthorID: bob.ID,
		Body:              "Null emails slip through — see the dry run.",
		LinkedDryRunJobID: &job,
		CreatedAt:         time.Now().Add(-24 * time.Hour),
		Replies:           &[]api.CommentResponseSchema{reply1},
	}
	top2 := api.CommentResponseSchema{
		ID:        uuid.MustParse("0190eeee-0000-0000-0000-000000000000"),
		AuthorID:  uuid.MustParse("dddddddd-1111-2222-3333-444444444444"), // author object suppressed
		Body:      "[comment deleted]",
		IsDeleted: &deleted,
		CreatedAt: time.Now().Add(-3 * time.Hour),
	}
	return []api.CommentResponseSchema{top1, top2}
}

func TestRenderDetailCommentsSection(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	comments := sampleCommentTree()
	d.Comments = &comments
	out := RenderDetail(d)
	for _, want := range []string{
		"## COMMENTS (2 top-level, 4 total)",
		"[c: 0190bbbb-0000-0000-0000-000000000000] Bob Roe (1 day ago):",
		"  Null emails slip through — see the dry run.",
		"  (dry run: job 512)",
		"  [c: 0190cccc-0000-0000-0000-000000000000] ↳ Sep Dehpour (23 hours ago) (edited):",
		"    Fixed in the resubmission, PTAL @Bob Roe",
		"    [c: 0190dddd-0000-0000-0000-000000000000] ↳ Bob Roe (2 hours ago):",
		"      Confirmed, thanks.",
		"[c: 0190eeee-0000-0000-0000-000000000000] (3 hours ago):",
		"  [comment deleted]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	// The tombstone must not leak the author UUID as a display name.
	if strings.Contains(out, "dddddddd-1111-2222-3333-444444444444") {
		t.Errorf("tombstone leaked the author UUID\n%s", out)
	}
	// Raw mention links must be de-rendered.
	if strings.Contains(out, "](mention:") {
		t.Errorf("mention link not de-rendered\n%s", out)
	}
}

func TestRenderDetailCommentsAppendTheAuthorRole(t *testing.T) {
	d := sampleDetail(api.ReviewRequestStatusOpen)
	thread := sampleCommentTree()
	role := api.RoleEnum("external")
	author := *thread[0].Author
	author.Role = &role
	thread[0].Author = &author
	d.Comments = &thread
	out := RenderDetail(d)
	if want := "[c: 0190bbbb-0000-0000-0000-000000000000] Bob Roe (external) (1 day ago):"; !strings.Contains(out, want) {
		t.Errorf("output missing %q\n---\n%s", want, out)
	}
	// The reply's author carries no role and renders as before.
	if want := "[c: 0190dddd-0000-0000-0000-000000000000] ↳ Bob Roe (2 hours ago):"; !strings.Contains(out, want) {
		t.Errorf("output missing %q\n---\n%s", want, out)
	}
}

func TestRenderDetailCommentsEmptyAndOrder(t *testing.T) {
	out := RenderDetail(sampleDetail(api.ReviewRequestStatusOpen))
	if !strings.Contains(out, "## COMMENTS (0 top-level, 0 total)") {
		t.Errorf("COMMENTS section must render with zero counts\n%s", out)
	}
	if strings.Index(out, "## DECISIONS") >= strings.Index(out, "## COMMENTS") ||
		strings.Index(out, "## COMMENTS") >= strings.Index(out, "## NEXT ACTIONS") {
		t.Errorf("section order broken\n%s", out)
	}
}

func TestRenderDetailNextActionsPhase2(t *testing.T) {
	id := "018f3c2e-4d5a-7b89-a1c2-3d4e5f607182"

	out := RenderDetail(sampleDetail(api.ReviewRequestStatusOpen))
	for _, want := range []string{
		"qctl review comment " + id + " --body",
		"qctl review approve " + id,
		"qctl review request-changes " + id,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("[open] missing %q\n%s", want, out)
		}
	}

	out = RenderDetail(sampleDetail(api.ReviewRequestStatusRejected))
	if !strings.Contains(out, "qctl review withdraw "+id) {
		t.Errorf("[rejected] missing withdraw hint\n%s", out)
	}
}
