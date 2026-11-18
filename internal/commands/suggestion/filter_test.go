package suggestion

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const listURL = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions"

// bulkBody is the wire shape of BulkSuggestionRequest, decoded rather than
// asserted on as a string so a renamed JSON key fails loudly. Pointers
// throughout: "absent" is a distinct claim from "empty", and suggestion_ids
// carries no omitempty, so a nil arrives as an explicit null.
type bulkBody struct {
	Source        *string   `json:"source"`
	Action        *string   `json:"action"`
	SuggestionIDs *[]string `json:"suggestion_ids"`
}

func decodeBulkBody(t *testing.T, raw []byte) bulkBody {
	t.Helper()
	var body bulkBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("bulk body not JSON: %v (%s)", err, raw)
	}
	return body
}

// escSeq is assembled rather than written as a literal: a spelled-out
// escape in a source file risks being saved as a raw control byte.
var escSeq = "\x1b" + "[31m"

func tinyRow(id string, action api.SuggestionAction, source api.SuggestionSource) api.DatasetRuleSuggestionTinySchema {
	return api.DatasetRuleSuggestionTinySchema{
		ID: uuid.MustParse(id), Action: action, Source: source,
		State: api.SuggestionStatePending, InstanceName: "dedupe",
	}
}

func TestFilterPathDismissesMatches(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("source"); got != "profiling" {
			t.Errorf("source not sent to the list route, query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling),
			},
		})
	})
	var raw []byte
	mock.RegisterHandler("POST", bulkDismissURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "dismiss",
		"--source", "profiling", "--table-id", "8", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("bulk-dismiss was not called")
	}
	// The selector must reach the WRITE, not just the preview. A
	// BulkSelector{} here would send neither source nor ids, and atlas's
	// _resolve_pending_suggestions would then resolve every pending suggestion
	// in the table — a silent "dismiss everything" that every other assertion
	// in this file would sit through.
	body := decodeBulkBody(t, raw)
	if body.Source == nil || *body.Source != "profiling" {
		t.Errorf("bulk-dismiss must carry source=profiling, body: %s", raw)
	}
	// The preview must name what is about to be written.
	if !strings.Contains(out, sugID2) {
		t.Errorf("preview must list the matched ids, got %q", out)
	}
	// Ruling B: renderBulkResult pluralizes, so one row reads "1 suggestion".
	// The trailing newline is what makes this assertion able to fail — without
	// it the singular is a prefix of the plural and both would pass.
	if !strings.Contains(out, "dismissed 1 suggestion\n") {
		t.Errorf("output = %q", out)
	}
}

func TestFilterPathFiltersActionClientSide(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		// The list route has no `action` parameter, so qctl must not invent one.
		if r.URL.Query().Has("action") {
			t.Errorf("action must not be sent to the list route, query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionCreate, api.User),
				tinyRow(testSuggestionUUID.String(), api.SuggestionActionUpdate, api.User),
			},
		})
	})
	var raw []byte
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		"--action", "create", "--table-id", "8", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("bulk-accept was not called")
	}
	// action IS a bulk-route parameter even though the list route has none, so
	// the write must carry it: filtering only the preview would accept rows the
	// user was never shown.
	body := decodeBulkBody(t, raw)
	if body.Action == nil || *body.Action != "create" {
		t.Errorf("bulk-accept must carry action=create, body: %s", raw)
	}
	if body.Source != nil {
		t.Errorf("--source was not given; it must not appear on the wire, body: %s", raw)
	}
	// The server chose this set. Naming ids too would AND the two filters and
	// silently narrow the write to whatever the ids happened to be.
	if body.SuggestionIDs != nil {
		t.Errorf("the filter route must not name ids, body: %s", raw)
	}
	if !strings.Contains(out, sugID2) {
		t.Errorf("the create row must be previewed, got %q", out)
	}
	if strings.Contains(out, testSuggestionUUID.String()) {
		t.Errorf("the update row must be filtered out of the preview, got %q", out)
	}
}

func TestFilterPathZeroMatchesExitsZero(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		"--source", "periodic", "--table-id", "8", "--yes")
	if err != nil {
		t.Fatalf("a filter matching nothing is not an error: %v", err)
	}
	if bulkCalled {
		t.Error("no write may happen when nothing matched")
	}
	// Deliberately NOT result.go's "no pending suggestions matched" line: that
	// one is only reachable after a write, and the two states must stay
	// distinguishable. See the routeBulkFilter arm in accept.go.
	if !strings.Contains(out, "nothing to accept") {
		t.Errorf("output = %q", out)
	}
	if strings.Contains(out, "no pending suggestions matched") {
		t.Errorf("that is result.go's post-write line; the pre-write case must read differently\n%s", out)
	}
}

func TestFilterPathRequiresConfirmation(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling),
			},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	// Answer "n" at the prompt.
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)

	_, err := runSuggestionCommand(t, "suggestion", "accept", "--source", "profiling", "--table-id", "8")
	if err == nil {
		t.Fatal("declining the prompt must not exit 0")
	}
	if bulkCalled {
		t.Error("nothing may be written when the prompt is declined")
	}
	// Ruling C: cmdutil.Confirm's contract — a non-interactive stdin is bad
	// usage (2) because only the command line can fix it, while declining at
	// the prompt is a choice, not a usage error (1).
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (declining is a choice, not bad usage)", got, qerrors.ExitGenericError)
	}
}

// TestFilterPathNonInteractiveNamesYes is the other half of cmdutil.Confirm's
// contract: with no reader and no --yes there is no answer to be had, and the
// only fix is on the command line.
func TestFilterPathNonInteractiveNamesYes(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling),
			},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	_, err := runSuggestionCommand(t, "suggestion", "accept", "--source", "profiling", "--table-id", "8")
	if err == nil {
		t.Fatal("an unanswerable confirmation must not exit 0")
	}
	if bulkCalled {
		t.Error("nothing may be written when the confirmation cannot be answered")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal must name the fix, got %q", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// TestFilterPathRefusesAPartialPreview guards maxPreviewPages. A server that
// keeps advertising a next page must not be walked forever, and the preview is
// the only thing the user gets to approve, so a truncated one is refused rather
// than shown.
func TestFilterPathRefusesAPartialPreview(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	pages := 0
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		pages++
		// Only Next's nil-ness is read — previewFilterMatches never inspects
		// the cursor inside it — so these values are filler, not a page
		// pointer. previewFilterMatches asks for the next page by number.
		start, end := 1, 2
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling),
			},
			Next: &api.PaginationSchema{Start: &start, End: &end},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	_, err := runSuggestionCommand(t, "suggestion", "accept",
		"--source", "profiling", "--table-id", "8", "--yes")
	if err == nil {
		t.Fatal("an unbounded listing must be refused, not acted on")
	}
	if bulkCalled {
		t.Error("nothing may be written from a partial preview")
	}
	if pages != maxPreviewPages {
		t.Errorf("walked %d pages, want %d", pages, maxPreviewPages)
	}
	// Bad usage, not a generic error: the only fix is a different command line
	// (name the ids, or narrow by --source), so a script must not read this as
	// a transient server failure and retry it.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "partial preview") {
		t.Errorf("refusal must say why, got %q", err.Error())
	}
}

// TestFilterPathPreviewsEveryPage is the assertion that the walk is a WALK.
// TestFilterPathRefusesAPartialPreview proves the loop iterates, but not that
// `page` advances or that a later page's rows survive into the preview —
// dropping `opts.Page = page` leaves that test green while the preview silently
// shows page 1 forever, which is the exact under-reporting filter.go's doc
// comment says the exhaustive walk exists to prevent.
func TestFilterPathPreviewsEveryPage(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	var pagesAsked []string
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pagesAsked = append(pagesAsked, page)
		if page == "2" {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{
					tinyRow(testSuggestionUUID.String(), api.SuggestionActionUpdate, api.Profiling),
				},
			})
			return
		}
		start := 2
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling),
			},
			Next: &api.PaginationSchema{Start: &start},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 2, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		"--source", "profiling", "--table-id", "8", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bulkCalled {
		t.Fatal("bulk-accept was not called")
	}
	if len(pagesAsked) != 2 {
		t.Fatalf("the walk must stop when next is null, asked pages %v", pagesAsked)
	}
	if pagesAsked[0] != "1" || pagesAsked[1] != "2" {
		t.Errorf("page must advance on the wire, asked pages %v", pagesAsked)
	}
	for _, want := range []string{sugID2, testSuggestionUUID.String()} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview must list %s — every page's rows reach it\n%s", want, out)
		}
	}
	if !strings.Contains(out, "2 pending suggestions will be accepted") {
		t.Errorf("the preview must count both pages, got %q", out)
	}
}

// candidateRow is a pending revalidation candidate as the LIST route returns
// it. The compact schema carries is_revalidation_candidate, which is the whole
// reason the filter route can predict accept's refusal without a pre-flight.
func candidateRow(id string, source api.SuggestionSource) api.DatasetRuleSuggestionTinySchema {
	row := tinyRow(id, api.SuggestionActionUpdate, source)
	yes := true
	row.IsRevalidationCandidate = &yes
	return row
}

// TestFilterPathPreviewNamesWhatAcceptWillRefuse: the --source/--action route
// runs no pre-flight, so before this the prompt said "2 pending suggestions
// will be accepted" about a set the server would only half accept — a promise
// the command could not keep, discovered by the user as an exit 1 after they
// had already approved it.
func TestFilterPathPreviewNamesWhatAcceptWillRefuse(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{
				tinyRow(sugID2, api.SuggestionActionUpdate, api.User),
				candidateRow(sugCandidateID, api.User),
			},
		})
	})
	var raw []byte
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 1, Skipped: 1,
				Errors: []string{sugCandidateID + ": is an in-place revalidation candidate"}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		"--source", "user", "--table-id", "8", "--yes")
	// One landed and one was refused, so this is the partial-success exit.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want %d; err = %v", got, qerrors.ExitGenericError, err)
	}
	if !strings.Contains(out, "2 pending suggestions matched; 1 is a revalidation candidate the server will refuse, so 1 will be accepted:") {
		t.Errorf("the prompt must not promise a write the server refuses, got %q", out)
	}
	// The row itself is marked, not merely counted: a user scanning the list
	// has to be able to tell WHICH one. Scoped to the PREVIEW block — the
	// blank line ends it — because renderBulkResult later echoes the server's
	// own refusal line, which names the same id and would satisfy a whole-output
	// search no matter what the preview printed.
	_, after, ok := strings.Cut(out, "will be accepted:\n")
	if !ok {
		t.Fatalf("no preview block in %q", out)
	}
	preview, _, _ := strings.Cut(after, "\n\n")
	for _, line := range strings.Split(preview, "\n") {
		if strings.Contains(line, sugCandidateID) && !strings.Contains(line, "will be refused") {
			t.Errorf("the candidate row must carry its own mark, got %q", line)
		}
		if strings.Contains(line, sugID2) && strings.Contains(line, "will be refused") {
			t.Errorf("a non-candidate row must not be marked, got %q", line)
		}
	}
	if !strings.Contains(preview, sugCandidateID) || !strings.Contains(preview, sugID2) {
		t.Errorf("the preview must list both matched rows, got %q", preview)
	}
	// Still written, and still as a FILTER: qctl cannot subtract from a set the
	// server re-selects for itself without switching to an id list.
	if raw == nil {
		t.Fatal("bulk-accept was not called")
	}
	body := decodeBulkBody(t, raw)
	if body.Source == nil || *body.Source != "user" || body.SuggestionIDs != nil {
		t.Errorf("the write must stay the filter, body: %s", raw)
	}
}

// TestFilterPathRefusesWhenEveryMatchIsACandidate: with nothing left to write,
// prompting would ask the user to approve a write of zero. Exit 2 for
// filter.go's cap reason — only a different command line clears it.
func TestFilterPathRefusesWhenEveryMatchIsACandidate(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{candidateRow(sugCandidateID, api.User)},
		})
	})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		"--source", "user", "--table-id", "8", "--yes")
	if err == nil {
		t.Fatal("a selection with nothing acceptable in it must not exit 0")
	}
	// The listing prints BEFORE the refusal. Telling a user their command is
	// impossible without naming the rows it matched leaves them nothing to act
	// on — and unlike the zero-match case there is something to show.
	if !strings.Contains(out, sugCandidateID) {
		t.Errorf("the refusal must still name what matched, got %q", out)
	}
	if !strings.Contains(out, "will be refused") {
		t.Errorf("the matched row must carry its mark, got %q", out)
	}
	if bulkCalled {
		t.Error("nothing may be written when every match will be refused")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	// The message must name the verb that DOES apply, or the user has no move.
	if !strings.Contains(err.Error(), "qctl suggestion dismiss") {
		t.Errorf("the refusal must name the fix, got %q", err.Error())
	}
}

// TestFilterPathDismissNeverMarksCandidates is the negative half: dismiss is
// the verb that retires a candidate, so the split must be empty for it. Without
// this, keying the split on anything but verbAccept would go unnoticed.
func TestFilterPathDismissNeverMarksCandidates(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{candidateRow(sugCandidateID, api.User)},
		})
	})
	called := false
	mock.RegisterHandler("POST", bulkDismissURL, func(w http.ResponseWriter, r *http.Request) {
		called = true
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "dismiss",
		"--source", "user", "--table-id", "8", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("bulk-dismiss was not called")
	}
	if !strings.Contains(out, "1 pending suggestion will be dismissed:\n") {
		t.Errorf("dismiss must not mark or discount a candidate, got %q", out)
	}
	if strings.Contains(out, "will be refused") {
		t.Errorf("dismiss refuses nothing, got %q", out)
	}
}

// TestConfirmQuestionDropsAllWhenPartOfTheSetIsRefused pins the one place the
// word "all" would become a lie.
func TestConfirmQuestionDropsAllWhenPartOfTheSetIsRefused(t *testing.T) {
	if got := confirmQuestion("accept", 4, 4); got != "accept all 4?" {
		t.Errorf("whole set: %q", got)
	}
	if got := confirmQuestion("accept", 1, 4); got != "accept the other 1?" {
		t.Errorf("partial set: %q", got)
	}
}

// TestSplitRevalidationCandidatesTreatsAMissingFlagAsNotACandidate: the field
// is optional on the wire, and reading a nil as "candidate" would refuse writes
// the server would have taken.
func TestSplitRevalidationCandidatesTreatsAMissingFlagAsNotACandidate(t *testing.T) {
	row := tinyRow(sugID2, api.SuggestionActionUpdate, api.User)
	if row.IsRevalidationCandidate != nil {
		t.Fatal("tinyRow must leave the flag absent for this test to mean anything")
	}
	writable, refused := splitRevalidationCandidates(
		[]api.DatasetRuleSuggestionTinySchema{row}, verbAccept)
	if len(writable) != 1 || len(refused) != 0 {
		t.Errorf("writable = %d, refused = %d; a missing flag is not a candidate", len(writable), len(refused))
	}
}

// listMatches is the only place the user sees which rows a filtered bulk write
// will touch -- the set was chosen by the server, not named by the caller -- so
// a control character here could misrepresent what is being authorised.
func TestListMatchesSanitizesServerText(t *testing.T) {
	row := tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling)
	row.InstanceName = "dedupe" + escSeq

	var buf strings.Builder
	listMatches(&buf, []api.DatasetRuleSuggestionTinySchema{row}, nil)

	got := buf.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", got)
	}
	if !strings.Contains(got, `\x1b[31m`) {
		t.Errorf("the control must stay visible, got: %q", got)
	}
}
