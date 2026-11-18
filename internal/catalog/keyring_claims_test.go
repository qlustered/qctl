package catalog_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A banned claim here is a SHAPE, not a fixed substring, and the reason is
// that a fixed substring has now been evaded twice by one interposed word.
// First `OS`: the list held "skip the keyring entirely" and the README said
// "Skip the OS keyring entirely". That was closed by folding "os keyring" to
// "keyring" -- and the very next wave shipped "keyring login LEFT is cleared"
// past the entry "keyring login is cleared", one word further along. Adding
// the new spelling to a list fixes that spelling and nothing else.
//
// So a shape is an ordered sequence of alternative-sets with a bounded number
// of WORDS allowed between consecutive parts. "keyring login is cleared",
// "keyring login left is cleared" and "keyring login left behind gets removed"
// are one shape.
//
// The gap is words only -- [letters/digits/'/-] -- so punctuation ends it. That
// is deliberate: it keeps the span inside one clause, which is what stops the
// shape reaching across a comma or a full stop into an unrelated sentence and
// reporting prose that is true.
type claimShape struct {
	// what the shape says, quoted back in the failure so the reader knows
	// which claim was matched rather than only which words.
	says string
	re   *regexp.Regexp
}

// shape compiles parts into a claimShape. gaps[i] is how many words may sit
// between parts[i] and parts[i+1]; a missing entry means zero.
func shape(says string, gaps []int, parts ...[]string) claimShape {
	const word = `[\p{L}\p{N}'’-]+`
	var b strings.Builder
	for i, alts := range parts {
		if i > 0 {
			gap := 0
			if i-1 < len(gaps) {
				gap = gaps[i-1]
			}
			fmt.Fprintf(&b, `(?:[ ]%s){0,%d}[ ]`, word, gap)
		}
		b.WriteString(`(?:`)
		for j, alt := range alts {
			if j > 0 {
				b.WriteString(`|`)
			}
			b.WriteString(regexp.QuoteMeta(alt))
		}
		b.WriteString(`)`)
	}
	return claimShape{says: says, re: regexp.MustCompile(`\b` + b.String() + `\b`)}
}

// The entry an earlier keyring login left, however it is named.
var theOlderKeyringEntry = []string{
	"keyring login", "earlier login", "login left", "older keyring entry",
}

// unconditionalDeleteClaims are wordings that promise the OUTCOME of the
// keyring delete rather than the attempt. Five of them shipped: having
// correctly said that `auth logout` still TRIES the keyring under the
// plaintext opt-in, five surfaces then said the old entry "is cleared" / "is
// still removed" / "is still cleaned up".
//
// deleteFromKeyring can fail. When the keyring is up and refuses, `auth
// logout` reports the entry as surviving and exits non-zero -- so a sentence
// promising the entry is gone is the branch's signature defect one clause
// later than where it was fixed. The honest form names the attempt: "still has
// its removal attempted", "still attempts to remove", "from trying to clear".
var unconditionalDeleteClaims = []claimShape{
	// "...an entry an earlier keyring login left is cleared too"
	shape("the entry an earlier keyring login left is gone",
		[]int{4, 3},
		theOlderKeyringEntry,
		[]string{"is", "was", "are", "were", "gets", "got", "has", "have", "had", "will", "would", "does", "ends"},
		[]string{"cleared", "removed", "cleaned", "deleted", "gone", "wiped", "erased"}),
	// "...clears an entry an earlier keyring login left"
	shape("qctl clears the entry an earlier keyring login left",
		[]int{6},
		[]string{"clears", "clearing", "removes", "removing", "cleans", "cleaning", "deletes", "deleting", "wipes", "wiping"},
		theOlderKeyringEntry),
}

// unscopedPlaintextClaims are the other half of the same defect: wordings that
// shipped on four separate surfaces -- `auth logout --help`, `qctl guide
// authenticate`, and two bullets of `qctl explain session` -- each saying that
// QCTL_ALLOW_PLAINTEXT_TOKENS makes qctl skip the keyring, full stop.
//
// It does not. CredentialStore.Delete calls deleteFromKeyring unconditionally,
// deliberately, so a user who opts into the plaintext store after an earlier
// keyring login still has that older entry's removal attempted -- see Delete's
// doc comment and internal/testutil/config.go, which needed a keyring stub for
// exactly this reason. The opt-in scopes to Store and Retrieve. So `auth
// logout` in plaintext mode still performs a real keyring delete, can still
// name a keyring entry in its failure message, and on a desktop can still
// raise an unlock prompt -- from the very keyring the help text said it never
// touches.
//
// A correctly SCOPED use -- "skips the keyring on purpose when storing" -- is
// deliberately not matched: what is banned is the qualifier that drops the
// condition, not the verb.
var unscopedPlaintextClaims = []claimShape{
	shape("the plaintext opt-in skips the keyring, full stop",
		[]int{2},
		[]string{"skip the keyring", "skips the keyring", "skipping the keyring", "skipped the keyring",
			"skip it", "skips it", "skipped", "bypass the keyring", "bypasses the keyring", "bypassing the keyring"},
		[]string{"entirely", "altogether", "completely", "and store", "and read", "full stop"}),
}

// normalise lowercases text, folds "OS keyring" to "keyring", and collapses
// every run of whitespace to one space.
//
// The fold means one shape covers both spellings, and a writer who adds "OS"
// to a banned phrase does not thereby unban it -- that single word hid the
// fifth site through a whole fix wave. Collapsing whitespace is the same
// argument for line breaks: the prose swept here is hard-wrapped markdown and
// Go string literals, where a claim is routinely split across two lines.
func normalise(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(text), "os keyring", "keyring")), " ")
}

// TestNoShippedSurfaceSaysThePlaintextOptInSkipsTheKeyring sweeps the three
// help surfaces that are embedded in the binary -- every command's Short/Long
// and flag usage, every `qctl guide` topic, and every `qctl explain` resource
// page -- rather than the handful of files a reviewer happened to name. Four
// sites carried this claim and a brief listing three of them would have left
// one.
//
// Three embedded surfaces is not every user-facing text qctl ships. The prose
// files a reader outside the binary looks at -- README.md and the top level of
// docs/ -- are swept too, by path, the way docs_test.go already reads
// ../../docs/patterns.md; the README carried this claim through the whole of
// wave E because it was not swept and the grep behind that wave lacked the
// word "OS".
//
// Still NOT covered, and still needing a grep: RUNTIME strings (Store's
// keyring-refused warning, describeFailedDelete's leads and remedies,
// unreadableCredentialError), which are built from values rather than declared.
func TestNoShippedSurfaceSaysThePlaintextOptInSkipsTheKeyring(t *testing.T) {
	check := func(surface, text string) {
		t.Helper()
		lower := normalise(text)
		for _, s := range unconditionalDeleteClaims {
			if hit := s.re.FindString(lower); hit != "" {
				t.Errorf("%s says %q -- it reads as %q.\n"+
					"The delete is ATTEMPTED, not guaranteed: a keyring that is up and refuses\n"+
					"makes `auth logout` report that entry as surviving and exit non-zero.\n"+
					"Say what is attempted, not what is achieved.",
					surface, hit, s.says)
			}
		}
		for _, s := range unscopedPlaintextClaims {
			if hit := s.re.FindString(lower); hit != "" {
				t.Errorf("%s says %q -- it reads as %q.\n"+
					"The plaintext opt-in scopes to Store and Retrieve; Delete always tries the keyring,\n"+
					"so a logout that reaches the delete still touches it -- and can still prompt for an\n"+
					"unlock -- under that opt-in.\n"+
					"CredentialStore.Delete's doc comment in internal/auth/credentials.go states that scoping;\n"+
					"scope the claim the same way instead of dropping the condition.",
					surface, hit, s.says)
			}
		}
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		text := cmd.Short + "\n" + cmd.Long
		// Flag usage strings are shipped help too, and the one flag that
		// matters here -- --allow-plaintext-token-store -- lives only there.
		cmd.Flags().VisitAll(func(f *pflag.Flag) { text += "\n" + f.Usage })
		cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { text += "\n" + f.Usage })
		check("`"+cmd.CommandPath()+" --help`", text)
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(realRoot(t))

	for _, name := range guide.TopicNames() {
		body, err := guide.TopicBody(name)
		if err != nil {
			t.Fatalf("guide.TopicBody(%q) error = %v", name, err)
		}
		check("`qctl guide "+name+"`", body)
	}

	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("resources.Pages() error = %v", err)
	}
	for _, p := range pages {
		check("`qctl explain "+p.Name+"`", p.Raw)
	}

	// The prose surfaces, by path. Not filepath.Glob("../../docs/*.md") plus
	// README alone -- a glob that matched nothing would make this half of the
	// sweep vanish silently, which is the failure mode the whole test exists
	// to prevent -- so the glob's result is checked for being empty.
	docFiles, err := filepath.Glob("../../docs/*.md")
	if err != nil {
		t.Fatalf("glob docs/*.md: %v", err)
	}
	if len(docFiles) == 0 {
		t.Fatal("no docs/*.md matched: the prose half of this sweep is not running")
	}
	for _, path := range append([]string{"../../README.md"}, docFiles...) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		check(strings.TrimPrefix(path, "../../"), string(body))
	}
}

// TestUnconditionalDeleteClaimShapesSurviveRewording is the test for the test.
// The list it guards was evaded twice by one interposed word, so the property
// that matters is not "this exact sentence is banned" but "rewording it does
// not un-ban it". Each want-caught line below is a different way of saying the
// same false thing; each want-allowed line is prose that ships today, or a
// near neighbour of it, and must stay green.
func TestUnconditionalDeleteClaimShapesSurviveRewording(t *testing.T) {
	caught := func(text string) bool {
		lower := normalise(text)
		for _, s := range append(append([]claimShape{}, unconditionalDeleteClaims...), unscopedPlaintextClaims...) {
			if s.re.MatchString(lower) {
				return true
			}
		}
		return false
	}

	for _, text := range []string{
		// The five wordings that actually shipped.
		"an entry an earlier keyring login left is cleared too",
		"an entry an earlier keyring login is cleared",
		"an entry an earlier keyring login is still removed",
		"the entry an earlier keyring login left is still cleaned up",
		"nothing stops `auth logout` from clearing an entry an earlier keyring login left",
		// Rewordings that no substring in the old list would have caught.
		"an entry an earlier keyring login left behind gets removed too",
		"an old entry from an earlier keyring login will be cleaned up as well",
		"any entry a previous keyring login left on this machine is deleted too",
		"logout removes the entry that an earlier keyring login left",
		// The other half of the same defect, reworded.
		"the opt-in skips the keyring entirely",
		"the opt-in skips the OS keyring altogether",
		"that flag skips it completely",
	} {
		if !caught(text) {
			t.Errorf("not caught: %q\nA rewording of a banned claim must still fail this sweep.", text)
		}
	}

	for _, text := range []string{
		// Shipped prose, and the honest forms the branch settled on.
		"an entry an earlier keyring login left still has its removal attempted, and is reported if it demonstrably survived",
		"an entry an earlier keyring login left has its removal attempted too",
		"it does not stop `auth logout` from trying to clear an entry an earlier keyring login left",
		"it still attempts to remove an entry an earlier keyring login left — `nothing to do` is printed only once that attempt comes back clean",
		"--allow-plaintext-token-store skips the keyring on purpose when storing",
		"set QCTL_ALLOW_PLAINTEXT_TOKENS=1 to skip the keyring on purpose instead of relying on the fallback",
		"it does not skip the delete",
		// A neighbouring true sentence that a span matcher must not reach into.
		"the credential came from an earlier keyring login. The fallback file is cleared regardless.",
	} {
		if caught(text) {
			t.Errorf("false positive: %q\nThis wording is true; the sweep must not fail on it.", text)
		}
	}
}
