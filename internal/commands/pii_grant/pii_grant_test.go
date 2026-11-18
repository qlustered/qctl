package pii_grant //nolint:revive // Package name mirrors the command.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

const (
	grantsPath  = "/api/orgs/" + testOrgID + "/datasets/42/pii-grants"
	revokePath  = grantsPath + "/7/revoke"
	membersPath = "/api/orgs/" + testOrgID + "/datasets/42/users"
)

var (
	granteeID = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	grantorID = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
)

// setupTestCommand wires what root.go wires for the exit codes asserted here:
// arity and flag errors, a missing required flag and an unknown -o value all
// exit 2, and kind/ prefixes are stripped from positionals.
func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		return nil
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
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

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"pii-grant"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func assertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// serve answers method+path with code and body, and returns the request
// bodies it received.
func serve(mock *testutil.MockAPIServer, method, path string, code int, body []byte) *[][]byte {
	var bodies [][]byte
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		bodies = append(bodies, raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	})
	return &bodies
}

// wireBody encodes v the way -o json does, so output can be compared byte
// for byte.
func wireBody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return append(raw, '\n')
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", raw, err)
	}
	return body
}

var grantExpiry = time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC)

func activeGrant() pii_grants.Grant {
	return pii_grants.Grant{
		ID: 7, DatasetID: 42, State: api.PiiGrantStateActive,
		GranteeUserID: granteeID, GranteeEmail: "alice@example.com",
		GrantedByUserID: grantorID, GrantedByEmail: "admin@example.com",
		Reason: "INC-4411", CreatedAt: grantExpiry.Add(-time.Hour), ExpiresAt: grantExpiry,
	}
}

func revokedGrant(reason api.PiiAccessGrantRevokeReason, note *string) pii_grants.Grant {
	g := activeGrant()
	at := grantExpiry.Add(-30 * time.Minute)
	email := "admin@example.com"
	g.State, g.RevokedAt, g.RevokedByUserID, g.RevokedByEmail = api.PiiGrantStateRevoked, &at, &grantorID, &email
	g.RevokeReason, g.RevokeNote = &reason, note
	return g
}

func serveMembers(mock *testutil.MockAPIServer) {
	serve(mock, http.MethodGet, membersPath, http.StatusOK, []byte(`[`+
		`{"user":{"id":"`+granteeID.String()+`","email":"Alice@Example.com","first_name":"A","last_name":"L"},`+
		`"role":"read_only","permission_level":"read_only","effective_permission_level":"read_only"},`+
		`{"user":{"id":"`+grantorID.String()+`","email":"admin@example.com","first_name":"B","last_name":"M"},`+
		`"role":"admin","permission_level":"owner","effective_permission_level":"owner"}]`))
}

func TestCreateUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":           {"--to-id", granteeID.String(), "--reason", "r"},
		"no user":            {"--table-id", "42", "--reason", "r"},
		"both users":         {"--table-id", "42", "--to", "a@b.c", "--to-id", granteeID.String(), "--reason", "r"},
		"malformed to-id":    {"--table-id", "42", "--to-id", "not-a-uuid", "--reason", "r"},
		"blank to":           {"--table-id", "42", "--to", "  ", "--reason", "r"},
		"no reason":          {"--table-id", "42", "--to-id", granteeID.String()},
		"blank reason":       {"--table-id", "42", "--to-id", granteeID.String(), "--reason", "  "},
		"unknown duration":   {"--table-id", "42", "--to-id", granteeID.String(), "--reason", "r", "--duration", "2h"},
		"positional present": {"orders", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newTestEnv(t)
			_, _, err := run(t, append([]string{"create", "--yes"}, args...)...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertRequests(t, mock)
		})
	}
}

func TestCreateSendsTheGrant(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, grantsPath, http.StatusCreated, wireBody(t, activeGrant()))

	out, _, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(),
		"--duration", "8H", "--reason", "  INC-4411  ", "--yes")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertRequests(t, mock, "POST "+grantsPath)
	got := decodeBody(t, (*bodies)[0])
	want := map[string]any{"grantee_user_id": granteeID.String(), "duration": "8h", "reason": "INC-4411"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	wantOut := "pii-grant/7 created: alice@example.com sees table/42's personal data unmasked until " +
		grantExpiry.Local().Format(time.RFC3339) + "\nNext: qctl get pii-grants --table-id 42\n"
	if out != wantOut {
		t.Errorf("output =\n%q\nwant\n%q", out, wantOut)
	}
}

func TestCreateDefaultsToOneHour(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, grantsPath, http.StatusCreated, wireBody(t, activeGrant()))
	if _, _, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r", "--yes"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := decodeBody(t, (*bodies)[0])["duration"]; got != "1h" {
		t.Errorf("duration = %v, want 1h", got)
	}
}

// --to resolves among the table's members, ignoring case, and sends the
// member's UUID.
func TestCreateResolvesAnEmailAmongTheMembers(t *testing.T) {
	mock := newTestEnv(t)
	serveMembers(mock)
	bodies := serve(mock, http.MethodPost, grantsPath, http.StatusCreated, wireBody(t, activeGrant()))

	if _, _, err := run(t, "create", "--table-id", "42", "--to", "alice@example.COM", "--reason", "r", "--yes"); err != nil {
		t.Fatalf("create: %v", err)
	}
	assertRequests(t, mock, "GET "+membersPath, "POST "+grantsPath)
	if got := decodeBody(t, (*bodies)[0])["grantee_user_id"]; got != granteeID.String() {
		t.Errorf("grantee_user_id = %v, want %s", got, granteeID)
	}
}

func TestCreateUnknownEmailMakesNoPOST(t *testing.T) {
	mock := newTestEnv(t)
	serveMembers(mock)
	_, _, err := run(t, "create", "--table-id", "42", "--to", "nobody@example.com", "--reason", "r", "--yes")
	assertExitCode(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock, "GET "+membersPath)
	if !strings.Contains(err.Error(), "--to-id") {
		t.Errorf("error should point at --to-id, got: %v", err)
	}
}

func TestCreateNonInteractiveWithoutYesIsExitTwo(t *testing.T) {
	mock := newTestEnv(t)
	_, _, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r")
	assertExitCode(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock)
}

func TestCreateDeclinedAtThePromptGrantsNothing(t *testing.T) {
	mock := newTestEnv(t)
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()
	out, stderr, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r")
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v, want cmdutil.ErrDeclined", err)
	}
	assertRequests(t, mock)
	if out != "" {
		t.Errorf("a declined grant printed a result:\n%s", out)
	}
	if want := "Grant " + granteeID.String() + " unmasked access to table/42's personal data for 1h?"; !strings.Contains(stderr, want) {
		t.Errorf("prompt %q not in stderr:\n%s", want, stderr)
	}
}

func TestCreateServerRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
		exit   int
	}{
		{"already unmasked", http.StatusBadRequest, "This user already sees the table's personal data unmasked.", qerrors.ExitBadUsage},
		{"not an admin", http.StatusForbidden, "You don't have access to grant or revoke access to personal data.", qerrors.ExitUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			serve(mock, http.MethodPost, grantsPath, tc.status, []byte(`{"detail":{"msg":"`+tc.msg+`"}}`))
			_, _, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r", "--yes")
			assertExitCode(t, err, tc.exit)
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error does not carry the server's message: %v", err)
			}
		})
	}
}

func TestCreateJSONIsTheWireBody(t *testing.T) {
	mock := newTestEnv(t)
	body := wireBody(t, activeGrant())
	serve(mock, http.MethodPost, grantsPath, http.StatusCreated, body)
	out, _, err := run(t, "create", "--table-id", "42", "--to-id", granteeID.String(), "--reason", "r", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != string(body) {
		t.Errorf("output =\n%s\nwant\n%s", out, body)
	}
}

func TestRevokeUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":           {"7"},
		"non-numeric id":     {"abc", "--table-id", "42"},
		"zero id":            {"0", "--table-id", "42"},
		"wrong kind":         {"table/7", "--table-id", "42"},
		"unknown reason":     {"7", "--table-id", "42", "--reason", "bored"},
		"other without note": {"7", "--table-id", "42", "--reason", "other"},
		"other, blank note":  {"7", "--table-id", "42", "--reason", "other", "--note", "  "},
		"no id":              {"--table-id", "42"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newTestEnv(t)
			_, _, err := run(t, append([]string{"revoke", "--yes"}, args...)...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertRequests(t, mock)
		})
	}
}

func TestRevokeDefaultsToNoLongerNeededWithANullNote(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, revokePath, http.StatusOK,
		wireBody(t, revokedGrant(api.PiiAccessGrantRevokeReasonNoLongerNeeded, nil)))

	out, _, err := run(t, "revoke", "pii-grant/7", "--table-id", "42", "--yes")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	assertRequests(t, mock, "POST "+revokePath)
	got := decodeBody(t, (*bodies)[0])
	want := map[string]any{"reason": "no_longer_needed", "note": nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	wantOut := "pii-grant/7 revoked (grantee: alice@example.com, table/42, reason: no_longer_needed)\n" +
		"Next: qctl get pii-grants --table-id 42\n"
	if out != wantOut {
		t.Errorf("output =\n%q\nwant\n%q", out, wantOut)
	}
}

func TestRevokeSendsReasonAndNote(t *testing.T) {
	mock := newTestEnv(t)
	note := "laptop lost"
	bodies := serve(mock, http.MethodPost, revokePath, http.StatusOK,
		wireBody(t, revokedGrant(api.PiiAccessGrantRevokeReasonOther, &note)))

	out, _, err := run(t, "revoke", "7", "--table-id", "42", "--reason", "OTHER", "--note", " laptop lost ", "--yes", "-o", "name")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	want := map[string]any{"reason": "other", "note": "laptop lost"}
	if got := decodeBody(t, (*bodies)[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	if out != "pii-grant/7\n" {
		t.Errorf("-o name output = %q, want pii-grant/7", out)
	}
}

func TestRevokeServerRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
		exit   int
	}{
		{"unknown grant", http.StatusNotFound, "PII access grant 7 not found.", qerrors.ExitNotFound},
		{"already ended", http.StatusConflict, "PII access grant 7 is already revoked or expired.", qerrors.ExitBadUsage},
		{"not an admin", http.StatusForbidden, "You don't have access to grant or revoke access to personal data.", qerrors.ExitUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			serve(mock, http.MethodPost, revokePath, tc.status, []byte(`{"detail":{"msg":"`+tc.msg+`"}}`))
			_, _, err := run(t, "revoke", "7", "--table-id", "42", "--yes")
			assertExitCode(t, err, tc.exit)
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error does not carry the server's message: %v", err)
			}
		})
	}
}

func TestRevokeNonInteractiveWithoutYesIsExitTwo(t *testing.T) {
	mock := newTestEnv(t)
	_, _, err := run(t, "revoke", "7", "--table-id", "42")
	assertExitCode(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock)
}

// revoke is gated on protected contexts, as table-member remove is; create,
// like share table, is not.
func TestOnlyRevokeIsDestructive(t *testing.T) {
	root := setupTestCommand()
	for path, want := range map[string]bool{"revoke": true, "create": false} {
		cmd, _, err := root.Find([]string{"pii-grant", path})
		if err != nil {
			t.Fatalf("find %s: %v", path, err)
		}
		if got := cmdutil.IsDestructive(cmd); got != want {
			t.Errorf("IsDestructive(pii-grant %s) = %v, want %v", path, got, want)
		}
	}
}
