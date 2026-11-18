package root

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestSkillNoticeUsesStderrAndPreservesJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(agentskill.CodexHomeEnv, t.TempDir())
	t.Setenv(agentskill.ClaudeConfigDirEnv, t.TempDir())
	inst := agentskill.New(agentskill.TargetCodex)
	dir, err := inst.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("an older skill")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	side, err := json.Marshal(agentskill.Sidecar{
		SchemaVersion: agentskill.SidecarSchema, QctlVersion: "old",
		Files: map[string]string{"SKILL.md": hex.EncodeToString(sum[:])},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentskill.SidecarName), side, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		f := cmd.PersistentFlags().Lookup("output")
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	cmd.SetArgs([]string{"api-resources", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(stderr.String(), "qctl skill install codex") {
		t.Fatalf("stdout = %q; stderr = %q", out.String(), stderr.String())
	}
	out.Reset()
	stderr.Reset()
	cmd.SetArgs([]string{"api-resources", "-o", "json"})
	if err := cmd.Execute(); err != nil || stderr.Len() != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("repeat: err = %v; stderr = %q", err, stderr.String())
	}
	if err := os.RemoveAll(filepath.Join(home, ".qctl")); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	cmd.SetArgs([]string{"api-resources", "-o", "invalid"})
	if err := cmd.Execute(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage || stderr.Len() != 0 {
		t.Fatalf("notice changed usage failure: %v, %q", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".qctl")); !os.IsNotExist(err) {
		t.Fatalf("usage failure wrote a cache: %v", err)
	}
}
