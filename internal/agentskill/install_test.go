package agentskill

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// managedFiles is the tree each target installs, written out rather than read
// back from Bundled(), so a file that quietly joins or leaves an asset tree
// fails here instead of being confirmed by the source it came from.
var managedFiles = map[Target][]string{
	TargetCodex:      {"SKILL.md", "agents/openai.yaml"},
	TargetClaudeCode: {"SKILL.md"},
}

// installedFiles is managedFiles plus qctl's sidecar: everything a successful
// install leaves in the skill directory, sorted the way walkFiles reports it.
func installedFiles(t *testing.T, target Target) []string {
	t.Helper()
	managed, ok := managedFiles[target]
	if !ok {
		t.Fatalf("no expected file list for the %q target", target)
	}
	out := append([]string{SidecarName}, managed...)
	sort.Strings(out)
	return out
}

// testInstaller uses a disposable Claude Code or legacy Codex root.
func testInstaller(t *testing.T, target Target, hostHome string) *Installer {
	t.Helper()
	return &Installer{
		Target:      target,
		HostHome:    hostHome,
		LegacyCodex: target == TargetCodex,
		UserHomeDir: func() (string, error) {
			t.Error("UserHomeDir was consulted although the host home is set")
			return "", errors.New("the real home directory is off limits in tests")
		},
		QctlVersion: "v-test",
	}
}

// eachTarget runs fn once per target, each with its own installer over its own
// disposable host home. The lifecycle is shared, so every guarantee it makes
// belongs to every host: the matrix is the default in this file, and a
// single-target test says in its name which host's contract it is about.
func eachTarget(t *testing.T, fn func(t *testing.T, i *Installer)) {
	t.Helper()
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			fn(t, testInstaller(t, target, t.TempDir()))
		})
	}
}

// installed returns the destination after a successful fresh install.
func installed(t *testing.T, i *Installer) string {
	t.Helper()
	plan, err := i.PlanInstall(false)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if err := i.Install(plan); err != nil {
		t.Fatalf("Install: %v", err)
	}
	return plan.Dir
}

// the destination

func TestTheHostHomeDecidesTheDestination(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir, err := i.Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		if want := filepath.Join(i.HostHome, "skills", "qctl"); dir != want {
			t.Errorf("Dir() = %q, want %q", dir, want)
		}
	})
}

// fallbackDirs is each host's dot directory under the user's home, which is
// where an unset home variable lands. Neither is an XDG path: both hosts'
// published contracts are a dot directory in the home.
var fallbackDirs = map[Target]string{
	TargetCodex:      ".agents",
	TargetClaudeCode: ".claude",
}

// The fallback is the half a developer's machine would answer differently from
// CI, so it goes through the seam rather than through os.UserHomeDir.
func TestAnUnsetHostHomeFallsBackToTheHostsOwnDotDirectory(t *testing.T) {
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			fallback, ok := fallbackDirs[target]
			if !ok {
				t.Fatalf("no expected fallback directory for the %q target", target)
			}
			home := t.TempDir()
			i := &Installer{Target: target, HostHome: "", UserHomeDir: func() (string, error) { return home, nil }}

			dir, err := i.Dir()
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if want := filepath.Join(home, fallback, "skills", "qctl"); dir != want {
				t.Errorf("Dir() = %q, want %q", dir, want)
			}
			if strings.Contains(dir, ".config") || strings.Contains(dir, "XDG") {
				t.Errorf("Dir() = %q: the fallback must not be an XDG path", dir)
			}
		})
	}
}

// Each host reads its own variable and no other host's, and an empty value is
// unset rather than a relative root: exporting CLAUDE_CONFIG_DIR= installs
// under the home directory, not under the working directory.
func TestNewReadsEachHostsOwnHomeVariable(t *testing.T) {
	set := map[Target]string{}
	for _, target := range Targets() {
		root, ok := hostRoots[target]
		if !ok {
			t.Fatalf("the %q target has no host rule, so it reads no variable", target)
		}
		set[target] = t.TempDir()
		t.Setenv(root.env, set[target])
	}
	for _, target := range Targets() {
		if got := New(target).HostHome; got != set[target] {
			t.Errorf("New(%s).HostHome = %q, want %q -- it read another host's variable", target, got, set[target])
		}
	}

	home := t.TempDir()
	for _, target := range Targets() {
		t.Run("empty "+hostRoots[target].env, func(t *testing.T) {
			t.Setenv(hostRoots[target].env, "")
			i := New(target)
			if i.HostHome != "" {
				t.Fatalf("HostHome = %q for an empty variable, want it treated as unset", i.HostHome)
			}
			i.UserHomeDir = func() (string, error) { return home, nil }
			dir, err := i.Dir()
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if want := filepath.Join(home, fallbackDirs[target], "skills", "qctl"); dir != want {
				t.Errorf("Dir() = %q, want %q", dir, want)
			}
		})
	}
}

func TestUnresolvableHomeIsAnError(t *testing.T) {
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			i := &Installer{Target: target, UserHomeDir: func() (string, error) { return "", errors.New("no home") }}
			if _, err := i.Dir(); err == nil {
				t.Fatal("Dir() succeeded with an unresolvable home")
			}
		})
	}
}

// A target no host rule answers to resolves no directory at all, rather than
// defaulting to whichever host happens to be listed first. The zero Target is
// the case a forgetful test literal or a new call site actually hits, and
// every entry point has to reject it the same way and name the alternatives.
func TestAnUnknownTargetResolvesNothingAndNamesTheAcceptedOnes(t *testing.T) {
	for _, target := range []Target{"", "claude", "kilroy"} {
		t.Run("target="+string(target), func(t *testing.T) {
			i := &Installer{Target: target, HostHome: t.TempDir()}
			calls := map[string]func() error{
				"Dir":     func() error { _, err := i.Dir(); return err },
				"Status":  func() error { _, err := i.Status(); return err },
				"Bundled": func() error { _, err := Bundled(target); return err },
				"Hashes":  func() error { _, err := Hashes(target); return err },
				"Version": func() error { _, err := Version(target); return err },
			}
			for name, call := range calls {
				err := call()
				if err == nil {
					t.Errorf("%s answered for the %q target", name, target)
					continue
				}
				for _, accepted := range TargetNames() {
					if !strings.Contains(err.Error(), accepted) {
						t.Errorf("%s: %q does not name the %q target", name, err, accepted)
					}
				}
			}
		})
	}
}

// the asset trees

// Adding a Target constant and forgetting its assets is what this catches: a
// target with no tree installs nothing, and one with no host rule resolves
// nowhere. The two sets are compared in both directions, so an asset tree with
// no constant behind it fails here too.
func TestEveryTargetHasAnAssetTreeAndAHostRule(t *testing.T) {
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		t.Fatalf("read the embedded assets: %v", err)
	}
	var trees []string
	for _, e := range entries {
		if e.IsDir() {
			trees = append(trees, e.Name())
		}
	}
	sort.Strings(trees)
	want := append([]string(nil), TargetNames()...)
	sort.Strings(want)
	if !equal(trees, want) {
		t.Errorf("assets/ holds %v, want one tree per target: %v", trees, want)
	}

	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			if _, ok := hostRoots[target]; !ok {
				t.Errorf("the %q target has no host rule, so Dir() resolves nothing for it", target)
			}
			files, err := Bundled(target)
			if err != nil {
				t.Fatalf("Bundled: %v", err)
			}
			var got []string
			for _, f := range files {
				got = append(got, f.Path)
				if len(f.Data) == 0 {
					t.Errorf("%s is empty", f.Path)
				}
			}
			if !contains(got, "SKILL.md") {
				t.Errorf("the tree carries %v and no SKILL.md, which is the one file every host reads", got)
			}
			if want := managedFiles[target]; !equal(got, want) {
				t.Errorf("the tree carries %v, want %v", got, want)
			}
		})
	}
}

// Claude Code has no analog of Codex's agents/openai.yaml: implicit invocation
// is its default and has no config file at all, so that file must not follow
// the policy across. One managed file plus qctl's sidecar is the whole install.
func TestTheClaudeCodeTreeIsOneFileAndCarriesNoAgentPolicy(t *testing.T) {
	files, err := Bundled(TargetClaudeCode)
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if !equal(paths, []string{"SKILL.md"}) {
		t.Fatalf("the Claude Code tree carries %v, want exactly [SKILL.md]", paths)
	}

	i := testInstaller(t, TargetClaudeCode, t.TempDir())
	dir := installed(t, i)
	if got, want := walkFiles(t, dir), installedFiles(t, TargetClaudeCode); !equal(got, want) {
		t.Errorf("the install wrote %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the install wrote an agents directory Claude Code never reads (err = %v)", err)
	}
}

// Every installed copy compares itself against these figures, so they are the
// assertion. A byte rewritten while moving or reformatting an asset fails no
// other test in this package -- it silently turns every existing install of
// that host into "update available" -- and changing an asset on purpose means
// changing the pin here in the same commit.
func TestEachTargetsBundledVersionIsPinned(t *testing.T) {
	pins := map[Target]string{
		TargetCodex:      "8cf9672bc031",
		TargetClaudeCode: "6cca87c642d1",
	}
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			pinned, ok := pins[target]
			if !ok {
				t.Fatalf("the %q target has no pinned digest; add one so a rewritten byte cannot pass unnoticed", target)
			}
			got, err := Version(target)
			if err != nil {
				t.Fatalf("Version: %v", err)
			}
			if got != pinned {
				t.Errorf("Version(%s) = %q, want %q: an asset changed, so update the pin if that was intended", target, got, pinned)
			}
		})
	}
}

// Each host's digest covers that host's tree and nothing else, so a
// Codex-only edit leaves every Claude Code install current. A test cannot edit
// the embed, so the independence is asserted from the other side: each
// target's figure is recomputed from the files under assets/<target>/qctl
// alone, walking the embed directly rather than through Bundled(). A digest
// taken over more than one tree -- the failure this guards -- matches neither.
func TestEachTargetsDigestCoversItsOwnTreeAlone(t *testing.T) {
	everything := map[string]string{}
	figures := map[Target]string{}
	for _, target := range Targets() {
		own := manifestUnder(t, assetRoot(target))
		got, err := Version(target)
		if err != nil {
			t.Fatalf("Version(%s): %v", target, err)
		}
		figures[target] = got
		if want := versionOf(own); got != want {
			t.Errorf("Version(%s) = %q, want %q: the digest covers files outside %s",
				target, got, want, assetRoot(target))
		}
		for p, h := range own {
			everything[string(target)+"/"+p] = h
		}
	}

	// The control: widening the manifest moves the figure, so the assertions
	// above are not passing against a digest that ignores what it is fed.
	whole := versionOf(everything)
	for target, got := range figures {
		if got == whole {
			t.Errorf("Version(%s) = %q, which is also the digest of every target's files together", target, got)
		}
	}
	// Hosts whose trees differ answer with different figures. Hosts shipping
	// identical bytes legitimately would not, so the trees decide which pairs
	// this applies to rather than the assumption that every host differs.
	for i, a := range Targets() {
		for _, b := range Targets()[i+1:] {
			if sameManifest(manifestUnder(t, assetRoot(a)), manifestUnder(t, assetRoot(b))) {
				continue
			}
			if figures[a] == figures[b] {
				t.Errorf("%s and %s carry different trees but both digest to %q", a, b, figures[a])
			}
		}
	}
}

// the skill content

// Skill frontmatter supplies the host's routing description.
func TestSkillFrontmatter(t *testing.T) {
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			front := frontmatter(t, target)

			if got := front["name"]; got != "qctl" {
				t.Errorf("name = %q, want qctl", got)
			}
			desc, _ := front["description"].(string)
			for _, term := range []string{"qctl", "Qluster", "what qctl supports", "Python rule files", "qluster_sdk", "RuleMetadata", "column_concepts"} {
				if !strings.Contains(desc, term) {
					t.Errorf("description does not mention %q, so the skill will not route on it: %q", term, desc)
				}
			}
			if len([]rune(desc)) > 1024 {
				t.Error("description exceeds the host's 1024-character routing limit")
			}
			if len(skillBody(t, target)) > 7*1024 {
				t.Error("SKILL.md body exceeds the 7 KiB operating-policy budget")
			}
			for key := range front {
				if key != "name" && key != "description" {
					t.Errorf("frontmatter carries %q; qctl sets name and description and nothing else", key)
				}
			}
		})
	}
}

// Claude Code invokes a skill by itself, and offers it as /qctl, unless the
// frontmatter opts out: disable-model-invocation turns the first off and
// user-invocable: false the second. qctl's policy is that both stay on, and
// that policy is expressed by the two keys' ABSENCE -- nothing else in the
// file records it -- so it is asserted rather than left to a comment.
func TestNoFrontmatterKeyTurnsOffInvocation(t *testing.T) {
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			if got := invocationKeysIn(frontmatterText(t, target)); len(got) > 0 {
				t.Errorf("the frontmatter names %v; their absence is what leaves implicit invocation and /qctl on", got)
			}
		})
	}

	// The control: the same predicate over a block that does carry both keys,
	// so a green result above means they are absent rather than that the
	// check cannot see them.
	block := "name: qctl\nDisable_Model_Invocation: false\nUser-Invocable: false\n"
	if got := invocationKeysIn(block); len(got) != 2 {
		t.Errorf("the check found %v in a block naming both keys, so it could not fail for the real ones either", got)
	}
}

// invocationKeysIn reports which invocation-disabling keys a frontmatter block
// names. It is spelling-blind on purpose: a key added under a different case
// or with underscores decodes to a different map key and would slip past a
// lookup, and the absence has to hold whatever value someone gave it. The
// check is over the block's text, so a commented-out or malformed key counts.
func invocationKeysIn(block string) []string {
	normalized := strings.ReplaceAll(strings.ToLower(block), "_", "-")
	var found []string
	for _, key := range []string{"disable-model-invocation", "user-invocable"} {
		if strings.Contains(normalized, key) {
			found = append(found, key)
		}
	}
	return found
}

// One operating policy, two files. The two SKILL.md files are byte-identical
// today, so comparing whole files would pass vacuously and go on passing after
// a host-specific frontmatter key arrives; what is compared is everything
// after the closing ---.
func TestTheOperatingPolicyIsTheSameForEveryHost(t *testing.T) {
	reference := Targets()[0]
	want := skillBody(t, reference)
	for _, target := range Targets()[1:] {
		if got := skillBody(t, target); got != want {
			t.Errorf("the %s body differs from %s's; both hosts are served one policy", target, reference)
		}
	}
}

// Codex's counterpart of Claude Code's absent frontmatter keys: the skill is
// eligible for implicit invocation, which for Codex takes a config file.
func TestOpenAIAgentPolicyAllowsImplicitInvocation(t *testing.T) {
	var doc struct {
		Interface struct {
			DisplayName      string `yaml:"display_name"`
			ShortDescription string `yaml:"short_description"`
		} `yaml:"interface"`
		Policy struct {
			AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
		} `yaml:"policy"`
	}
	if err := yaml.Unmarshal(bundledFile(t, TargetCodex, "agents/openai.yaml"), &doc); err != nil {
		t.Fatalf("decode agents/openai.yaml: %v", err)
	}
	if doc.Policy.AllowImplicitInvocation == nil || !*doc.Policy.AllowImplicitInvocation {
		t.Error("allow_implicit_invocation is not true, so the skill only loads when named")
	}
	if doc.Interface.DisplayName != "qctl" {
		t.Errorf("display_name = %q, want qctl", doc.Interface.DisplayName)
	}
	if doc.Interface.ShortDescription == "" {
		t.Error("short_description is empty")
	}
}

// The skill is an operating policy, not a second copy of the command
// reference. Every rule the ticket names has to survive a rewrite of the
// surrounding prose, so each is checked by the term that carries it.
func TestSkillBodyCarriesTheOperatingRules(t *testing.T) {
	for _, target := range Targets() {
		t.Run(string(target), func(t *testing.T) {
			body := strings.Join(strings.Fields(strings.ToLower(skillBody(t, target))), " ")
			for _, want := range []string{
				"organization",
				"403",
				"404 or exit 3 can describe an existing table's state",
				"empty list can reflect your own filters",
				"table names are exact and case-sensitive",
				"lacks a table grant",
				"not associated with this organization",
				"table_setup_in_progress",
				"report and stop for authentication or access failures",
				"missing resource id the user supplied",
				"for exit 2",
				"command line you constructed",
				"re-run once",
				"if the correction is unclear or the retry fails, report it and stop",
				"`--yes` only for the change the user asked for",
				"api-resources",
				"omit `spec.severity` unless the user or an existing manifest explicitly sets it",
				"preserve an explicit severity; never rewrite severities in bulk",
				"`true` becomes `as_declared`, and `false` becomes `warning`",
				"tablerule",
				"authoritative",
			} {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("SKILL.md no longer mentions %q", want)
				}
			}
			last := -1
			for _, step := range []string{
				"1. `qctl <command> --help`",
				"2. `qctl guide`",
				"3. `qctl explain <kind>.<field>`",
				"4. a whole `qctl explain <resource>`",
			} {
				index := strings.Index(body, step)
				if index <= last {
					t.Errorf("SKILL.md escalation step %q is missing or out of order", step)
				}
				last = index
			}
		})
	}
}

// installing

func TestInstallWritesTheBundledFilesByteForByte(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		files, err := Bundled(i.Target)
		if err != nil {
			t.Fatalf("Bundled: %v", err)
		}
		if len(files) == 0 {
			t.Fatal("the bundle carries no files")
		}
		for _, f := range files {
			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
			if err != nil {
				t.Fatalf("read %s: %v", f.Path, err)
			}
			if string(got) != string(f.Data) {
				t.Errorf("%s on disk differs from the embedded copy", f.Path)
			}
		}
	})
}

// A fresh install writes the skill directory and nothing else: no sibling, no
// stray staging directory left behind in the skills directory.
func TestInstallCreatesOnlyTheIntendedTree(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		skills := filepath.Dir(dir)
		entries, err := os.ReadDir(skills)
		if err != nil {
			t.Fatalf("read %s: %v", skills, err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) != 1 || names[0] != "qctl" {
			t.Errorf("%s holds %v, want exactly [qctl]", skills, names)
		}

		if got, want := walkFiles(t, dir), installedFiles(t, i.Target); !equal(got, want) {
			t.Errorf("installed files = %v, want %v", got, want)
		}
	})
}

func TestInstallRecordsTheBundledHashes(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		raw, err := os.ReadFile(filepath.Join(dir, SidecarName))
		if err != nil {
			t.Fatalf("read the sidecar: %v", err)
		}
		var side Sidecar
		if err := json.Unmarshal(raw, &side); err != nil {
			t.Fatalf("decode the sidecar: %v", err)
		}
		if side.SchemaVersion != SidecarSchema {
			t.Errorf("schemaVersion = %d, want %d", side.SchemaVersion, SidecarSchema)
		}
		if side.QctlVersion != "v-test" {
			t.Errorf("qctlVersion = %q, want the installing binary's version", side.QctlVersion)
		}
		bundled, err := Hashes(i.Target)
		if err != nil {
			t.Fatalf("Hashes: %v", err)
		}
		if !sameManifest(side.Files, bundled) {
			t.Errorf("recorded hashes %v, want %v", side.Files, bundled)
		}
		version, err := Version(i.Target)
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if side.SkillVersion != version {
			t.Errorf("skillVersion = %q, want %q", side.SkillVersion, version)
		}
	})
}

func TestReinstallingTheSameVersionIsANoOp(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)
		before := snapshot(t, dir)

		plan, err := i.PlanInstall(false)
		if err != nil {
			t.Fatalf("PlanInstall: %v", err)
		}
		if plan.State != StateCurrent {
			t.Errorf("state = %q, want %q", plan.State, StateCurrent)
		}
		if plan.Writes() {
			t.Errorf("action = %q, want %q", plan.Action, ActionNone)
		}
		if err := i.Install(plan); err != nil {
			t.Fatalf("Install: %v", err)
		}
		if got := snapshot(t, dir); !sameManifest(got, before) {
			t.Errorf("a no-op install rewrote the directory: %v -> %v", before, got)
		}
	})
}

// An install from an older binary is intact but different. It updates without
// --force, and its sidecar is refreshed to the bundled hashes.
func TestAnIntactOlderInstallUpdates(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installOlderVersion(t, i)

		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateUpdateAvailable {
			t.Fatalf("state = %q, want %q", rep.State, StateUpdateAvailable)
		}

		plan, err := i.PlanInstall(false)
		if err != nil {
			t.Fatalf("PlanInstall: %v", err)
		}
		if plan.Action != ActionUpdate {
			t.Errorf("action = %q, want %q", plan.Action, ActionUpdate)
		}
		if err := i.Install(plan); err != nil {
			t.Fatalf("Install: %v", err)
		}

		after, err := i.Status()
		if err != nil {
			t.Fatalf("Status after the update: %v", err)
		}
		if after.State != StateCurrent {
			t.Errorf("state after the update = %q, want %q", after.State, StateCurrent)
		}
		if after.InstalledVersion != after.BundledVersion {
			t.Errorf("the sidecar still records %q, want %q", after.InstalledVersion, after.BundledVersion)
		}
		// The retired file of the older version must be gone: an update
		// replaces the directory rather than merging into it.
		if _, err := os.Stat(filepath.Join(dir, "retired.md")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("retired.md survived the update (err = %v)", err)
		}
	})
}

func TestAModifiedInstallIsRefusedAndLeftAlone(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		skill := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(skill, []byte("# mine now\n"), 0o644); err != nil {
			t.Fatalf("edit SKILL.md: %v", err)
		}
		before := snapshot(t, dir)

		_, err := i.PlanInstall(false)
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("PlanInstall err = %v, want a *RefusedError", err)
		}
		if refused.State != StateModified {
			t.Errorf("refused state = %q, want %q", refused.State, StateModified)
		}
		if got := snapshot(t, dir); !sameManifest(got, before) {
			t.Errorf("the refusal changed the directory: %v -> %v", before, got)
		}
	})
}

// A file a user drops into the skill directory is not qctl's, so the directory
// stops being replaceable without --force even though every managed file still
// matches.
func TestAnAddedFileMakesTheInstallModified(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		if err := os.WriteFile(filepath.Join(dir, "NOTES.md"), []byte("mine\n"), 0o644); err != nil {
			t.Fatalf("add a file: %v", err)
		}
		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateModified {
			t.Errorf("state = %q, want %q", rep.State, StateModified)
		}
		if len(rep.Findings) != 1 || !strings.Contains(rep.Findings[0], "NOTES.md") {
			t.Errorf("findings = %v, want one naming NOTES.md", rep.Findings)
		}
	})
}

func TestAMissingManagedFileMakesTheInstallModified(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Fatalf("remove SKILL.md: %v", err)
		}
		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateModified {
			t.Errorf("state = %q, want %q", rep.State, StateModified)
		}
	})
}

func TestAnUnrecognizedDirectoryIsRefusedAndUntouched(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir, err := i.Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("someone else's\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		before := snapshot(t, dir)

		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateUnrecognized {
			t.Errorf("state = %q, want %q", rep.State, StateUnrecognized)
		}

		_, err = i.PlanInstall(false)
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("PlanInstall err = %v, want a *RefusedError", err)
		}
		if got := snapshot(t, dir); !sameManifest(got, before) {
			t.Errorf("the refusal changed the directory: %v -> %v", before, got)
		}
	})
}

// A sidecar from a schema this binary does not know is not a licence to guess
// at the directory's contents.
func TestAFutureSidecarSchemaReadsAsUnrecognized(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		raw := []byte(`{"schemaVersion":999,"files":{"SKILL.md":"x"}}` + "\n")
		if err := os.WriteFile(filepath.Join(dir, SidecarName), raw, 0o644); err != nil {
			t.Fatalf("write the sidecar: %v", err)
		}
		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateUnrecognized {
			t.Errorf("state = %q, want %q", rep.State, StateUnrecognized)
		}
	})
}

func TestForceReplacesOnlyTheQctlDirectory(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installed(t, i)

		sibling := filepath.Join(i.HostHome, "skills", "other")
		if err := os.MkdirAll(sibling, 0o755); err != nil {
			t.Fatalf("mkdir sibling: %v", err)
		}
		siblingFile := filepath.Join(sibling, "SKILL.md")
		if err := os.WriteFile(siblingFile, []byte("someone else's skill\n"), 0o644); err != nil {
			t.Fatalf("write sibling: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
			t.Fatalf("edit SKILL.md: %v", err)
		}

		plan, err := i.PlanInstall(true)
		if err != nil {
			t.Fatalf("PlanInstall(force): %v", err)
		}
		if plan.Action != ActionReplace {
			t.Errorf("action = %q, want %q", plan.Action, ActionReplace)
		}
		if err := i.Install(plan); err != nil {
			t.Fatalf("Install: %v", err)
		}

		rep, err := i.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rep.State != StateCurrent {
			t.Errorf("state after --force = %q, want %q", rep.State, StateCurrent)
		}
		got, err := os.ReadFile(siblingFile)
		if err != nil {
			t.Fatalf("the sibling skill is gone: %v", err)
		}
		if string(got) != "someone else's skill\n" {
			t.Errorf("the sibling skill was rewritten: %q", got)
		}
	})
}

// The staged write is what makes this true: the second file's write fails, and
// the destination still holds the complete previous installation.
func TestAMidWriteFailureLeavesThePriorInstallation(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		dir := installOlderVersion(t, i)
		before := snapshot(t, dir)

		i.WriteFile = failOnSecondWrite()

		plan, err := i.PlanInstall(false)
		if err != nil {
			t.Fatalf("PlanInstall: %v", err)
		}
		if err := i.Install(plan); err == nil {
			t.Fatal("Install succeeded although a staged write failed")
		}

		if got := snapshot(t, dir); !sameManifest(got, before) {
			t.Errorf("a failed install changed the directory: %v -> %v", before, got)
		}
		// And nothing half-written was left beside it.
		if extra := strayEntries(t, filepath.Dir(dir)); len(extra) > 0 {
			t.Errorf("the failed install left %v beside the destination", extra)
		}
	})
}

// The same failure with nothing installed leaves nothing installed, rather
// than a directory holding part of the managed set.
func TestAMidWriteFailureOnAFreshInstallLeavesNothing(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		i.WriteFile = failOnSecondWrite()

		plan, err := i.PlanInstall(false)
		if err != nil {
			t.Fatalf("PlanInstall: %v", err)
		}
		if err := i.Install(plan); err == nil {
			t.Fatal("Install succeeded although a staged write failed")
		}

		if _, err := os.Stat(plan.Dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a failed fresh install left %s behind (err = %v)", plan.Dir, err)
		}
		if extra := strayEntries(t, filepath.Join(i.HostHome, "skills")); len(extra) > 0 {
			t.Errorf("the failed install left %v behind", extra)
		}
	})
}

// failOnSecondWrite is a WriteFile seam that fails partway. The second staged
// write is the sidecar for a one-file host and a managed file for a larger
// one, so either way it lands after a file is already in the staging
// directory and before the swap.
func failOnSecondWrite() func(string, []byte, fs.FileMode) error {
	writes := 0
	return func(name string, data []byte, perm fs.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("disk full")
		}
		return os.WriteFile(name, data, perm)
	}
}

// reading the destination

func TestStatusDistinguishesEveryState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, i *Installer) string
		want  State
	}{
		{"absent", func(_ *testing.T, i *Installer) string {
			dir, _ := i.Dir()
			return dir
		}, StateAbsent},
		{"current", installed, StateCurrent},
		{"older", installOlderVersion, StateUpdateAvailable},
		{"modified", func(t *testing.T, i *Installer) string {
			dir := installed(t, i)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
				t.Fatalf("edit: %v", err)
			}
			return dir
		}, StateModified},
		{"unrecognized", func(t *testing.T, i *Installer) string {
			dir, _ := i.Dir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			return dir
		}, StateUnrecognized},
	}

	for _, target := range Targets() {
		bundledVersion, err := Version(target)
		if err != nil {
			t.Fatalf("Version(%s): %v", target, err)
		}
		for _, tc := range cases {
			t.Run(string(target)+"/"+tc.name, func(t *testing.T) {
				i := testInstaller(t, target, t.TempDir())
				dir := tc.setup(t, i)

				rep, err := i.Status()
				if err != nil {
					t.Fatalf("Status: %v", err)
				}
				if rep.State != tc.want {
					t.Errorf("state = %q, want %q (findings: %v)", rep.State, tc.want, rep.Findings)
				}
				if rep.Dir != dir {
					t.Errorf("Dir = %q, want %q", rep.Dir, dir)
				}
				if rep.BundledVersion != bundledVersion {
					t.Errorf("BundledVersion = %q, want %q", rep.BundledVersion, bundledVersion)
				}
				switch tc.want {
				case StateAbsent, StateUnrecognized:
					if rep.InstalledVersion != "" {
						t.Errorf("InstalledVersion = %q, want empty for %q", rep.InstalledVersion, tc.want)
					}
				default:
					if rep.InstalledVersion == "" {
						t.Errorf("InstalledVersion is empty for %q", tc.want)
					}
				}
				if (tc.want == StateModified || tc.want == StateUnrecognized) && len(rep.Findings) == 0 {
					t.Errorf("%q reported no findings; a refusal has to say why", tc.want)
				}
			})
		}
	}
}

// Status is a read. None of the five states may create the destination, which
// is what keeps `qctl skill status` safe to run anywhere.
func TestStatusNeverCreatesTheDestination(t *testing.T) {
	eachTarget(t, func(t *testing.T, i *Installer) {
		if _, err := i.Status(); err != nil {
			t.Fatalf("Status: %v", err)
		}
		if _, err := os.Stat(filepath.Join(i.HostHome, "skills")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Status created the skills directory (err = %v)", err)
		}
	})
}

// uninstalling

func TestUninstall(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, i *Installer)
	}{
		{"removes an intact install and no sibling", func(t *testing.T, i *Installer) {
			dir := installed(t, i)
			sibling := filepath.Join(i.HostHome, "skills", "other")
			if err := os.MkdirAll(sibling, 0o755); err != nil {
				t.Fatalf("mkdir sibling: %v", err)
			}

			plan, err := i.PlanUninstall(false)
			if err != nil {
				t.Fatalf("PlanUninstall: %v", err)
			}
			if plan.Action != ActionRemove {
				t.Errorf("action = %q, want %q", plan.Action, ActionRemove)
			}
			if err := i.Uninstall(plan); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s survived the uninstall (err = %v)", dir, err)
			}
			if _, err := os.Stat(sibling); err != nil {
				t.Errorf("the sibling skill was removed too: %v", err)
			}
		}},

		{"is idempotent", func(t *testing.T, i *Installer) {
			plan, err := i.PlanUninstall(false)
			if err != nil {
				t.Fatalf("PlanUninstall: %v", err)
			}
			if plan.Writes() {
				t.Errorf("action = %q, want %q for an absent install", plan.Action, ActionNone)
			}
			if err := i.Uninstall(plan); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
		}},

		{"refuses a modified install", func(t *testing.T, i *Installer) {
			dir := installed(t, i)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
				t.Fatalf("edit: %v", err)
			}
			before := snapshot(t, dir)

			_, err := i.PlanUninstall(false)
			var refused *RefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("PlanUninstall err = %v, want a *RefusedError", err)
			}
			if refused.Verb != "uninstall" {
				t.Errorf("refusal names %q, want uninstall", refused.Verb)
			}
			if got := snapshot(t, dir); !sameManifest(got, before) {
				t.Errorf("the refusal changed the directory: %v -> %v", before, got)
			}
		}},

		{"refuses an unrecognized directory", func(t *testing.T, i *Installer) {
			dir, err := i.Dir()
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if _, err := i.PlanUninstall(false); err == nil {
				t.Fatal("PlanUninstall accepted a directory qctl did not install")
			}
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("the refusal removed the directory: %v", err)
			}
		}},

		{"removes a modified install with --force", func(t *testing.T, i *Installer) {
			dir := installed(t, i)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
				t.Fatalf("edit: %v", err)
			}
			plan, err := i.PlanUninstall(true)
			if err != nil {
				t.Fatalf("PlanUninstall(force): %v", err)
			}
			if err := i.Uninstall(plan); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s survived --force (err = %v)", dir, err)
			}
		}},
	}

	for _, target := range Targets() {
		for _, tc := range cases {
			t.Run(string(target)+"/"+tc.name, func(t *testing.T) {
				tc.run(t, testInstaller(t, target, t.TempDir()))
			})
		}
	}
}

// one host at a time

// A host's whole lifecycle leaves every other host's directory alone -- not
// created, not consulted, not removed. The homes here are two dot directories
// under ONE temp tree, which is where a destination built from the wrong
// host's rule would still land somewhere plausible instead of failing.
//
// The "not consulted" half is the last step: the other host's install is
// broken into the state that refuses an install, and this host's install,
// status and uninstall all still succeed over it.
func TestOneHostsLifecycleLeavesTheOtherHostsInstallAlone(t *testing.T) {
	for _, target := range Targets() {
		for _, other := range Targets() {
			if other == target {
				continue
			}
			t.Run(string(target)+"-leaves-"+string(other), func(t *testing.T) {
				home := t.TempDir()
				forHost := func(h Target) *Installer {
					return &Installer{
						Target:      h,
						UserHomeDir: func() (string, error) { return home, nil },
						QctlVersion: "v-test",
					}
				}
				mine, theirs := forHost(target), forHost(other)

				mineDir := installed(t, mine)
				theirRoot := filepath.Join(home, fallbackDirs[other])
				if _, err := os.Stat(theirRoot); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("installing for %s created %s (err = %v)", target, theirRoot, err)
				}

				theirDir := installed(t, theirs)
				if theirDir == mineDir {
					t.Fatalf("both hosts resolved %s; the two installs are not separable", mineDir)
				}
				if got, want := walkFiles(t, mineDir), installedFiles(t, target); !equal(got, want) {
					t.Errorf("%s holds %v, want %v", target, got, want)
				}
				if got, want := walkFiles(t, theirDir), installedFiles(t, other); !equal(got, want) {
					t.Errorf("%s holds %v, want %v", other, got, want)
				}

				// Break the other host's install into the state that refuses.
				if err := os.Remove(filepath.Join(theirDir, SidecarName)); err != nil {
					t.Fatalf("remove the other host's sidecar: %v", err)
				}
				before := snapshot(t, theirDir)

				rep, err := mine.Status()
				if err != nil {
					t.Fatalf("Status: %v", err)
				}
				if rep.State != StateCurrent {
					t.Errorf("state = %q, want %q: the other host's directory changed the answer", rep.State, StateCurrent)
				}
				plan, err := mine.PlanInstall(false)
				if err != nil {
					t.Fatalf("PlanInstall: %v", err)
				}
				if plan.Writes() {
					t.Errorf("action = %q, want %q", plan.Action, ActionNone)
				}
				removal, err := mine.PlanUninstall(false)
				if err != nil {
					t.Fatalf("PlanUninstall: %v", err)
				}
				if err := mine.Uninstall(removal); err != nil {
					t.Fatalf("Uninstall: %v", err)
				}
				if _, err := os.Stat(mineDir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s survived its own uninstall (err = %v)", mineDir, err)
				}

				if got := snapshot(t, theirDir); !sameManifest(got, before) {
					t.Errorf("the %s lifecycle changed %s's directory: %v -> %v", target, other, before, got)
				}
				theirReport, err := theirs.Status()
				if err != nil {
					t.Fatalf("Status(%s): %v", other, err)
				}
				if theirReport.State != StateUnrecognized {
					t.Errorf("%s's state = %q, want %q", other, theirReport.State, StateUnrecognized)
				}
			})
		}
	}
}

// naming

// The accepted spellings, and the order help and completion list them in.
// Every other check in this file is driven off Targets() and would move with
// it if a host were dropped, so this is where the names themselves are held.
func TestTheAcceptedTargetNames(t *testing.T) {
	if got := TargetNames(); !equal(got, []string{"codex", "claude-code"}) {
		t.Errorf("TargetNames() = %v, want [codex claude-code]", got)
	}
	if got := JoinTargets(); got != "codex, claude-code" {
		t.Errorf("JoinTargets() = %q, want %q", got, "codex, claude-code")
	}
}

func TestParseTarget(t *testing.T) {
	for _, target := range Targets() {
		if got, ok := ParseTarget(string(target)); !ok || got != target {
			t.Errorf("ParseTarget(%q) = %q, %v", target, got, ok)
		}
	}
	if _, ok := ParseTarget("claude"); ok {
		t.Error(`ParseTarget("claude") was accepted; the Claude Code target is spelled claude-code`)
	}
	if _, ok := ParseTarget(""); ok {
		t.Error(`ParseTarget("") was accepted`)
	}
}

// HostName is what a sentence a user reads calls the host, which is not the
// name they type: "claude-code" reads wrong in prose. A target with no host
// rule answers with its own spelling rather than with nothing.
func TestHostNameNamesEachHostInProse(t *testing.T) {
	want := map[Target]string{
		TargetCodex:      "Codex",
		TargetClaudeCode: "Claude Code",
	}
	for _, target := range Targets() {
		got := HostName(target)
		expected, ok := want[target]
		switch {
		case !ok:
			t.Errorf("the %q target has no expected host name; add one so a new host is not announced by its target name", target)
		case got != expected:
			t.Errorf("HostName(%s) = %q, want %q", target, got, expected)
		}
	}
	if got := HostName("kilroy"); got != "kilroy" {
		t.Errorf(`HostName("kilroy") = %q, want the target's own spelling`, got)
	}
}

// helpers

func bundledFile(t *testing.T, target Target, rel string) []byte {
	t.Helper()
	files, err := Bundled(target)
	if err != nil {
		t.Fatalf("Bundled(%s): %v", target, err)
	}
	for _, f := range files {
		if f.Path == rel {
			return f.Data
		}
	}
	t.Fatalf("the %s bundle carries no %s", target, rel)
	return nil
}

// frontmatterText is a target's SKILL.md leading --- block, as written.
func frontmatterText(t *testing.T, target Target) string {
	t.Helper()
	start, closing, raw := frontmatterBounds(t, target)
	return raw[start:closing]
}

// skillBody is everything after a target's closing frontmatter ---: the
// operating policy itself, which is the half that must not differ per host.
func skillBody(t *testing.T, target Target) string {
	t.Helper()
	_, closing, raw := frontmatterBounds(t, target)
	body := raw[closing+len("\n---\n"):]
	if strings.TrimSpace(body) == "" {
		t.Fatalf("%s's SKILL.md has no body after its frontmatter", target)
	}
	// The split has to have actually happened: a body that still carried the
	// frontmatter would make the parity comparison a whole-file one, which
	// the two identical files pass whatever their frontmatter says.
	if strings.Contains(body, "description:") {
		t.Fatalf("%s's body still carries the frontmatter", target)
	}
	return body
}

// frontmatterBounds locates the leading --- block, returning the offsets of
// its first byte and of its closing delimiter along with the whole file.
func frontmatterBounds(t *testing.T, target Target) (int, int, string) {
	t.Helper()
	const open = "---\n"
	raw := string(bundledFile(t, target, "SKILL.md"))
	if !strings.HasPrefix(raw, open) {
		t.Fatalf("%s's SKILL.md does not open with a frontmatter block", target)
	}
	end := strings.Index(raw[len(open):], "\n---\n")
	if end < 0 {
		t.Fatalf("%s's SKILL.md frontmatter block is not closed", target)
	}
	return len(open), len(open) + end, raw
}

// frontmatter decodes a target's SKILL.md leading --- block.
func frontmatter(t *testing.T, target Target) map[string]any {
	t.Helper()
	var front map[string]any
	if err := yaml.Unmarshal([]byte(frontmatterText(t, target)), &front); err != nil {
		t.Fatalf("decode the frontmatter: %v", err)
	}
	return front
}

// manifestUnder is the path-to-hash manifest of one directory in the embed,
// derived without Bundled() so that the two derivations are able to disagree.
func manifestUnder(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(assets, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, root+"/")] = hashBytes(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no files", root)
	}
	return out
}

// installOlderVersion writes an install that a newer binary must see as intact
// but out of date: content qctl did not bundle, with a sidecar that matches it.
func installOlderVersion(t *testing.T, i *Installer) string {
	t.Helper()
	dir, err := i.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string][]byte{
		"SKILL.md":   []byte("---\nname: qctl\ndescription: an older qctl skill\n---\n"),
		"retired.md": []byte("a file the current bundle no longer carries\n"),
	}
	hashes := map[string]string{}
	for rel, data := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		hashes[rel] = hashBytes(data)
	}
	side, err := marshalSidecar(hashes, "v-older")
	if err != nil {
		t.Fatalf("marshalSidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SidecarName), side, 0o644); err != nil {
		t.Fatalf("write the sidecar: %v", err)
	}
	return dir
}

// snapshot is the directory's contents as a path-to-hash map, so a test can
// assert nothing changed without caring about the order it walked.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = hashBytes(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func walkFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for rel := range snapshot(t, dir) {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// strayEntries lists everything in dir other than the installed skill, which
// is what a leaked staging or backup directory would show up as.
func strayEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() != "qctl" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
