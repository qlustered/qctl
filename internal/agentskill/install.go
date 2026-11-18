package agentskill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	internalversion "github.com/qlustered/qctl/internal/version"
)

// Target is the agent host an install writes for. Each one has its own asset
// tree, its own home environment variable and its own content digest; the
// lifecycle around them is shared.
type Target string

const (
	// TargetCodex installs into Codex's user skill directory.
	TargetCodex Target = "codex"
	// TargetClaudeCode installs into Claude Code's personal skill directory.
	TargetClaudeCode Target = "claude-code"
)

// Targets returns the accepted targets, in the order help and completion
// should list them.
func Targets() []Target { return []Target{TargetCodex, TargetClaudeCode} }

// hostRoot holds the Claude Code and legacy Codex home rules.
type hostRoot struct {
	name     string
	env      string
	fallback string
}

// hostRoots is the destination rule, one entry per Target. A target missing
// from here resolves no directory at all rather than defaulting to a host the
// caller did not ask for.
var hostRoots = map[Target]hostRoot{
	TargetCodex:      {name: "Codex", env: CodexHomeEnv, fallback: ".codex"},
	TargetClaudeCode: {name: "Claude Code", env: ClaudeConfigDirEnv, fallback: ".claude"},
}

const (
	// dirName is the skill directory's name inside the host's skills
	// directory. It is the name the host catalogs the skill under.
	dirName = "qctl"

	// CodexHomeEnv is the environment variable Codex resolves its home from.
	CodexHomeEnv = "CODEX_HOME"

	// ClaudeConfigDirEnv overrides Claude Code's config home.
	ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644

	stagePattern  = ".qctl-skill-stage-*"
	backupSuffix  = ".old"
	noSuchContent = "the directory holds no qctl install manifest"
)

// State is what the destination holds. Every state but StateAbsent means a
// directory is there; which of the four it is decides whether an install may
// write without --force.
type State string

const (
	// StateAbsent means nothing is installed at the destination.
	StateAbsent State = "absent"
	// StateCurrent means an intact qctl install of exactly the bundled files.
	StateCurrent State = "current"
	// StateUpdateAvailable means an intact qctl install of different content.
	StateUpdateAvailable State = "update-available"
	// StateModified means a qctl install whose files no longer match the
	// hashes its own manifest recorded, or that has gained a file the
	// manifest does not name.
	StateModified State = "modified"
	// StateUnrecognized means the destination exists but qctl did not install
	// it: no usable manifest, or not a directory at all.
	StateUnrecognized State = "unrecognized"
)

// Action is what applying a plan does to the filesystem.
type Action string

const (
	// ActionInstall writes a skill where none was installed.
	ActionInstall Action = "install"
	// ActionUpdate replaces an intact qctl install with the bundled files.
	ActionUpdate Action = "update"
	// ActionReplace overwrites content qctl does not own. --force only.
	ActionReplace Action = "replace"
	// ActionRemove deletes the skill directory.
	ActionRemove Action = "remove"
	// ActionNone is the idempotent no-op: reinstalling what is already
	// current, or uninstalling what is not installed.
	ActionNone Action = "none"
)

// Report is the destination's state, read without changing it.
type Report struct {
	// Dir is the resolved skill directory, printed whether or not anything
	// is installed there.
	Dir string
	// State is what Dir holds.
	State State
	// InstalledVersion is the skill version recorded by the install manifest,
	// empty unless State is one of the three managed states.
	InstalledVersion string
	// InstalledQctlVersion is the qctl build that wrote that manifest.
	InstalledQctlVersion string
	// BundledVersion is the skill version this binary carries.
	BundledVersion string
	// Findings say why a directory is modified or unrecognized, one per
	// offending path, sorted. Empty for the other three states.
	Findings []string
}

// Plan is what an install or uninstall would do, decided before anything is
// written so a caller can prompt on exactly the operations that write.
type Plan struct {
	Report
	Action Action
}

// Writes reports whether applying the plan touches the filesystem.
func (p Plan) Writes() bool { return p.Action != ActionNone }

// RefusedError is the destination holding content qctl does not own. It is a
// refusal, not a failure: nothing was read partially and no byte was changed.
type RefusedError struct {
	Dir      string
	State    State
	Verb     string // "install" or "uninstall", for the message
	Findings []string
}

func (e *RefusedError) Error() string {
	what := "was not installed by qctl"
	if e.State == StateModified {
		what = "has been modified since qctl installed it"
	}
	msg := fmt.Sprintf("refusing to %s: %s %s", e.Verb, e.Dir, what)
	for _, f := range e.Findings {
		msg += "\n  " + f
	}
	return msg + "\n\nNothing was changed. Pass --force to " + e.Verb + " anyway."
}

// Installer resolves the destination and drives the lifecycle. The two
// function fields are seams: tests supply them so no test reads the
// developer's real home directory or has to arrange a failing disk.
type Installer struct {
	// Target is the agent host. It has no default: an unset or unknown
	// target resolves no directory, so a caller that forgets to set it
	// cannot install for whichever host happens to be listed first.
	Target Target
	// HostHome overrides the Claude Code root or the legacy Codex root.
	HostHome string
	// LegacyCodex selects Codex's deprecated home-based skill directory.
	LegacyCodex bool
	// UserHomeDir resolves the fallback root. nil means os.UserHomeDir.
	UserHomeDir func() (string, error)
	// WriteFile writes one staged file. nil means os.WriteFile. A failure
	// here happens before any rename, which is what keeps a half-written
	// staging directory from ever becoming the install.
	WriteFile func(name string, data []byte, perm fs.FileMode) error
	// QctlVersion is recorded in the manifest. Empty means the build's own.
	QctlVersion string
}

// New returns an Installer for target, reading that target's home variable
// from the process environment. An unknown target yields an Installer that
// reports the target as unknown when it resolves its directory.
func New(target Target) *Installer {
	var home string
	if root, ok := hostRoots[target]; ok {
		home = os.Getenv(root.env)
	}
	return &Installer{Target: target, HostHome: home}
}

// ParseTarget resolves a command-line target name.
func ParseTarget(name string) (Target, bool) {
	for _, t := range Targets() {
		if string(t) == name {
			return t, true
		}
	}
	return "", false
}

// Dir resolves the selected host's skill directory.
func (i *Installer) Dir() (string, error) {
	root, ok := hostRoots[i.Target]
	if !ok {
		return "", unknownTarget(i.Target)
	}
	if i.Target == TargetCodex && !i.LegacyCodex {
		home, err := i.userHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine the home directory for Codex skills: %w", err)
		}
		return filepath.Join(home, ".agents", "skills", dirName), nil
	}
	home := i.HostHome
	if home == "" {
		userHome, err := i.userHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine the home directory to resolve %s from: %w", root.env, err)
		}
		home = filepath.Join(userHome, root.fallback)
	}
	return filepath.Join(home, "skills", dirName), nil
}

// unknownTarget is the one phrasing for a target no host rule or asset tree
// answers to, so the package reports it the same way wherever it is caught.
func unknownTarget(target Target) error {
	return fmt.Errorf("unknown skill target %q (accepted: %s)", target, JoinTargets())
}

func (i *Installer) userHomeDir() (string, error) {
	if i.UserHomeDir != nil {
		return i.UserHomeDir()
	}
	return os.UserHomeDir()
}

func (i *Installer) writeFile(name string, data []byte, perm fs.FileMode) error {
	if i.WriteFile != nil {
		return i.WriteFile(name, data, perm)
	}
	return os.WriteFile(name, data, perm)
}

func (i *Installer) qctlVersion() string {
	if i.QctlVersion != "" {
		return i.QctlVersion
	}
	return internalversion.Version
}

// Status reads the destination and reports what is there. It never writes, and
// it returns an error only when the filesystem or the embed cannot be read --
// every install state, including the two that refuse an install, is a Report.
func (i *Installer) Status() (Report, error) {
	dir, err := i.Dir()
	if err != nil {
		return Report{}, err
	}
	bundled, err := Hashes(i.Target)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Dir: dir, BundledVersion: versionOf(bundled)}

	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		rep.State = StateAbsent
		return rep, nil
	case err != nil:
		return Report{}, fmt.Errorf("read %s: %w", dir, err)
	case !info.IsDir():
		rep.State = StateUnrecognized
		rep.Findings = []string{dir + " is not a directory"}
		return rep, nil
	}

	side, err := readSidecar(dir)
	if err != nil {
		rep.State = StateUnrecognized
		rep.Findings = []string{unrecognizedFinding(err)}
		return rep, nil
	}
	rep.InstalledVersion = versionOf(side.Files)
	rep.InstalledQctlVersion = side.QctlVersion

	findings, err := verify(dir, side.Files)
	if err != nil {
		return Report{}, err
	}
	switch {
	case len(findings) > 0:
		rep.State = StateModified
		rep.Findings = findings
	case sameManifest(side.Files, bundled):
		rep.State = StateCurrent
	default:
		rep.State = StateUpdateAvailable
	}
	return rep, nil
}

// unrecognizedFinding phrases a sidecar read failure for a user. A missing
// file is the ordinary case -- someone else's skill directory -- and says so
// without the errno.
func unrecognizedFinding(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return noSuchContent
	}
	return err.Error()
}

// verify compares the directory against the manifest's managed-file list. A
// managed file that is missing or changed and an unmanaged file that appeared
// are both findings: the second is what stops an install or uninstall from
// silently destroying something a user put there.
func verify(dir string, managed map[string]string) ([]string, error) {
	var findings []string
	seen := map[string]bool{}

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
		rel = filepath.ToSlash(rel)
		if rel == SidecarName {
			return nil
		}
		want, ok := managed[rel]
		if !ok {
			findings = append(findings, rel+" was added after the install")
			return nil
		}
		seen[rel] = true
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if hashBytes(data) != want {
			findings = append(findings, rel+" no longer matches the installed copy")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	for rel := range managed {
		if !seen[rel] {
			findings = append(findings, rel+" is missing")
		}
	}
	sort.Strings(findings)
	return findings, nil
}

func sameManifest(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// PlanInstall decides what an install would do. It returns a *RefusedError for
// a destination qctl does not own unless force is set; every other state
// yields a plan, including the no-op.
func (i *Installer) PlanInstall(force bool) (Plan, error) {
	rep, err := i.Status()
	if err != nil {
		return Plan{}, err
	}
	switch rep.State {
	case StateAbsent:
		return Plan{Report: rep, Action: ActionInstall}, nil
	case StateCurrent:
		return Plan{Report: rep, Action: ActionNone}, nil
	case StateUpdateAvailable:
		return Plan{Report: rep, Action: ActionUpdate}, nil
	default:
		if !force {
			return Plan{}, &RefusedError{Dir: rep.Dir, State: rep.State, Verb: "install", Findings: rep.Findings}
		}
		return Plan{Report: rep, Action: ActionReplace}, nil
	}
}

// Install applies a plan from PlanInstall.
//
// Every file is written into a staging directory beside the destination and
// renamed into place in one step, so a write that fails partway leaves the
// previous installation exactly as it was and never a half-written
// SKILL.md/manifest pair. The old directory is renamed aside first and removed
// only once the new one is in place; a failure at that point puts it back.
func (i *Installer) Install(p Plan) error {
	if !p.Writes() {
		return nil
	}
	files, err := Bundled(i.Target)
	if err != nil {
		return err
	}
	hashes, err := Hashes(i.Target)
	if err != nil {
		return err
	}
	sidecar, err := marshalSidecar(hashes, i.qctlVersion())
	if err != nil {
		return err
	}

	parent := filepath.Dir(p.Dir)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	stage, err := os.MkdirTemp(parent, stagePattern)
	if err != nil {
		return fmt.Errorf("create a staging directory in %s: %w", parent, err)
	}
	// After a successful swap the staging directory is the installed one and
	// no longer exists under this name, so the cleanup is a no-op.
	defer func() { _ = os.RemoveAll(stage) }()

	if err := os.Chmod(stage, dirMode); err != nil {
		return fmt.Errorf("set the mode of %s: %w", stage, err)
	}
	for _, f := range files {
		if err := i.stage(stage, f.Path, f.Data); err != nil {
			return err
		}
	}
	if err := i.stage(stage, SidecarName, sidecar); err != nil {
		return err
	}
	return swap(stage, p.Dir)
}

// stage writes one file into the staging directory, creating the parents the
// bundled path implies.
func (i *Installer) stage(stage, rel string, data []byte) error {
	dest := filepath.Join(stage, filepath.FromSlash(rel))
	if parent := filepath.Dir(dest); parent != stage {
		if err := os.MkdirAll(parent, dirMode); err != nil {
			return fmt.Errorf("create %s: %w", parent, err)
		}
	}
	if err := i.writeFile(dest, data, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", path.Join(dirName, rel), err)
	}
	return nil
}

// swap moves the staged directory into place, keeping whatever was there until
// it has. The backup name is derived from the staging directory's, which
// os.MkdirTemp already made unique, so nothing has to be probed for.
func swap(stage, dest string) error {
	backup := stage + backupSuffix
	restore := false
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("move %s aside: %w", dest, err)
		}
		restore = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", dest, err)
	}

	if err := os.Rename(stage, dest); err != nil {
		if restore {
			_ = os.Rename(backup, dest)
		}
		return fmt.Errorf("install into %s: %w", dest, err)
	}
	if restore {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// PlanUninstall decides what an uninstall would do. An absent install is the
// no-op; a modified or unrecognized directory is refused without force.
func (i *Installer) PlanUninstall(force bool) (Plan, error) {
	rep, err := i.Status()
	if err != nil {
		return Plan{}, err
	}
	switch rep.State {
	case StateAbsent:
		return Plan{Report: rep, Action: ActionNone}, nil
	case StateCurrent, StateUpdateAvailable:
		return Plan{Report: rep, Action: ActionRemove}, nil
	default:
		if !force {
			return Plan{}, &RefusedError{Dir: rep.Dir, State: rep.State, Verb: "uninstall", Findings: rep.Findings}
		}
		return Plan{Report: rep, Action: ActionRemove}, nil
	}
}

// Uninstall applies a plan from PlanUninstall. It removes the skill directory
// and nothing else: siblings under the host's skills directory, and the skills
// directory itself, are left alone.
func (i *Installer) Uninstall(p Plan) error {
	if !p.Writes() {
		return nil
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return fmt.Errorf("remove %s: %w", p.Dir, err)
	}
	return nil
}

// Describe renders a state as the one line a human reads.
func (s State) Describe() string {
	switch s {
	case StateAbsent:
		return "not installed"
	case StateCurrent:
		return "installed, current"
	case StateUpdateAvailable:
		return "installed, update available"
	case StateModified:
		return "installed, modified"
	case StateUnrecognized:
		return "installed, unrecognized"
	default:
		return string(s)
	}
}

// TargetNames returns the accepted targets as strings, for cobra's ValidArgs
// and for an error message that has to list them.
func TargetNames() []string {
	out := make([]string, 0, len(Targets()))
	for _, t := range Targets() {
		out = append(out, string(t))
	}
	return out
}

// JoinTargets lists the accepted targets for an error message.
func JoinTargets() string { return strings.Join(TargetNames(), ", ") }

// HostName is the host's own name, for a sentence a user reads: "Codex",
// "Claude Code". It is not the target name they type -- "claude-code" reads
// wrong in prose -- and a target with no host rule answers with its own
// spelling rather than nothing.
func HostName(target Target) string {
	if root, ok := hostRoots[target]; ok {
		return root.name
	}
	return string(target)
}
