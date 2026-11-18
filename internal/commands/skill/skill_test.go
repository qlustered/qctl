// Package skill_test drives the commands through the real root tree rather
// than a hand-built one. The exit codes this file asserts are produced by
// wiring that lives in root's init() -- cmdutil.TypeArgsErrors for a bad
// argument count, the FlagErrorFunc for a bad flag -- so a synthetic parent
// would report codes no user ever sees.
package skill_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/commands/root"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// refusingTransport fails the test on any HTTP request. internal/client reads
// http.DefaultTransport per request rather than capturing one, so swapping it
// covers every client the tree could build.
type refusingTransport struct{ t *testing.T }

func (r refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("the skill commands made an HTTP request to %s", req.URL)
	return nil, http.ErrUseLastResponse
}

// hostHomeEnv is the variable each target resolves its home from. Every one of
// them is pointed at a disposable directory for every invocation, so no test
// here can reach a developer's real $CODEX_HOME, $CLAUDE_CONFIG_DIR or
// ~/.claude whichever host it happens to be driving -- and a target with no
// entry fails the run rather than quietly resolving the real thing.
var hostHomeEnv = map[agentskill.Target]string{
	agentskill.TargetCodex:      agentskill.CodexHomeEnv,
	agentskill.TargetClaudeCode: agentskill.ClaudeConfigDirEnv,
}

// hosts is one disposable home directory per target.
type hosts map[agentskill.Target]string

func newHosts(t *testing.T) hosts {
	t.Helper()
	h := hosts{}
	for _, target := range agentskill.Targets() {
		h[target] = t.TempDir()
	}
	return h
}

// dir is where target's skill installs under these homes.
func (h hosts) dir(target agentskill.Target) string {
	if target == agentskill.TargetCodex {
		return filepath.Join(h[target], ".agents", "skills", "qctl")
	}
	return filepath.Join(h[target], "skills", "qctl")
}

// run executes one qctl invocation against disposable host homes and a home
// directory with no qctl config in it, and returns stdout and the error.
func run(t *testing.T, h hosts, args ...string) (string, error) {
	t.Helper()

	// No qctl config, no credentials, and nothing to reach: all three
	// commands have to work on a machine that has never run qctl.
	home := h[agentskill.TargetCodex]
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, target := range agentskill.Targets() {
		env, ok := hostHomeEnv[target]
		if !ok {
			t.Fatalf("the %q target's home variable is not stubbed here; an invocation for it would resolve the real one", target)
		}
		dir, ok := h[target]
		if !ok || dir == "" {
			t.Fatalf("no disposable home for the %q target", target)
		}
		t.Setenv(env, dir)
	}

	saved := http.DefaultTransport
	http.DefaultTransport = refusingTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = saved })

	cmd := root.GetRootCmd()
	var out bytes.Buffer
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		resetFlags(t, cmd)
	})

	err := cmd.Execute()
	resetFlags(t, cmd)

	// Offline means no config either: the group never bootstraps one, so a
	// machine that has never run qctl is left exactly as it was found.
	if config := filepath.Join(home, ".qctl"); !isAbsent(config) {
		t.Errorf("%v created %s; the skill commands need no config file", args, config)
	}
	return out.String(), err
}

func isAbsent(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// resetFlags puts the flags this file sets back, because the root tree is a
// package-level singleton shared by every test in the binary.
func resetFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	if f := cmd.PersistentFlags().Lookup("output"); f != nil {
		if err := f.Value.Set("table"); err != nil {
			t.Fatalf("reset --output: %v", err)
		}
		f.Changed = false
	}
	for _, leaf := range skillGroup(t).Commands() {
		for _, name := range []string{"yes", "force", "legacy"} {
			if f := leaf.Flags().Lookup(name); f != nil {
				if err := f.Value.Set("false"); err != nil {
					t.Fatalf("reset --%s on %s: %v", name, leaf.Name(), err)
				}
				f.Changed = false
			}
		}
	}
}

// Each host is installed, reported on and removed on its own, and nothing the
// invocation does reaches the other host's home. Which files land is the
// target's own bundle; internal/agentskill's tests pin what each host's tree
// holds.
func TestInstallStatusUninstallRoundTrip(t *testing.T) {
	for _, target := range agentskill.Targets() {
		t.Run(string(target), func(t *testing.T) {
			name := string(target)
			h := newHosts(t)
			dir := h.dir(target)

			if _, err := run(t, h, "skill", "install", name, "--yes"); err != nil {
				t.Fatalf("install: %v", err)
			}
			files, err := agentskill.Bundled(target)
			if err != nil {
				t.Fatalf("Bundled: %v", err)
			}
			for _, f := range files {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
					t.Errorf("install did not write %s: %v", f.Path, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, agentskill.SidecarName)); err != nil {
				t.Errorf("install did not write the sidecar: %v", err)
			}
			assertOnlyHostInstalled(t, h, target)

			out, err := run(t, h, "skill", "status", name, "-o", "json")
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			view := decodeStatus(t, out)
			if view.Target != name {
				t.Errorf("target = %q, want %q", view.Target, name)
			}
			if view.State != string(agentskill.StateCurrent) {
				t.Errorf("state = %q, want %q", view.State, agentskill.StateCurrent)
			}
			if view.Path != dir {
				t.Errorf("path = %q, want %q", view.Path, dir)
			}
			if view.BundledVersion == "" {
				t.Error("status reported no bundled version")
			}

			if _, err := run(t, h, "skill", "uninstall", name, "--yes"); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if !isAbsent(dir) {
				t.Errorf("uninstall left %s behind", dir)
			}
		})
	}
}

// assertOnlyHostInstalled fails if any host other than target has a skill
// directory. One verb names one host, and a destination built from the wrong
// host's rule would show up here.
func assertOnlyHostInstalled(t *testing.T, h hosts, target agentskill.Target) {
	t.Helper()
	for _, other := range agentskill.Targets() {
		if other == target {
			continue
		}
		if !isAbsent(h.dir(other)) {
			t.Errorf("a %s invocation created %s, which belongs to %s", target, h.dir(other), other)
		}
	}
}

// Both argument rejections are exit 2, and neither creates anything: the
// validator runs before any destination is resolved. A rejected name also has
// to say what would have been accepted, so a user who typed "claude" is told
// the spelling rather than left to guess.
func TestBadArgumentsExitTwoBeforeTouchingTheFilesystem(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		namesTargets bool
	}{
		{"unknown target", []string{"skill", "install", "kilroy", "--yes"}, true},
		{"a target that is nearly right", []string{"skill", "status", "claude"}, true},
		{"no target", []string{"skill", "status"}, false},
		{"two targets", []string{"skill", "uninstall", "codex", "claude-code", "--yes"}, false},
		{"unknown flag", []string{"skill", "install", "claude-code", "--nope"}, false},
		{"legacy Claude Code", []string{"skill", "install", "claude-code", "--legacy", "--yes"}, false},
		{"legacy Claude Code status", []string{"skill", "status", "claude-code", "--legacy"}, false},
		{"legacy Claude Code uninstall", []string{"skill", "uninstall", "claude-code", "--legacy", "--yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHosts(t)
			_, err := run(t, h, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted", tc.args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			for target, home := range h {
				if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
					t.Errorf("the rejection touched %s's home %s: %v (err = %v)", target, home, entries, readErr)
				}
			}
			if !tc.namesTargets {
				return
			}
			for _, name := range agentskill.TargetNames() {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the rejection never names the %q target: %v", name, err)
				}
			}
		})
	}
}

// --yes answers the prompt. It is not a licence to overwrite an edit, which is
// what --force is for, and the refusal must leave the edit intact.
func TestYesDoesNotImplyForce(t *testing.T) {
	for _, target := range agentskill.Targets() {
		t.Run(string(target), func(t *testing.T) {
			name := string(target)
			h := newHosts(t)
			if _, err := run(t, h, "skill", "install", name, "--yes"); err != nil {
				t.Fatalf("install: %v", err)
			}

			skill := filepath.Join(h.dir(target), "SKILL.md")
			mine := []byte("# mine now\n")
			if err := os.WriteFile(skill, mine, 0o644); err != nil {
				t.Fatalf("edit SKILL.md: %v", err)
			}

			if _, err := run(t, h, "skill", "install", name, "--yes"); err == nil {
				t.Fatal("--yes replaced a modified install")
			}
			if _, err := run(t, h, "skill", "uninstall", name, "--yes"); err == nil {
				t.Fatal("--yes removed a modified install")
			}
			got, err := os.ReadFile(skill)
			if err != nil {
				t.Fatalf("read SKILL.md: %v", err)
			}
			if string(got) != string(mine) {
				t.Errorf("the refused operations rewrote SKILL.md: %q", got)
			}

			if _, err := run(t, h, "skill", "install", name, "--yes", "--force"); err != nil {
				t.Fatalf("install --force: %v", err)
			}
			if got, err := os.ReadFile(skill); err != nil || string(got) == string(mine) {
				t.Errorf("--force did not replace SKILL.md (err = %v)", err)
			}
			assertOnlyHostInstalled(t, h, target)
		})
	}
}

// Every state status reports is informational: the exit code says whether the
// directory could be read, not whether an update is due. The state itself is
// read back from -o json, so a run that exits 0 while reporting the wrong
// thing fails here rather than passing as "exited 0".
func TestStatusExitsZeroForEveryInstallState(t *testing.T) {
	states := []agentskill.State{
		agentskill.StateAbsent,
		agentskill.StateCurrent,
		agentskill.StateUpdateAvailable,
		agentskill.StateModified,
		agentskill.StateUnrecognized,
	}
	for _, target := range agentskill.Targets() {
		for _, state := range states {
			t.Run(string(target)+"/"+string(state), func(t *testing.T) {
				h := newHosts(t)
				seed(t, h, target, state)

				out, err := run(t, h, "skill", "status", string(target), "-o", "json")
				if err != nil {
					t.Fatalf("status reporting %q: %v", state, err)
				}
				if got := decodeStatus(t, out).State; got != string(state) {
					t.Errorf("state = %q, want %q", got, state)
				}
			})
		}
	}
}

// seed puts target's skill directory into one of the five states. Two are
// reachable through the CLI; the other three are written by hand, because the
// CLI exists to refuse to produce them.
func seed(t *testing.T, h hosts, target agentskill.Target, state agentskill.State) {
	t.Helper()
	dir := h.dir(target)
	install := func() {
		if _, err := run(t, h, "skill", "install", string(target), "--yes"); err != nil {
			t.Fatalf("install: %v", err)
		}
	}
	switch state {
	case agentskill.StateAbsent:
	case agentskill.StateCurrent:
		install()
	case agentskill.StateUpdateAvailable:
		writeOlderInstall(t, dir)
	case agentskill.StateModified:
		install()
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
			t.Fatalf("edit SKILL.md: %v", err)
		}
	case agentskill.StateUnrecognized:
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	default:
		t.Fatalf("no way to seed the %q state", state)
	}
}

// writeOlderInstall puts an intact install of content this binary does not
// bundle at dir: the shape a copy installed by an older qctl leaves behind.
func writeOlderInstall(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := []byte("---\nname: qctl\ndescription: an older qctl skill\n---\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), body, 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	sum := sha256.Sum256(body)
	raw, err := json.Marshal(agentskill.Sidecar{
		SchemaVersion: agentskill.SidecarSchema,
		SkillVersion:  "older",
		QctlVersion:   "v-older",
		Files:         map[string]string{"SKILL.md": hex.EncodeToString(sum[:])},
	})
	if err != nil {
		t.Fatalf("render the sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentskill.SidecarName), raw, 0o644); err != nil {
		t.Fatalf("write the sidecar: %v", err)
	}
}

// statusView is this command's wire shape, spelled out here rather than
// imported, so a rename in the command's own struct shows up as a test failure
// instead of travelling silently into every script reading the JSON.
type statusView struct {
	Target         string      `json:"target"`
	Path           string      `json:"path"`
	State          string      `json:"state"`
	BundledVersion string      `json:"bundledVersion"`
	LegacyInstall  *statusView `json:"legacyInstall"`
	Legacy         bool        `json:"legacy"`
}

func TestCodexMigrationKeepsBothCopiesUntilExplicitLegacyRemoval(t *testing.T) {
	h := newHosts(t)
	legacy := filepath.Join(h[agentskill.TargetCodex], "skills", "qctl")
	writeOlderInstall(t, legacy)
	before, err := os.ReadFile(filepath.Join(legacy, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, h, "skill", "install", "codex", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(legacy, "SKILL.md")); err != nil || !bytes.Equal(got, before) {
		t.Fatalf("new install changed the legacy copy: %q, %v", got, err)
	}
	out, err := run(t, h, "skill", "status", "codex", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	view := decodeStatus(t, out)
	if view.State != "current" || view.LegacyInstall == nil || view.LegacyInstall.State != "update-available" || view.LegacyInstall.Path != legacy || !view.LegacyInstall.Legacy {
		t.Fatalf("status lost one of the copies: %s", out)
	}
	out, err = run(t, h, "skill", "status", "codex")
	if err != nil || !strings.Contains(out, "qctl skill install codex --legacy") || !strings.Contains(out, "qctl skill uninstall codex --legacy") {
		t.Fatalf("migration advice = %q, %v", out, err)
	}
	if _, err := run(t, h, "skill", "uninstall", "codex", "--legacy", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !isAbsent(legacy) || isAbsent(h.dir(agentskill.TargetCodex)) {
		t.Fatal("legacy removal affected the wrong copy")
	}
}

func TestLegacyInstallAndModifiedCopyProtection(t *testing.T) {
	h := newHosts(t)
	legacy := filepath.Join(h[agentskill.TargetCodex], "skills", "qctl")
	if _, err := run(t, h, "skill", "install", "codex", "--legacy", "--yes"); err != nil {
		t.Fatal(err)
	}
	if isAbsent(legacy) || !isAbsent(h.dir(agentskill.TargetCodex)) {
		t.Fatal("--legacy installed at the wrong location")
	}
	if err := os.WriteFile(filepath.Join(legacy, "SKILL.md"), []byte("my edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, h, "skill", "install", "codex", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, h, "skill", "uninstall", "codex", "--legacy", "--yes"); err == nil {
		t.Fatal("legacy uninstall discarded edits without --force")
	}
	if got, err := os.ReadFile(filepath.Join(legacy, "SKILL.md")); err != nil || string(got) != "my edits" {
		t.Fatalf("legacy edits changed: %q, %v", got, err)
	}
	if _, err := run(t, h, "skill", "uninstall", "codex", "--legacy", "--yes", "--force"); err != nil {
		t.Fatal(err)
	}
	if !isAbsent(legacy) || isAbsent(h.dir(agentskill.TargetCodex)) {
		t.Fatal("forced legacy removal affected the preferred copy")
	}
}

func decodeStatus(t *testing.T, out string) statusView {
	t.Helper()
	var view statusView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("status -o json is not JSON: %v\n%s", err, out)
	}
	return view
}

// Every target is named in the group's help and in all three leaves', so
// neither host reads as the default and a third one cannot be added without
// saying where it installs. The list is TargetNames() rather than a
// transcription of it, which is what makes this fire on the third host instead
// of going stale with the prose it guards.
func TestEveryTargetIsNamedInTheHelpOfEveryVerb(t *testing.T) {
	group := skillGroup(t)
	bodies := map[string]string{"skill": group.Long}
	for _, leaf := range group.Commands() {
		bodies["skill "+leaf.Name()] = leaf.Long
	}
	if len(bodies) != 4 {
		t.Fatalf("expected the group and its three verbs, got %d: %v", len(bodies), bodies)
	}
	for path, long := range bodies {
		for _, name := range agentskill.TargetNames() {
			if !strings.Contains(long, name) {
				t.Errorf("`qctl %s --help` never names the %q target:\n%s", path, name, long)
			}
		}
	}
}

// ValidArgs is what a shell completes from, and it is every target and no
// more: a host missing from it is one no shell ever suggests, and a name in it
// that Args rejects is a suggestion that exits 2 when taken.
//
// Both sides here are TargetNames(), so this catches a verb that drifts from
// the list -- not a host dropped from the list itself, which moves both sides
// together. That one is agentskill's TestTheAcceptedTargetNames.
func TestEveryVerbCompletesEveryTarget(t *testing.T) {
	want := append([]string(nil), agentskill.TargetNames()...)
	sort.Strings(want)

	leaves := skillGroup(t).Commands()
	if len(leaves) != 3 {
		t.Fatalf("the group has %d verbs, want 3", len(leaves))
	}
	for _, leaf := range leaves {
		t.Run(leaf.Name(), func(t *testing.T) {
			got := append([]string(nil), leaf.ValidArgs...)
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("ValidArgs = %v, want %v", got, want)
			}

			// And the shell actually receives them: ValidArgs is only a
			// declaration until cobra's completion command reads it.
			out, err := run(t, newHosts(t), "__complete", "skill", leaf.Name(), "")
			if err != nil {
				t.Fatalf("__complete: %v", err)
			}
			for _, name := range want {
				if !strings.Contains(out, name) {
					t.Errorf("completing `qctl skill %s` never offers %q:\n%s", leaf.Name(), name, out)
				}
			}
		})
	}
}

// skillGroup finds the group in the real tree. Reaching for it by name rather
// than calling skill.NewCommand() keeps this on the same node the user runs.
func skillGroup(t *testing.T) *cobra.Command {
	t.Helper()
	for _, node := range root.GetRootCmd().Commands() {
		if node.Name() == "skill" {
			return node
		}
	}
	t.Fatal("the root tree has no skill command")
	return nil
}

func TestStatusSuggestionsRunWithoutConfirmationInput(t *testing.T) {
	prompt.ResetReader()
	t.Cleanup(prompt.ResetReader)
	for _, state := range []agentskill.State{agentskill.StateAbsent, agentskill.StateUpdateAvailable, agentskill.StateModified, agentskill.StateUnrecognized} {
		t.Run(string(state), func(t *testing.T) {
			h := newHosts(t)
			seed(t, h, agentskill.TargetClaudeCode, state)
			out, err := run(t, h, "skill", "status", "claude-code")
			if err != nil {
				t.Fatal(err)
			}
			var command string
			for _, line := range strings.Split(out, "\n") {
				if at := strings.Index(line, "qctl skill install "); at >= 0 {
					command = line[at:]
				}
			}
			if command == "" || !strings.Contains(command, " --yes") {
				t.Fatalf("missing unattended install recipe: %s", out)
			}
			if _, err := run(t, h, strings.Fields(command)[1:]...); err != nil {
				t.Fatalf("printed recipe failed: %v", err)
			}
		})
	}
}
