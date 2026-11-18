package comments

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	aliceID = uuid.MustParse("aaaaaaaa-1111-2222-3333-444444444444")
	bobID   = uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444")
)

func testDirectory() *Directory {
	return &Directory{users: []api.UserListItemSchema{
		{ID: aliceID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
		{ID: bobID, Email: "bob@corp.com", FirstName: "Bob", LastName: "Roe"},
	}}
}

func TestResolveByUUIDPassthrough(t *testing.T) {
	// UUID passthrough works even when the user is not in the directory.
	d := &Directory{}
	stranger := "cccccccc-1111-2222-3333-444444444444"
	got, err := d.Resolve(stranger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != stranger {
		t.Errorf("Resolve = %s, want %s", got, stranger)
	}
}

// An email or a name is trimmed before it is matched, so a UUID must be too:
// otherwise " <uuid>" falls through to the name match and exits 2. The
// paddings avoid a 38-byte total, which uuid.Parse reads as a braced UUID
// without checking the braces.
func TestResolveByPaddedUUIDPassthrough(t *testing.T) {
	d := &Directory{}
	stranger := "cccccccc-1111-2222-3333-444444444444"
	for _, input := range []string{" " + stranger, stranger + "\n", "  " + stranger + "\t"} {
		got, err := d.Resolve(input)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", input, err)
			continue
		}
		if got.String() != stranger {
			t.Errorf("Resolve(%q) = %s, want %s", input, got, stranger)
		}
	}
}

func TestResolveByEmailCaseInsensitive(t *testing.T) {
	d := testDirectory()
	got, err := d.Resolve("ALICE@corp.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != aliceID {
		t.Errorf("Resolve = %s, want %s", got, aliceID)
	}
}

func TestResolveByFullName(t *testing.T) {
	d := testDirectory()
	got, err := d.Resolve("bob roe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != bobID {
		t.Errorf("Resolve = %s, want %s", got, bobID)
	}
}

func TestResolveNoMatchNamesInput(t *testing.T) {
	d := testDirectory()
	_, err := d.Resolve("carol@corp.com")
	if err == nil || !strings.Contains(err.Error(), "carol@corp.com") {
		t.Fatalf("error must name the input, got: %v", err)
	}
}

func TestResolveAmbiguousListsCandidates(t *testing.T) {
	d := &Directory{users: []api.UserListItemSchema{
		{ID: aliceID, Email: "alice@corp.com", FirstName: "Alex", LastName: "Smith"},
		{ID: bobID, Email: "asmith@corp.com", FirstName: "Alex", LastName: "Smith"},
	}}
	_, err := d.Resolve("Alex Smith")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "alice@corp.com") || !strings.Contains(err.Error(), "asmith@corp.com") {
		t.Errorf("error must list candidate emails, got: %v", err)
	}
}

// A --mention value naming nobody, or naming two people, is a flag validation
// failure: exit 2, like every other thing the user typed wrong. Left at the
// generic 1 a script cannot tell it from "the API fell over" and retries.
func TestResolveFailuresAreBadUsage(t *testing.T) {
	ambiguous := &Directory{users: []api.UserListItemSchema{
		{ID: aliceID, Email: "alice@corp.com", FirstName: "Alex", LastName: "Smith"},
		{ID: bobID, Email: "asmith@corp.com", FirstName: "Alex", LastName: "Smith"},
	}}
	cases := map[string]struct {
		dir   *Directory
		input string
	}{
		"no match":  {testDirectory(), "carol@corp.com"},
		"ambiguous": {ambiguous, "Alex Smith"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tc.dir.Resolve(tc.input)
			if err == nil {
				t.Fatalf("expected an error for %q", tc.input)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
		})
	}
}

func TestResolveAll(t *testing.T) {
	d := testDirectory()
	ids, err := d.ResolveAll([]string{"alice@corp.com", "Bob Roe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 || ids[0] != aliceID || ids[1] != bobID {
		t.Errorf("ResolveAll = %v", ids)
	}
}

func TestDisplayNameFallbacks(t *testing.T) {
	d := &Directory{users: []api.UserListItemSchema{
		{ID: aliceID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
		{ID: bobID, Email: "bob@corp.com"}, // no name → email
	}}
	if got := d.DisplayName(aliceID); got != "Alice Chen" {
		t.Errorf("DisplayName = %q, want Alice Chen", got)
	}
	if got := d.DisplayName(bobID); got != "bob@corp.com" {
		t.Errorf("DisplayName = %q, want bob@corp.com", got)
	}
	stranger := uuid.MustParse("cccccccc-1111-2222-3333-444444444444")
	if got := d.DisplayName(stranger); got != stranger.String() {
		t.Errorf("DisplayName = %q, want the UUID string", got)
	}
}

// CCLine is the one spelling of the --mention cc line across every comment
// verb: a blank line, "cc", then one structured link per id in the order given.
func TestCCLine(t *testing.T) {
	d := testDirectory()
	stranger := uuid.MustParse("cccccccc-1111-2222-3333-444444444444")
	got := d.CCLine([]uuid.UUID{bobID, aliceID, stranger})
	want := "\n\ncc [@Bob Roe](mention:" + bobID.String() + ") [@Alice Chen](mention:" + aliceID.String() +
		") [@" + stranger.String() + "](mention:" + stranger.String() + ")"
	if got != want {
		t.Errorf("CCLine = %q, want %q", got, want)
	}
	if got := d.CCLine(nil); got != "" {
		t.Errorf("CCLine(nil) = %q, want empty", got)
	}
}

func TestFetchUsersPaginates(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if r.URL.Query().Get("order_by") == "" {
				t.Error("page without order_by is ignored by the backend — order_by must be sent")
			}
			switch page {
			case 1:
				start := 2
				testutil.RespondJSON(w, http.StatusOK, api.UsersListSchema{
					Results: []api.UserListItemSchema{{ID: aliceID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"}},
					Next:    &api.PaginationSchema{Start: &start},
				})
			default:
				testutil.RespondJSON(w, http.StatusOK, api.UsersListSchema{
					Results: []api.UserListItemSchema{{ID: bobID, Email: "bob@corp.com", FirstName: "Bob", LastName: "Roe"}},
					Next:    nil,
				})
			}
		})

	apiClient, err := client.New(client.Config{BaseURL: mock.Server.URL, AccessToken: "test-token"})
	if err != nil {
		t.Fatalf("failed to build API client: %v", err)
	}
	dir, err := FetchUsers(apiClient, uuid.MustParse(testOrgID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := dir.Resolve("alice@corp.com"); err != nil {
		t.Errorf("page-1 user missing: %v", err)
	}
	if _, err := dir.Resolve("bob@corp.com"); err != nil {
		t.Errorf("page-2 user missing: %v", err)
	}
}

func TestFetchUsersOffsetPages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		empty     bool
		status    int
		wantUsers int
		wantPages int
		wantExit  int
	}{
		{name: "all users without next", wantUsers: 450, wantPages: 3},
		{name: "empty page stops the walk", empty: true, wantUsers: 200, wantPages: 2},
		{name: "later page forbidden", status: http.StatusForbidden, wantPages: 2, wantExit: qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			pages := 0
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
				pages++
				query := r.URL.Query()
				page, _ := strconv.Atoi(query.Get("page"))
				if page != pages || query.Get("limit") != "200" || query.Get("order_by") != "created_at" {
					t.Errorf("query = %v, request %d", query, pages)
				}
				if page == 2 && tc.status != 0 {
					testutil.RespondError(w, tc.status, "denied")
					return
				}
				resp := api.UsersListSchema{Results: []api.UserListItemSchema{}}
				if page == 1 {
					total := 450
					resp.TotalRows = &total
				}
				if !tc.empty || page == 1 {
					for i := (page-1)*200 + 1; i <= min(page*200, 450); i++ {
						user := api.UserListItemSchema{ID: uuid.New(), Email: "user" + strconv.Itoa(i) + "@corp.com"}
						if i == 450 {
							user = api.UserListItemSchema{ID: bobID, Email: "bob@corp.com", FirstName: "Bob", LastName: "Roe"}
						}
						resp.Results = append(resp.Results, user)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, resp)
			})
			apiClient, err := client.New(client.Config{BaseURL: mock.URL(), AccessToken: "test-token"})
			if err != nil {
				t.Fatal(err)
			}
			dir, err := FetchUsers(apiClient, uuid.MustParse(testOrgID))
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d: %v", got, tc.wantExit, err)
			}
			if pages != tc.wantPages {
				t.Errorf("pages = %d, want %d", pages, tc.wantPages)
			}
			if err != nil {
				return
			}
			if len(dir.users) != tc.wantUsers {
				t.Errorf("users = %d, want %d", len(dir.users), tc.wantUsers)
			}
			if tc.wantUsers == 450 {
				if id, err := dir.Resolve("bob@corp.com"); err != nil || id != bobID {
					t.Errorf("late-page resolution = %s, %v", id, err)
				}
				if got := dir.DisplayName(bobID); got != "Bob Roe" {
					t.Errorf("late-page display name = %q", got)
				}
			}
		})
	}
}

// A mentionable-users directory holds only some of the org, so its miss says
// how to name anyone else.
func TestMentionableDirectoryMissNamesTheWayPast(t *testing.T) {
	d := &Directory{missNote: mentionableMissNote}
	_, err := d.Resolve("bob@corp.com")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{`"bob@corp.com"`, "flagged rows of this table", "full UUID"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}
