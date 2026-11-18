package comments

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestUserDisplayFallbacks(t *testing.T) {
	fallback := uuid.MustParse("dddddddd-1111-2222-3333-444444444444")
	if got := UserDisplay(&api.UserInfoTinyDictSchema{FirstName: "Alice", LastName: "Chen", Email: "a@corp.com"}, fallback); got != "Alice Chen" {
		t.Errorf("UserDisplay = %q, want Alice Chen", got)
	}
	if got := UserDisplay(&api.UserInfoTinyDictSchema{Email: "a@corp.com"}, fallback); got != "a@corp.com" {
		t.Errorf("UserDisplay = %q, want a@corp.com", got)
	}
	if got := UserDisplay(&api.UserInfoTinyDictSchema{}, fallback); got != fallback.String() {
		t.Errorf("UserDisplay = %q, want the fallback UUID", got)
	}
	// A suppressed author object (tombstone) falls all the way back.
	if got := UserDisplay(nil, fallback); got != fallback.String() {
		t.Errorf("UserDisplay(nil) = %q, want the fallback UUID", got)
	}
}

func TestUserDisplayWithRole(t *testing.T) {
	fallback := uuid.MustParse("dddddddd-1111-2222-3333-444444444444")
	role := api.RoleEnum("external")
	for _, tc := range []struct {
		user *api.UserInfoTinyDictSchema
		want string
	}{
		{&api.UserInfoTinyDictSchema{FirstName: "Ada", Role: &role}, "Ada (external)"},
		{&api.UserInfoTinyDictSchema{Email: "a@corp.com", Role: &role}, "a@corp.com (external)"},
		{&api.UserInfoTinyDictSchema{Role: &role}, fallback.String() + " (external)"},
		{&api.UserInfoTinyDictSchema{FirstName: "Ada"}, "Ada"},
		{nil, fallback.String()},
	} {
		if got := UserDisplayWithRole(tc.user, fallback); got != tc.want {
			t.Errorf("UserDisplayWithRole(%+v) = %q, want %q", tc.user, got, tc.want)
		}
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

func TestCountCommentsCountsWholeTree(t *testing.T) {
	if got := CountComments(sampleCommentTree()); got != 4 {
		t.Errorf("CountComments = %d, want 4", got)
	}
	if got := CountComments(nil); got != 0 {
		t.Errorf("CountComments(nil) = %d, want 0", got)
	}
}

func TestRenderCommentNestsAndDeRendersMentions(t *testing.T) {
	tree := sampleCommentTree()
	var b strings.Builder
	for i := range tree {
		RenderComment(&b, &tree[i], 0)
	}
	out := b.String()
	for _, want := range []string{
		"[c: 0190bbbb-0000-0000-0000-000000000000] Bob Roe (1 day ago):",
		"  Null emails slip through — see the dry run.",
		"  (dry run: job 512)",
		"  [c: 0190cccc-0000-0000-0000-000000000000] ↳ Sep Dehpour (23 hours ago) (edited):",
		"    Fixed in the resubmission, PTAL @Bob Roe",
		"    [c: 0190dddd-0000-0000-0000-000000000000] ↳ Bob Roe (2 hours ago):",
		"      Confirmed, thanks.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "](mention:") {
		t.Errorf("mention link not de-rendered\n%s", out)
	}
}

func TestRenderCommentTombstoneHidesAuthor(t *testing.T) {
	tree := sampleCommentTree()
	var b strings.Builder
	RenderComment(&b, &tree[1], 0)
	out := b.String()
	if !strings.Contains(out, "[c: 0190eeee-0000-0000-0000-000000000000] (3 hours ago):") {
		t.Errorf("tombstone header wrong\n%s", out)
	}
	if !strings.Contains(out, "  [comment deleted]") {
		t.Errorf("tombstone body missing\n%s", out)
	}
	// The tombstone must not leak the author UUID as a display name.
	if strings.Contains(out, "dddddddd-1111-2222-3333-444444444444") {
		t.Errorf("tombstone leaked the author UUID\n%s", out)
	}
}

func TestRenderCommentIndentsByDepth(t *testing.T) {
	tree := sampleCommentTree()
	var b strings.Builder
	RenderComment(&b, &tree[0], 2)
	first := strings.SplitN(b.String(), "\n", 2)[0]
	if !strings.HasPrefix(first, "    [c: ") {
		t.Errorf("depth-2 comment must be indented four spaces, got %q", first)
	}
	if !strings.Contains(first, "↳ ") {
		t.Errorf("a non-zero-depth comment must carry the reply marker, got %q", first)
	}
}

func TestRenderCommentAppendsTheAuthorRole(t *testing.T) {
	tree := sampleCommentTree()
	role := api.RoleEnum("external")
	author := *tree[0].Author
	author.Role = &role
	tree[0].Author = &author
	tree[0].Replies = nil

	var b strings.Builder
	RenderComment(&b, &tree[0], 0)
	want := "[c: 0190bbbb-0000-0000-0000-000000000000] Bob Roe (external) (1 day ago):"
	if first := strings.SplitN(b.String(), "\n", 2)[0]; first != want {
		t.Errorf("header = %q, want %q", first, want)
	}

	tree[0].Author.Role = nil
	b.Reset()
	RenderComment(&b, &tree[0], 0)
	want = "[c: 0190bbbb-0000-0000-0000-000000000000] Bob Roe (1 day ago):"
	if first := strings.SplitN(b.String(), "\n", 2)[0]; first != want {
		t.Errorf("header without a role = %q, want %q", first, want)
	}
}
