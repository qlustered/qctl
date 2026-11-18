//nolint:revive // Tests intentionally match the command package directory.
package rule_upgrade

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	testFamilyID = uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	newRevID     = uuid.MustParse("0190eeee-1111-2222-3333-444444444444")
	oldRevID     = uuid.MustParse("0190aaaa-9999-2222-3333-444444444444")
)

func setupCommand() *cobra.Command {
	// The same persistent-flag set the notification package's harness registers
	// (internal/commands/notification/read_test.go): Bootstrap and the printers
	// read these, and a missing one fails as a flag lookup rather than as the
	// behaviour under test. SilenceUsage/SilenceErrors mirror the real root so a
	// RunE error does not dump the usage block into the captured stdout.
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	// Mirrors root.go: an -o value the verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func revision(id uuid.UUID, release string, isDefault bool) api.RuleRevisionTinySchema {
	return api.RuleRevisionTinySchema{
		ID: id, FamilyID: testFamilyID, Name: "phone_format", Slug: "phone_format",
		Release: release, IsDefault: isDefault, State: api.RuleState("enabled"),
	}
}

func registerResolution(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	revs := []api.RuleRevisionTinySchema{
		revision(oldRevID, "2.1.0", false),
		revision(newRevID, "3.0.0", true),
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{
				Results: revs, TotalRows: testutil.IntPtr(len(revs)),
			})
		})
	// Registered for EVERY revision id because the lenient resolver may land on
	// any release of the family. NOT "its business, not this test's" any more:
	// TestNotifyByNameDoesNotReportASubstitution is only non-vacuous because it
	// lands on 2.1.0, the NON-default -- if it landed on 3.0.0 the note could
	// not fire whether it were gated or not, and the test would pass for the
	// wrong reason. Verified by ungating the note and watching that test go
	// red, which it does.
	for _, rev := range revs {
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String(),
			func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionsListSchema{
					FamilyID: testFamilyID, Name: "phone_format", Results: revs,
				})
			})
	}
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"rule-upgrade", "notify"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// With no --release the command must target the family's DEFAULT revision:
// it is the only one the backend accepts, so making the user name it would be
// a demand for the one answer that is never a choice.
func TestNotifyTargetsTheDefaultRevision(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	posted := false
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			posted = true
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 4})
		})

	out, _, err := run(t, "phone_format", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !posted {
		t.Fatal("the blast did not reach the default revision")
	}
	for _, want := range []string{"rule-upgrade/" + newRevID.String(), "notified", "4"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// The backend answers 404 for a non-default revision, which qctl would report
// as "not found" for a revision that plainly exists. Catch it client-side, as
// bad usage, and send NOTHING.
func TestNotifyRejectsANonDefaultReleaseWithoutPosting(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	posted := false
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+oldRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) { posted = true })

	_, _, err := run(t, "phone_format", "--release", "2.1.0", "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if posted {
		t.Error("a rejected release still sent the blast")
	}
	for _, want := range []string{"2.1.0", "not the default", "3.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
}

// Declining at the prompt must write nothing, and is a choice rather than a
// usage error.
func TestNotifyDeclinedAtThePromptSendsNothing(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	posted := false
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) { posted = true })

	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	// Assert the VALUE, not an inequality. `got != ExitBadUsage` passes for 1,
	// 3, 4 and anything else, so it could not fail for the reason it exists:
	// a refactor that turned a decline into a not-found (exit 3) would keep it
	// green. cmdutil.Confirm returns a bare fmt.Errorf("aborted") on a decline,
	// which carries no *qerrors.Error, so GetExitCode falls through to 1.
	if _, _, err := run(t, "phone_format"); err == nil {
		t.Fatal("expected the decline to be an error")
	} else if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("declining is a choice, not bad usage; exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	if posted {
		t.Error("a declined prompt still sent the blast")
	}
}

// Non-interactive stdin with no --yes is bad usage, and the message must name
// --yes because that is the fix.
func TestNotifyNonInteractiveWithoutYesIsExitTwo(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)

	_, _, err := run(t, "phone_format")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error must name --yes, got: %v", err)
	}
}

func TestNotifyMessageReachesTheBody(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	var body api.BlastNotificationRequestSchema
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&body)
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 1})
		})

	if _, _, err := run(t, "phone_format", "--message", "before Friday", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Msg == nil || *body.Msg != "before Friday" {
		t.Errorf("body msg = %+v, want %q", body.Msg, "before Friday")
	}
}

// Omitting --message must send msg: null, NOT an empty string. The backend
// replaces a notice's text only when a msg is supplied, so a "" here would
// blank the text of every notice this re-sends, in other people's inboxes.
// The nil-vs-empty distinction is the backend's contract, not a qctl choice,
// which is why it gets its own test: decoding into the wire schema is what
// makes this mutation-proof, since {"msg":""} decodes to a non-nil pointer.
func TestNotifyWithoutMessageSendsNull(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	var body api.BlastNotificationRequestSchema
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&body)
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 1})
		})

	if _, _, err := run(t, "phone_format", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Msg != nil {
		t.Errorf("body msg = %q, want null: omitting --message must leave each "+
			"notice's existing text alone", *body.Msg)
	}
}

// Zero reactivations is a legitimate outcome — nobody had dismissed it — so it
// exits 0 and explains itself on stderr rather than looking like a failure.
func TestNotifyZeroReactivatedExplainsItself(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 0})
		})

	_, errOut, err := run(t, "phone_format", "--yes")
	if err != nil {
		t.Fatalf("zero reactivations must not be an error: %v", err)
	}
	if !strings.Contains(errOut, "Nobody had dismissed") {
		t.Errorf("stderr missing the explanation\n%s", errOut)
	}
}

// Only the revision's creator may blast. The 403 keeps its exit code and gains
// the reason, which the server's own message does not give.
func TestNotifyForbiddenNamesTheCreatorRule(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":{"msg":"Only the revision creator can blast notifications"}}`))
		})

	_, _, err := run(t, "phone_format", "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
	if !strings.Contains(err.Error(), "created revision 3.0.0") {
		t.Errorf("error must name the creator rule, got: %v", err)
	}
}

// --release "" is a script whose variable did not expand. Reading it by
// emptiness would silently mean "use the default" and blast whatever the
// family's default happens to be for a release the script never named --
// whereas every other unknown release exits 2. Flags().Changed is what tells
// "not passed" from "passed empty", and this test fails if the read goes back
// to comparing against "".
func TestNotifyWithAnEmptyReleaseIsExitTwo(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	posted := false
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) { posted = true })

	_, _, err := run(t, "phone_format", "--release", "", "--yes")
	if err == nil {
		t.Fatal("expected --release \"\" to be rejected, not treated as the default")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if posted {
		t.Error("--release \"\" blasted the default revision instead of exiting 2")
	}
}

// Naming a NON-DEFAULT revision by id resolves to its family and lands on the
// default, because the notice is per family. That substitution must be stated:
// `get rule-upgrades -o json` prints each item's current_revision_id, so
// pasting one here under --yes would otherwise blast a different revision in
// silence -- while --release 2.1.0 for the same revision exits 2.
func TestNotifyReportsARevisionItSubstituted(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 1})
		})

	_, errOut, err := run(t, oldRevID.String(), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut, oldRevID.String()) || !strings.Contains(errOut, "3.0.0") {
		t.Errorf("the substitution was not reported; stderr = %q", errOut)
	}
}

// ...but a family NAME is the ordinary case and must NOT trigger that note.
// ResolveRuleIDAny "picks the first match" among a family's releases, which is
// routinely not the default, so an ungated note would fire on nearly every
// invocation and mean nothing.
//
// This test is only meaningful because the resolver lands on 2.1.0 here, the
// NON-default: it is the case where an ungated note WOULD fire, so a green
// result says the gate works rather than that the note had nothing to say.
// The POST assertion below pins that -- it goes to the default revision while
// the resolution went through the other one.
func TestNotifyByNameDoesNotReportASubstitution(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	postedTo := ""
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			postedTo = newRevID.String()
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 1})
		})

	_, errOut, err := run(t, "phone_format", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if postedTo != newRevID.String() {
		t.Fatalf("blast went to %q, want the default revision %s", postedTo, newRevID)
	}
	if strings.Contains(errOut, "the default") {
		t.Errorf("a family name must not report a substitution; stderr = %q", errOut)
	}
}

// selectRevision's two remaining branches, both unexercised until now: a
// family with no default revision at all, and a --release naming a release
// the family does not have. Both are exit 2 and neither may reach the wire.
func TestNotifySelectRevisionRejections(t *testing.T) {
	noDefault := []api.RuleRevisionTinySchema{revision(oldRevID, "2.1.0", false)}
	withDefault := []api.RuleRevisionTinySchema{
		revision(oldRevID, "2.1.0", false), revision(newRevID, "3.0.0", true),
	}
	for _, tc := range []struct {
		name, want string
		revs       []api.RuleRevisionTinySchema
		args       []string
	}{
		{"no default revision", "no default revision", noDefault, []string{"phone_format", "--yes"}},
		{"unknown release", "has no release", withDefault, []string{"phone_format", "--release", "9.9.9", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			revs := tc.revs
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionListSchema{
						Results: revs, TotalRows: testutil.IntPtr(len(revs)),
					})
				})
			for _, rev := range revs {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String(),
					func(w http.ResponseWriter, r *http.Request) {
						testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionsListSchema{
							FamilyID: testFamilyID, Name: "phone_format", Results: revs,
						})
					})
			}
			blasted := false
			for _, rev := range revs {
				mock.RegisterHandler("POST",
					"/api/orgs/"+testOrgID+"/rule-revisions/"+rev.ID.String()+"/blast-upgrade-notifications",
					func(w http.ResponseWriter, r *http.Request) { blasted = true })
			}

			_, _, err := run(t, tc.args...)
			if err == nil {
				t.Fatal("expected exit 2")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message = %q, want it to mention %q", err.Error(), tc.want)
			}
			if blasted {
				t.Error("a client-side rejection still reached the wire")
			}
		})
	}
}

// A 403 must NOT be reported as "only the creator can do this" flatly. The
// route carries three independent 403 sources and the creator check is the
// LAST of them -- RULE_INSTANTIATE and the org-access check run first -- so a
// flat assertion contradicts the server's own message printed directly above
// it and sends the reader after the wrong person.
func TestNotifyDoesNotAssertTheCreatorIsTheCauseOfEvery403(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden,
				map[string]any{"detail": "The requested resource belongs to a different organization"})
		})

	_, _, err := run(t, "phone_format", "--yes")
	if err == nil {
		t.Fatal("expected the 403 to be an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "usual cause") {
		t.Errorf("the creator explanation must be hedged, not asserted; got %q", msg)
	}
	// The hint has to name a command that actually shows the creator.
	// `get rule-revisions` carries it in neither its default nor its verbose
	// column set; `describe rule` prints a "Created by" line.
	if strings.Contains(msg, "get rule-revisions") {
		t.Errorf("the hint names a command that does not show the creator; got %q", msg)
	}
	if !strings.Contains(msg, "describe rule phone_format --release 3.0.0") {
		t.Errorf("the hint should name describe rule; got %q", msg)
	}
}

// The non-403 wrapper must not double-prefix: rule_upgrades.Blast already
// passes "request failed" as its context message and buildHTTPError renders
// that as a prefix, so adding "failed to re-send ...:" on top reads as
// "failed to re-send the X upgrade notice: request failed: <detail>".
func TestNotifyDoesNotDoublePrefixANon403(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError,
				map[string]any{"detail": "boom"})
		})

	_, _, err := run(t, "phone_format", "--yes")
	if err == nil {
		t.Fatal("expected the 500 to be an error")
	}
	if strings.Contains(err.Error(), "failed to re-send") {
		t.Errorf("double-prefixed message: %q", err.Error())
	}
}

// -o json writes the server's {message, reactivated_count}; the outcome line
// moves to stderr, and -o name, which has nothing to name, exits 2.
func TestNotifyWritesTheResponse(t *testing.T) {
	mock := newTestEnv(t)
	registerResolution(t, mock)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+newRevID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				api.BlastNotificationResponseSchema{ReactivatedCount: 4, Message: "sent"})
		})

	stdout, stderr, err := run(t, "phone_format", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "notified") {
		t.Errorf("stderr missing the outcome line:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["reactivated_count"] != float64(4) || got["message"] != "sent" {
		t.Errorf("document = %v, want the response", got)
	}

	_, _, err = run(t, "phone_format", "--yes", "-o", "name")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("-o name: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
}
