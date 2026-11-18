package delete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func deleteCloudSourceRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewCommand())
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd
}

func deleteCloudSourceEnv(t *testing.T, state api.DataSourceState) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3 orders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeS3, State: state,
				UnresolvedAlerts: 3,
			})
		})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// The server's name gate is the point: qctl reads the exact name from /tiny and
// sends it, so the user only ever types an id. A name with a space also proves
// the value is encoded rather than concatenated.
func TestDeleteCloudSourceSendsTheNameFromTheTinyRead(t *testing.T) {
	mock := deleteCloudSourceEnv(t, api.DataSourceStateActive)
	var gotQuery string
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/data-sources/42",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.DataSourceDeleteResponseSchema{Result: "ok"})
		})

	root := deleteCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"delete", "cloud-source", "42", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// gotQuery is written only from inside the DELETE handler, so this is also
	// the assertion that the write was sent at all: every line printed below
	// comes from `src`, read BEFORE the write, and renders identically whether
	// or not client.DeleteCloudSource was called.
	if gotQuery != "name=s3+orders" {
		t.Errorf("query = %q, want name=s3+orders", gotQuery)
	}
	// The headline names src.Name back to the user, so it is what proves the
	// /tiny read fed the output rather than the id from the command line.
	if !strings.Contains(out.String(), "Cloud source 42 (s3 orders) deleted.") {
		t.Errorf("output must name the deleted source, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "qctl undelete cloud-source 42") {
		t.Errorf("output must name the recovery attempt, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "current servers reject it with 404") {
		t.Errorf("output must explain the recovery limitation, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "3 unresolved alert") {
		t.Errorf("output must report the alerts the delete resolved, got:\n%s", out.String())
	}
}

// Non-interactive without --yes: exit 2, naming the flag, and no DELETE. No
// DELETE handler is registered, so a write fails the test.
func TestDeleteCloudSourceNonInteractiveWithoutYesExitsTwo(t *testing.T) {
	mock := deleteCloudSourceEnv(t, api.DataSourceStateActive)

	root := deleteCloudSourceRoot()
	root.SetArgs([]string{"delete", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal with no TTY and no --yes")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal must name --yes, got: %v", err)
	}
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "DELETE ") {
			t.Fatalf("nothing may be deleted without confirmation; requests = %v", mock.Requests())
		}
	}
}

// Declining at the prompt is a choice, not a usage error: exit 1.
func TestDeleteCloudSourceDeclinedAtThePromptExitsOne(t *testing.T) {
	mock := deleteCloudSourceEnv(t, api.DataSourceStateActive)
	cmdutilSetReader(t, "n\n")

	root := deleteCloudSourceRoot()
	root.SetArgs([]string{"delete", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when the prompt is declined")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
	}
	// Exit 1 and "no DELETE" are equally true of any failure BEFORE the prompt --
	// a 500 from /tiny, a client-construction error -- so they do not by
	// themselves prove a prompt happened. "aborted" is produced only by
	// cmdutil.Confirm's decline branch, which is reachable only once the prompt
	// has been shown and answered.
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("the error must come from declining the prompt, got: %v", err)
	}
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "DELETE ") {
			t.Fatalf("a declined prompt must delete nothing; requests = %v", mock.Requests())
		}
	}
}

// Deleting an already-deleted source is refused, and refused BEFORE the prompt.
//
// The DELETE route applies no state check of its own (atlas
// delete_data_source, capi/capi/views/data_source_view.py): it answers 200 and
// rewrites state = deleted, so without the guard this command would report a
// deletion that did not happen -- on a source the user cannot even see in the
// default listing.
//
// The two invocations are what make the guard's POSITION testable, and only
// the second one can see it. Under --yes, Confirm returns immediately, so a
// guard moved below it still refuses before the write and this test could not
// tell the two orders apart. At the prompt with "n" it can: the guard exits 2,
// while a guard placed after the prompt would let the decline answer first and
// exit 1. Deleting the guard outright fails both cases.
//
// The DELETE route is registered on purpose. Without it, "no DELETE arrived"
// would be true whatever the code did, and the 404 from the unregistered route
// would satisfy err != nil on its own.
func TestDeleteCloudSourceOnADeletedSourceExitsTwoWithoutWriting(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "unattended", args: []string{"delete", "cloud-source", "42", "--yes"}},
		{name: "at the prompt", args: []string{"delete", "cloud-source", "42"}, input: "n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := deleteCloudSourceEnv(t, api.DataSourceStateDeleted)
			mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/data-sources/42",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.DataSourceDeleteResponseSchema{Result: "ok"})
				})
			if tt.input != "" {
				cmdutilSetReader(t, tt.input)
			}

			root := deleteCloudSourceRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a refusal for a source that is already deleted")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			for _, r := range mock.Requests() {
				if strings.HasPrefix(r, "DELETE ") {
					t.Fatalf("a deleted source must not be deleted again; requests = %v", mock.Requests())
				}
			}
			// The success headline is unconditional once the write returns, so
			// its absence is a second, independent witness that no write ran.
			if strings.Contains(out.String(), "deleted.") {
				t.Errorf("nothing may claim a deletion happened, got:\n%s", out.String())
			}
		})
	}
}

func cmdutilSetReader(t *testing.T, input string) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader(input))
	t.Cleanup(cmdutil.ResetReader)
}
