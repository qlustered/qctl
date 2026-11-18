package explain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/root"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// alertPage is the page these tests read: one with enough gotchas that a
// count, a full listing and a filtered one are each distinguishable.
func alertPage(t *testing.T) catalog.Resource {
	t.Helper()
	res, ok := catalog.ResourceNamed(root.GetRootCmd(), "alert")
	if !ok {
		t.Fatal("no alert resource in the catalog")
	}
	if len(res.Gotchas) < 5 {
		t.Fatalf("alert page has %d gotchas; these tests need at least 5", len(res.Gotchas))
	}
	return res
}

func TestExplainCountsGotchasByDefault(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	res := alertPage(t)

	out, _, err := runExplain(t, "explain", "alert")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain alert: exit %d, want 0: %v", code, err)
	}
	want := fmt.Sprintf("GOTCHAS:  %d, not shown: 'qctl explain alert --gotchas'", len(res.Gotchas))
	if !strings.Contains(out, want) {
		t.Errorf("default page must count the gotchas as %q; got:\n%s", want, out)
	}
	for _, section := range []string{"USAGE:", "EXAMPLES:"} {
		if !strings.Contains(out, section) {
			t.Errorf("default page lost its %s section:\n%s", section, out)
		}
	}
	for _, g := range res.Gotchas {
		if strings.Contains(out, g.Text) {
			t.Errorf("default page printed a gotcha in full: %.80s", g.Text)
		}
	}
}

func TestExplainGotchasPrintsEveryBullet(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	res := alertPage(t)

	out, _, err := runExplain(t, "explain", "alert", "--gotchas")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain alert --gotchas: exit %d, want 0: %v", code, err)
	}
	for _, want := range []string{"USAGE:", "EXAMPLES:", "\nGOTCHAS:\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("--gotchas output missing %q:\n%s", want, out)
		}
	}
	for _, g := range res.Gotchas {
		if !strings.Contains(out, "  - "+g.Text+"\n") {
			t.Errorf("--gotchas output missing a bullet: %.80s", g.Text)
		}
	}
}

func TestExplainGrepPrintsOnlyTheMatchingGotchas(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	res := alertPage(t)

	var matched, unmatched int
	for _, g := range res.Gotchas {
		if strings.Contains(strings.ToLower(g.Text), "next actions") {
			matched++
		} else {
			unmatched++
		}
	}
	if matched == 0 || unmatched == 0 {
		t.Fatalf("the alert page must have bullets on both sides of \"next actions\" (%d match, %d do not)",
			matched, unmatched)
	}

	// Mixed case on purpose: the page spells it NEXT ACTIONS, and the match
	// ignores case on both sides.
	out, _, err := runExplain(t, "explain", "alert", "--grep", "Next Actions")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain alert --grep 'Next Actions': exit %d, want 0: %v", code, err)
	}
	header := fmt.Sprintf("GOTCHAS matching \"Next Actions\": %d of %d\n", matched, len(res.Gotchas))
	if !strings.HasPrefix(out, "RESOURCE: alert\n"+header) {
		t.Errorf("--grep output must open with the resource and %q; got:\n%s", header, out)
	}
	for _, g := range res.Gotchas {
		has := strings.Contains(out, g.Text)
		if want := strings.Contains(strings.ToLower(g.Text), "next actions"); has != want {
			t.Errorf("--grep printed=%v for a bullet whose match is %v: %.80s", has, want, g.Text)
		}
	}
	if strings.Contains(out, "USAGE:") {
		t.Errorf("--grep must print the matching gotchas alone, not the page:\n%s", out)
	}
}

func TestExplainGrepWithNoMatchSaysSoAndSucceeds(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	res := alertPage(t)

	out, _, err := runExplain(t, "explain", "alert", "--grep", "zzqqxxnomatch")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit %d, want 0: %v", code, err)
	}
	want := fmt.Sprintf("GOTCHAS matching \"zzqqxxnomatch\": 0 of %d\n", len(res.Gotchas))
	if !strings.HasSuffix(out, want) {
		t.Errorf("no-match output must end with %q; got:\n%s", want, out)
	}
}

func TestExplainPageFlagsWithoutAPageExitTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	for _, args := range [][]string{
		{"explain", "table-rule", "--gotchas", "-o", "json"},
		{"explain", "table-rule", "--grep", "state", "-o", "yaml"},
		{"explain", "table-rule.spec", "--grep", "state"},
		{"explain", "apply", "--gotchas"},
		{"explain", "alert", "--grep", ""},
		{"explain", "alert", "--grep", "  "},
	} {
		_, _, err := runExplain(t, args...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("qctl %s: exit %d, want %d: %v", strings.Join(args, " "), code, qerrors.ExitBadUsage, err)
		}
	}
}
