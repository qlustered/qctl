package suggestion

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
)

// requireBulkExitOne asserts the exit code AND that a *qerrors.Error is really
// in the chain.
//
// GetExitCode alone cannot fail that way: 1 is its FALLBACK for any error it
// cannot classify, so a broken Unwrap chain — errors.New, or a fmt.Errorf("%v")
// wrap — scores 1 too. The errors.As check is what makes the assertion able to
// fail, per CLAUDE.md's error contract.
func requireBulkExitOne(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want a non-nil error, got nil")
	}
	var qe *qerrors.Error
	if !errors.As(err, &qe) {
		t.Fatalf("no *qerrors.Error in the chain (exit code would silently degrade to 1): %T %v", err, err)
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestRenderBulkResultCleanRun(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{Processed: 3, Skipped: 0, Errors: []string{}}
	err := renderBulkResult(&buf, res, []string{"a", "b", "c"}, "accept")
	if err != nil {
		t.Fatalf("a clean run must exit 0, got %v", err)
	}
	if !strings.Contains(buf.String(), "accepted 3 suggestions") {
		t.Errorf("output = %q", buf.String())
	}
	// R1: the denominator is noise when nothing was refused or went missing.
	if strings.Contains(buf.String(), "of 3") {
		t.Errorf("a clean run must not print the of-M form, got %q", buf.String())
	}
}

func TestRenderBulkResultPartialExitsOne(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{
		Processed: 2, Skipped: 1,
		Errors: []string{"8f3a: is an in-place revalidation candidate"},
	}
	err := renderBulkResult(&buf, res, []string{"a", "b", "8f3a"}, "accept")
	if err == nil {
		t.Fatal("a partial must not exit 0: the route answers 200 either way")
	}
	requireBulkExitOne(t, err)
	// A nonzero exit reads as "nothing happened" unless the message says
	// otherwise, so the count that DID land has to be in the error itself.
	if !strings.Contains(err.Error(), "the other 2 were committed") {
		t.Errorf("error must name what landed, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "1 of 3 suggestions was not accepted") {
		t.Errorf("summary arithmetic wrong, got %q", err.Error())
	}
	// R1's other half: here the denominator MUST appear.
	if !strings.Contains(buf.String(), "accepted 2 of 3 suggestions") {
		t.Errorf("count line must carry the denominator, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "revalidation candidate") {
		t.Errorf("the per-item reason must be rendered, got %q", buf.String())
	}
}

func TestRenderBulkResultReconcilesShortfall(t *testing.T) {
	var buf bytes.Buffer
	// Three ids sent, two accounted for: the third changed state between the
	// pre-flight and the write. The route reports nothing about it.
	res := &rule_suggestions.BulkResult{Processed: 2, Skipped: 0, Errors: []string{}}
	err := renderBulkResult(&buf, res, []string{"a", "b", "c"}, "accept")
	if err == nil {
		t.Fatal("an unaccounted-for id must not exit 0")
	}
	requireBulkExitOne(t, err)
	// The count line must agree with the shortfall line directly below it:
	// "accepted 2 suggestions" over "1 id was no longer pending" is
	// self-contradicting output.
	if !strings.Contains(buf.String(), "accepted 2 of 3 suggestions") {
		t.Errorf("count line must include the missing id in its total, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "1 id was no longer pending") {
		t.Errorf("the shortfall must be explained, got %q", buf.String())
	}
	if !strings.Contains(err.Error(), "1 of 3 suggestions was not accepted; the other 2 were committed") {
		t.Errorf("summary must count the missing id as a failure, got %q", err.Error())
	}
}

func TestRenderBulkResultAllIDsRacedAway(t *testing.T) {
	var buf bytes.Buffer
	// Both named ids changed state between the pre-flight and the write, so the
	// server's query returned nothing. On the id route that is a write that
	// touched nothing, not a clean no-op: it must not be reported as success.
	res := &rule_suggestions.BulkResult{Processed: 0, Skipped: 0, Errors: []string{}}
	err := renderBulkResult(&buf, res, []string{"a", "b"}, "accept")
	if err == nil {
		t.Fatal("an id route where every id raced away must not exit 0")
	}
	requireBulkExitOne(t, err)
	if !strings.Contains(buf.String(), "accepted 0 of 2 suggestions") {
		t.Errorf("count line = %q", buf.String())
	}
	if !strings.Contains(buf.String(), "2 ids were no longer pending") {
		t.Errorf("the shortfall must be explained, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "no pending suggestions matched") {
		t.Errorf("the zero-match message is the filter route's only, got %q", buf.String())
	}
}

// I2: refusals AND a shortfall together — the one state where the summary
// arithmetic can disagree with the count line. Neither term can be dropped
// without failing here.
func TestRenderBulkResultRefusalsAndShortfallTogether(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{
		Processed: 1, Skipped: 1,
		Errors: []string{"c9de: is an in-place revalidation candidate"},
	}
	err := renderBulkResult(&buf, res, []string{"a", "b", "c", "c9de"}, "accept")
	requireBulkExitOne(t, err)

	// total = 1 processed + 1 skipped + 2 missing.
	if !strings.Contains(buf.String(), "accepted 1 of 4 suggestions") {
		t.Errorf("count line must carry processed+skipped+missing, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "  refused  c9de: is an in-place revalidation candidate") {
		t.Errorf("the refusal reason must be rendered, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "2 ids were no longer pending") {
		t.Errorf("the shortfall must be rendered alongside the refusal, got %q", buf.String())
	}
	// 1 refused + 2 missing = 3 failed, and exactly 1 landed.
	if !strings.Contains(err.Error(), "3 of 4 suggestions were not accepted; the other 1 was committed") {
		t.Errorf("summary must count refusals AND the shortfall, got %q", err.Error())
	}
}

// I3: the filter route with refusals — the most likely real-world bulk
// failure, and the one a "the filter route never fails" shortcut would hide.
func TestRenderBulkResultFilterRouteRefusalsExitOne(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{
		Processed: 5, Skipped: 3,
		Errors: []string{"a1: refused", "b2: refused", "c3: refused"},
	}
	err := renderBulkResult(&buf, res, nil, "accept")
	if err == nil {
		t.Fatal("a filter batch with refusals must not exit 0: a script would read it as success")
	}
	requireBulkExitOne(t, err)
	if !strings.Contains(buf.String(), "accepted 5 of 8 suggestions") {
		t.Errorf("count line = %q", buf.String())
	}
	for _, want := range []string{"a1: refused", "b2: refused", "c3: refused"} {
		if !strings.Contains(buf.String(), "  refused  "+want) {
			t.Errorf("missing reason %q in %q", want, buf.String())
		}
	}
	// No id list, so nothing can be missing: every failure is a refusal.
	if strings.Contains(buf.String(), "no longer pending") {
		t.Errorf("the filter route has no id list to reconcile, got %q", buf.String())
	}
	if !strings.Contains(err.Error(), "3 of 8 suggestions were not accepted; the other 5 were committed") {
		t.Errorf("summary = %q", err.Error())
	}
}

// M1: a skip that carries no reason. Unreachable while atlas pairs every skip
// with an error string, which is exactly why it is pinned here — the clean-run
// gate must not depend on an invariant living in another repo.
func TestRenderBulkResultUnexplainedSkipsAreNotACleanRun(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{Processed: 3, Skipped: 2, Errors: []string{}}
	err := renderBulkResult(&buf, res, nil, "accept")
	if err == nil {
		t.Fatal("two skipped rows must not be reported as a clean run")
	}
	requireBulkExitOne(t, err)
	if !strings.Contains(buf.String(), "accepted 3 of 5 suggestions") {
		t.Errorf("count line must expose the two lost rows, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "2 suggestions were skipped with no stated reason") {
		t.Errorf("an unexplained skip must still be named, got %q", buf.String())
	}
	// The summary must agree with the count line rather than reporting the
	// empty Errors slice as "0 ... were not accepted" at exit 1.
	if !strings.Contains(err.Error(), "2 of 5 suggestions were not accepted; the other 3 were committed") {
		t.Errorf("summary must count skips, not error strings, got %q", err.Error())
	}
}

// N1: the mirror of the unexplained-skips case — more reasons than skips.
// Unreachable while atlas pairs the two, exactly as unreachable as the skip
// direction, and it used to render the same self-contradiction from the other
// side: "accepted 3 of 3 suggestions" over two refusals, then "0 of 3 ... were
// not accepted" at exit 1.
func TestRenderBulkResultMoreReasonsThanSkips(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{
		Processed: 3, Skipped: 0,
		Errors: []string{"a1: refused", "b2: refused"},
	}
	err := renderBulkResult(&buf, res, nil, "accept")
	requireBulkExitOne(t, err)

	// accounted = max(0, 2) = 2, so the two refused rows are inside the total.
	if !strings.Contains(buf.String(), "accepted 3 of 5 suggestions") {
		t.Errorf("count line must account for reasons the skip count omitted, got %q", buf.String())
	}
	if !strings.Contains(err.Error(), "2 of 5 suggestions were not accepted; the other 3 were committed") {
		t.Errorf("summary = %q", err.Error())
	}
	// The defect this pins: a summary asserting nothing failed while exiting 1.
	if strings.Contains(err.Error(), "0 of") {
		t.Errorf("summary must not report zero failures at exit 1, got %q", err.Error())
	}
	// Judgement call, pinned so it is a decision rather than an oversight: this
	// direction gets NO summary line of its own. Every excess row is a reason
	// string that the refusal loop has already printed on its own line, so
	// unlike a reason-less skip there is nothing left unnamed to report.
	if strings.Contains(buf.String(), "no stated reason") {
		t.Errorf("the reasons are the surface here; no extra line is warranted, got %q", buf.String())
	}
}

var (
	countLineRe = regexp.MustCompile(`ed (\d+) of (\d+) suggestion`)
	summaryRe   = regexp.MustCompile(`(\d+) of (\d+) suggestion.* the other (\d+) `)
)

// The count line and the summary are both derived from `accounted`, so they
// cannot disagree. Pinned across both directions of invariant drift and across
// both routes, because a disagreement between those two numbers is precisely
// what the M1 and N1 findings each were.
func TestRenderBulkResultSummaryAgreesWithCountLine(t *testing.T) {
	cases := []struct {
		name string
		res  rule_suggestions.BulkResult
		sent []string
	}{
		{"skips exceed reasons", rule_suggestions.BulkResult{Processed: 3, Skipped: 2, Errors: []string{}}, nil},
		{"reasons exceed skips", rule_suggestions.BulkResult{Processed: 3, Skipped: 0, Errors: []string{"a: x", "b: y"}}, nil},
		{"balanced on the filter route", rule_suggestions.BulkResult{Processed: 5, Skipped: 3, Errors: []string{"a: x", "b: y", "c: z"}}, nil},
		{"balanced with a shortfall", rule_suggestions.BulkResult{Processed: 1, Skipped: 1, Errors: []string{"d: w"}}, []string{"a", "b", "c", "d"}},
		{"every id raced away", rule_suggestions.BulkResult{Processed: 0, Skipped: 0, Errors: []string{}}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			res := tc.res
			err := renderBulkResult(&buf, &res, tc.sent, "accept")
			requireBulkExitOne(t, err)

			cm := countLineRe.FindStringSubmatch(buf.String())
			if cm == nil {
				t.Fatalf("no count line in %q", buf.String())
			}
			sm := summaryRe.FindStringSubmatch(err.Error())
			if sm == nil {
				t.Fatalf("no summary in %q", err.Error())
			}
			landed, countTotal := atoi(t, cm[1]), atoi(t, cm[2])
			failed, sumTotal, committed := atoi(t, sm[1]), atoi(t, sm[2]), atoi(t, sm[3])

			if countTotal != sumTotal {
				t.Errorf("totals disagree: count line says %d, summary says %d", countTotal, sumTotal)
			}
			if landed != committed {
				t.Errorf("count line says %d landed, summary says %d were committed", landed, committed)
			}
			if failed+committed != sumTotal {
				t.Errorf("%d failed + %d committed != %d total", failed, committed, sumTotal)
			}
			if landed != res.Processed {
				t.Errorf("count line says %d landed, server said %d", landed, res.Processed)
			}
		})
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}

func TestRenderBulkResultFilterRouteSkipsReconcile(t *testing.T) {
	var buf bytes.Buffer
	// nil sentIDs = the filter route, where the server chose the set and there
	// is no caller-supplied list to reconcile against.
	res := &rule_suggestions.BulkResult{Processed: 5, Skipped: 0, Errors: []string{}}
	if err := renderBulkResult(&buf, res, nil, "dismiss"); err != nil {
		t.Fatalf("filter route must not reconcile: %v", err)
	}
	if !strings.Contains(buf.String(), "dismissed 5 suggestions") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderBulkResultZeroMatches(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{Processed: 0, Skipped: 0, Errors: []string{}}
	if err := renderBulkResult(&buf, res, nil, "accept"); err != nil {
		t.Fatalf("zero matches on the filter route is not an error: %v", err)
	}
	if !strings.Contains(buf.String(), "no pending suggestions matched") {
		t.Errorf("output = %q", buf.String())
	}
}

// M3: every count that can be 1 reads as English.
func TestRenderBulkResultSingularWording(t *testing.T) {
	t.Run("one processed on a clean run", func(t *testing.T) {
		var buf bytes.Buffer
		res := &rule_suggestions.BulkResult{Processed: 1, Skipped: 0, Errors: []string{}}
		if err := renderBulkResult(&buf, res, nil, "accept"); err != nil {
			t.Fatalf("clean run: %v", err)
		}
		if !strings.Contains(buf.String(), "accepted 1 suggestion\n") {
			t.Errorf("output = %q", buf.String())
		}
		if strings.Contains(buf.String(), "1 suggestions") {
			t.Errorf("singular count must not take the plural noun, got %q", buf.String())
		}
	})

	t.Run("one missing id and one committed", func(t *testing.T) {
		var buf bytes.Buffer
		res := &rule_suggestions.BulkResult{Processed: 1, Skipped: 0, Errors: []string{}}
		err := renderBulkResult(&buf, res, []string{"a", "b"}, "dismiss")
		requireBulkExitOne(t, err)
		if !strings.Contains(buf.String(), "1 id was no longer pending") {
			t.Errorf("singular id line = %q", buf.String())
		}
		if strings.Contains(buf.String(), "id(s)") {
			t.Errorf("the id(s) form must be gone, got %q", buf.String())
		}
		if !strings.Contains(err.Error(), "the other 1 was committed") {
			t.Errorf("singular tail = %q", err.Error())
		}
	})

	t.Run("one unexplained skip", func(t *testing.T) {
		var buf bytes.Buffer
		res := &rule_suggestions.BulkResult{Processed: 2, Skipped: 1, Errors: []string{}}
		err := renderBulkResult(&buf, res, nil, "accept")
		requireBulkExitOne(t, err)
		if !strings.Contains(buf.String(), "1 suggestion was skipped with no stated reason") {
			t.Errorf("singular skip line = %q", buf.String())
		}
	})
}

// BulkResult.Errors holds the backend's own refusal strings, written straight
// to the writer under a "refused" label rather than returned, so qerrors.Exit's
// central pass never sees them.
func TestRenderBulkResultSanitizesTheServerRefusalStrings(t *testing.T) {
	var buf bytes.Buffer
	res := &rule_suggestions.BulkResult{
		Processed: 1,
		Skipped:   1,
		Errors:    []string{"column \x1b[31mamount\x1b[0m is locked\x00"},
	}
	err := renderBulkResult(&buf, res, []string{"a", "b"}, "accept")
	requireBulkExitOne(t, err)

	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x00') {
		t.Fatalf("a refusal reached the terminal with a live control byte: %q", out)
	}
	if !strings.Contains(out, `column \x1b[31mamount\x1b[0m is locked\x00`) {
		t.Errorf("missing the visible escape: %q", out)
	}
}
