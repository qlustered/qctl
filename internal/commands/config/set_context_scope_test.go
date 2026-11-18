package config

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestSetContextOrgConfirmationAndCurrentContext(t *testing.T) {
	for _, tt := range []struct {
		name     string
		input    string
		yes      bool
		answer   string
		wantExit int
	}{
		{"fuzzy noninteractive", "Glob", false, "", 2},
		{"fuzzy declined", "Glob", false, "no\n", 1},
		{"fuzzy accepted", "Glob", false, "yes\n", 0},
		{"fuzzy yes", "Glob", true, "", 0},
		{"exact", "Globex", false, "", 0},
		{"uuid prefix", "550e8400", false, "", 0},
		{"unknown", "Unknown", true, "", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			cfg := env.SetupConfigWithOrg("http://localhost:8000", "", "old-org")
			cfg.Contexts["other"] = &config.Context{Server: "http://localhost:8000", Organization: "old-org", Organizations: []config.OrganizationRef{
				{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "Globex"},
			}}
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(env.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			cmdutil.ResetReader()
			if tt.answer != "" {
				cmdutil.SetReader(strings.NewReader(tt.answer))
			}
			t.Cleanup(cmdutil.ResetReader)
			cmd := newSetContextCommand()
			args := []string{"other", "--org", tt.input}
			if tt.yes {
				args = append(args, "--yes")
			}
			cmd.SetArgs(args)
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err = cmd.Execute()
			if qerrors.GetExitCode(err) != tt.wantExit {
				t.Fatalf("error = %v, want exit %d", err, tt.wantExit)
			}
			after, readErr := os.ReadFile(env.ConfigPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err != nil {
				if !bytes.Equal(before, after) {
					t.Error("rejected input changed config")
				}
				if tt.wantExit == 2 && tt.input == "Glob" && !strings.Contains(err.Error(), "--yes") {
					t.Errorf("missing --yes: %v", err)
				}
			} else {
				saved, loadErr := config.Load()
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if saved.CurrentContext != "default" || saved.Contexts["other"].Organization != "550e8400-e29b-41d4-a716-446655440000" {
					t.Errorf("unexpected saved context: %+v", saved)
				}
				if strings.Contains(stdout.String(), "Switched") {
					t.Errorf("false switch message: %s", &stdout)
				}
			}
			if strings.Contains(stdout.String(), "Did you mean") {
				t.Error("confirmation leaked to stdout")
			}
			if tt.answer != "" && !strings.Contains(stderr.String(), "Did you mean") {
				t.Error("confirmation missing from stderr")
			}
		})
	}
}

func TestSetContextValidationExitCodes(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig("http://localhost:8000", "")
	for _, args := range [][]string{
		{}, {"other"}, {"--current", "default"}, {"other", "--server", "ftp://localhost"},
	} {
		cmd := newSetContextCommand()
		cmd.SetArgs(args)
		err := cmd.Execute()
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("args = %q, error = %v, want exit 2", args, err)
		}
	}
}
