package rows

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupRowsTestCommand builds the root the real one builds, minus the commands
// this package does not exercise. --verbose MUST be CountP: cmdutil.Bootstrap
// reads it as a count.
func setupRowsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 0, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	// The real root types cobra's own ParseFlags failures as bad usage, and
	// subcommands inherit it because FlagErrorFunc() walks up to the parent.
	// Without it here an unknown flag exits 1 in tests and 2 in the shipped
	// binary, and a test asserting the contract would be asserting the harness.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	// The real root validates --output against each command's declared set from
	// PersistentPreRunE. Without this hook `-o name` would run the command and
	// print the table here while exiting 2 in the shipped binary, and a test
	// asserting the contract would be asserting the harness. Same reason the
	// FlagErrorFunc above is here.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newRowsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	registerTableResolve(mock)
	return mock
}

// registerTableResolve answers the name-only lookup `orders` makes, resolving it
// to dataset 42 — the id every route below is registered on.
func registerTableResolve(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
}

// testCloudSourceID is the one cloud source the fixtures know about, fixed the
// way registerTableResolve fixes dataset 42.
const testCloudSourceID = 7

// registerCloudSourceResolve answers the --cloud-source-id lookup
// cmdutil.ResolveCloudSource makes. Without it the narrowing costs a 404 and
// the command fails at the SCOPE, which would let a selector test pass for the
// wrong reason.
func registerCloudSourceResolve(mock *testutil.MockAPIServer) {
	sid := strconv.Itoa(testCloudSourceID)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/"+sid, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"id":`+sid+`,"name":"s3-nightly"}`))
	})
}

// recorded is what one registered route saw.
//
// queries and bodies keep EVERY hit, in order; query and body are the LAST
// one. Both halves are here because both are wanted: nearly every test in this
// package drives a verb that makes one call and asserts on it by name, which
// query/body read cleanly, while a route hit twice in a run — a retry, a
// confirm path that counts before it acts — is exactly the case the singular
// fields cannot express. They used to be the only fields, so a second hit
// silently overwrote the first and an assertion on "the" body was an assertion
// on whichever call happened to be last.
//
// search_replace_test.go's recordedCalls is the same idea reached
// independently for the one verb that already needed it; this is that shape
// on the shared recorder.
type recorded struct {
	hits    int
	queries []string
	bodies  []string

	// query and body are the most recent hit, and are meaningful only
	// alongside hits: on a route that was never called they are "" — the same
	// value an empty query or an empty body produces.
	query string
	body  string
}

func record(mock *testutil.MockAPIServer, method, path, resp string) *recorded {
	got := &recorded{}
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		got.hits++
		got.query = r.URL.RawQuery
		got.queries = append(got.queries, got.query)
		// A bodiless route sends no body at all, and http.Request.Body is
		// nil there rather than an empty reader. The append is outside the
		// guard so bodies stays index-aligned with queries and with hits: a
		// bodiless call contributes "", not a missing element that would shift
		// every later index by one.
		got.body = ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			got.body = strings.TrimSpace(string(b))
		}
		got.bodies = append(got.bodies, got.body)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(resp))
	})
	return got
}

func runRows(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupRowsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// shellWords splits a command line qctl printed the way a POSIX shell would,
// for the quoting shellArg emits: bare words, single-quoted runs, and a
// backslash-escaped character outside quotes (the splice an embedded quote
// needs). An UNQUOTED shell metacharacter fails the test instead of being
// split, because a printed line carrying one does something other than what it
// reads as when pasted — which is the defect these lines are checked for.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var cur strings.Builder
	inWord, inSingle := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle, inWord = true, true
		case c == '\\':
			if i+1 == len(line) {
				t.Fatalf("trailing backslash in %q", line)
			}
			i++
			cur.WriteByte(line[i])
			inWord = true
		case c == ' ':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case strings.IndexByte("\"$`;&|<>()*?[]{}#~!\t\n", c) >= 0:
			t.Fatalf("unquoted shell metacharacter %q in printed command line %q", c, line)
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inSingle {
		t.Fatalf("unterminated single quote in %q", line)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// cobraUnknownFlag is cobra's own refusal of a flag the command does not
// register, obtained BY ASKING COBRA rather than by typing what it says today.
//
// Several verbs in this group deliberately leave a flag unregistered and let
// cobra answer — addScopeFlags registers the narrowing flags only for the
// scopes a verb's support{} lists, and that is the designed refusal there. The
// tests for it asserted the literal "unknown flag: --cloud-source-id", so a
// cobra upgrade that reworded its error would have turned a dozen tests red
// across seven files while nothing in qctl had changed.
//
// Asserting the exit code alone is not the alternative: every client-side
// refusal in this group is bad usage too, so an exit-code-only test passes for
// a run refused for some entirely different reason and proves nothing about
// the flag being unregistered. Deriving the expected text keeps that
// discrimination and drops the transcription — the probe command registers
// nothing, so whatever it answers IS the unknown-flag wording of the cobra
// this build links.
func cobraUnknownFlag(t *testing.T, flag string) string {
	t.Helper()
	probe := &cobra.Command{
		Use:           "probe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	probe.SetOut(io.Discard)
	probe.SetErr(io.Discard)
	probe.SetArgs([]string{"--" + flag})
	err := probe.Execute()
	if err == nil {
		t.Fatalf("cobra accepted --%s on a command that registers no flags", flag)
	}
	return err.Error()
}
