package submit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func TestSubmitFileCharacterLimit(t *testing.T) {
	readers := []struct {
		name string
		read func(string) (string, error)
	}{
		{"rules.py", readAndValidateRuleFile},
		{"kind.toml", readAndValidateTableKindFile},
	}
	tests := []struct {
		name   string
		text   string
		length int
	}{
		{"ASCII at limit", strings.Repeat("x", 500_000), 500_000},
		{"ASCII above limit", strings.Repeat("x", 500_001), 500_001},
		{"old byte limit", strings.Repeat("x", 512_000), 512_000},
		{"multibyte at limit", strings.Repeat("\u00e9", 500_000), 500_000},
		{"astral at limit", strings.Repeat("\U0001f600", 500_000), 500_000},
		{"combining at limit", strings.Repeat("e\u0301", 250_000), 500_000},
		{"multibyte above limit", strings.Repeat("\u00e9", 500_001), 500_001},
		{"combining above limit", strings.Repeat("e\u0301", 250_000) + "x", 500_001},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), reader.name)
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if err := os.WriteFile(path, []byte(tt.text), 0600); err != nil {
						t.Fatal(err)
					}
					got, err := reader.read(path)
					if tt.length <= 500_000 {
						if err != nil {
							t.Fatalf("file within character limit: %v", err)
						}
						if got != tt.text {
							t.Error("file content changed")
						}
						return
					}
					if err == nil {
						t.Fatal("expected rejection above 500,000 characters")
					}
					if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
						t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
					}
					want := fmt.Sprintf("file %s exceeds maximum size: %d characters (limit: 500000)", path, tt.length)
					if err.Error() != want {
						t.Errorf("error = %q, want %q", err, want)
					}
				})
			}
		})
	}
}

func TestSubmitPreviewCountsCharacters(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *cobra.Command
		args     []string
		filename string
		endpoint string
		field    string
		response any
	}{
		{"rules", setupSubmitRulesTestCommand, []string{"submit", "rules", "--yes"},
			"rules.py", "/rule-revisions/submit", "file_text",
			map[string]any{"message": "ok", "added": []any{}, "not_changed": []any{}}},
		{"check", setupSubmitRulesTestCommand, []string{"submit", "rules", "--check"},
			"rules.py", "/rule-revisions/check", "file_text",
			map[string]any{"message": "ok", "rules": []any{}}},
		{"table kinds", setupSubmitTableKindsTestCommand, []string{"submit", "table-kinds", "--yes"},
			"kind.toml", "/problem-domains/import-config", "content", importedKind("Kind", "kind")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			const text = "# \u00e9\U0001f600e\u0301\n" // Seven code points.
			calls := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+tt.endpoint, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if body[tt.field] != text {
					t.Errorf("request %s = %q, want %q", tt.field, body[tt.field], text)
				}
				testutil.RespondJSON(w, http.StatusOK, tt.response)
			})
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := tt.setup()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(append(tt.args, "-f", path))
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Errorf("requests = %d, want 1", calls)
			}
			if want := "Size         : 7 characters (limit 500000)\n"; !strings.Contains(out.String(), want) {
				t.Errorf("preview missing %q:\n%s", want, out.String())
			}
			if err := os.WriteFile(path, []byte(strings.Repeat("x", 500_001)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Execute(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("oversized file error = %v, want exit 2", err)
			}
			if calls != 1 {
				t.Errorf("oversized file reached the server: requests = %d", calls)
			}
		})
	}
}
