package agentskill

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/prompt"
)

func noticeInstallers(t *testing.T) (string, func(Target) *Installer) {
	t.Helper()
	home := t.TempDir()
	return home, func(target Target) *Installer {
		return &Installer{
			Target: target, UserHomeDir: func() (string, error) { return home, nil },
			QctlVersion: "v-test",
		}
	}
}

func TestNoticeRateLimitAndHostIndependence(t *testing.T) {
	home, factory := noticeInstallers(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	i := factory(TargetCodex)
	dir := installOlderVersion(t, i)
	before := snapshot(t, dir)
	var out bytes.Buffer
	notify(&out, home, now, factory)
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "qctl skill install codex") {
		t.Fatalf("initial notice = %q", got)
	}
	if got := snapshot(t, dir); !sameManifest(got, before) {
		t.Fatal("notice changed the installed skill")
	}
	cache := filepath.Join(home, ".qctl", "skill-notice-codex.json")
	info, err := os.Stat(cache)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode: %v, %v", info, err)
	}
	out.Reset()
	notify(&out, home, now.Add(time.Hour), factory)
	if out.Len() != 0 {
		t.Fatalf("notice repeated within a day: %q", out.String())
	}
	installOlderVersion(t, factory(TargetClaudeCode))
	notify(&out, home, now.Add(2*time.Hour), factory)
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "qctl skill install claude-code") {
		t.Fatalf("other host was suppressed: %q", got)
	}
	out.Reset()
	notify(&out, home, now.Add(noticeInterval), factory)
	if !strings.Contains(out.String(), "qctl skill install codex") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("notice did not renew after 24 hours: %q", out.String())
	}
	plan, err := i.PlanInstall(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Install(plan); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	notify(&out, home, now.Add(noticeInterval+time.Hour), factory)
	if out.Len() != 0 {
		t.Fatalf("notice survived an update: %q", out.String())
	}
}

func TestNoticeDoesNotCreateCacheForAbsentCurrentOrUnmanagedSkills(t *testing.T) {
	for _, state := range []State{StateAbsent, StateCurrent, StateUnrecognized} {
		t.Run(string(state), func(t *testing.T) {
			home, factory := noticeInstallers(t)
			i := factory(TargetCodex)
			switch state {
			case StateCurrent:
				installed(t, i)
			case StateUnrecognized:
				dir, err := i.Dir()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			notify(&out, home, time.Now(), factory)
			if out.Len() != 0 {
				t.Fatalf("unexpected notice: %q", out.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".qctl")); !os.IsNotExist(err) {
				t.Fatalf("unexpected cache directory: %v", err)
			}
		})
	}
}

func TestNoticeCacheFailuresAndClockRollback(t *testing.T) {
	for _, failure := range []string{"invalid JSON", "unwritable cache", "future timestamp"} {
		t.Run(failure, func(t *testing.T) {
			home, factory := noticeInstallers(t)
			installOlderVersion(t, factory(TargetClaudeCode))
			now := time.Now()
			cacheDir := filepath.Join(home, ".qctl")
			cache := filepath.Join(cacheDir, "skill-notice-claude-code.json")
			switch failure {
			case "invalid JSON":
				if err := os.MkdirAll(cacheDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(cache, []byte("broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unwritable cache":
				if err := os.WriteFile(cacheDir, []byte("a file blocks the directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "future timestamp":
				notify(&bytes.Buffer{}, home, now.Add(time.Hour), factory)
			}
			var out bytes.Buffer
			notify(&out, home, now, factory)
			if !strings.Contains(out.String(), "qctl skill install claude-code") {
				t.Fatalf("cache failure hid the notice: %q", out.String())
			}
		})
	}
}

func TestModifiedSkillNoticeAsksForInspection(t *testing.T) {
	home, factory := noticeInstallers(t)
	dir := installed(t, factory(TargetCodex))
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("my local edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	notify(&out, home, time.Now(), factory)
	if got := out.String(); !strings.Contains(got, "qctl skill status codex") || strings.Contains(got, "--force") || strings.Contains(got, "update with") {
		t.Fatalf("modified copy advice = %q", got)
	}
}

func TestLegacyNoticeSurvivesASeparateCurrentInstall(t *testing.T) {
	home, factory := noticeInstallers(t)
	old := factory(TargetCodex)
	old.LegacyCodex = true
	dir := installOlderVersion(t, old)
	installed(t, factory(TargetCodex))
	var out bytes.Buffer
	notify(&out, home, time.Now(), factory)
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, dir) || !strings.Contains(got, "qctl skill uninstall codex --legacy") {
		t.Fatalf("legacy copy went unnoticed: %q", got)
	}
}

func TestNoticeReadFailureDoesNotHideOtherHost(t *testing.T) {
	home, factory := noticeInstallers(t)
	installOlderVersion(t, factory(TargetClaudeCode))
	broken := func(target Target) *Installer {
		i := factory(target)
		if target == TargetCodex {
			i.UserHomeDir = func() (string, error) { return "", errors.New("unavailable home") }
		}
		return i
	}
	var out bytes.Buffer
	notify(&out, home, time.Now(), broken)
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "qctl skill install claude-code") {
		t.Fatalf("read failure affected another host: %q", got)
	}
}

func TestSkillNoticeSuggestionsConfirmationSuffix(t *testing.T) {
	_, factory := noticeInstallers(t)
	old := factory(TargetCodex)
	old.LegacyCodex = true
	installOlderVersion(t, old)
	installOlderVersion(t, factory(TargetCodex))
	for _, interactive := range []bool{false, true} {
		prompt.ResetReader()
		if interactive {
			prompt.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(prompt.ResetReader)
		suffix := " --yes"
		if interactive {
			suffix = ""
		}
		got := noticeMessage(factory(TargetCodex))
		for _, want := range []string{
			"qctl skill install codex" + suffix + ",",
			"qctl skill install codex --legacy" + suffix + ";",
			"qctl skill uninstall codex --legacy" + suffix + " (inspect",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("interactive=%t: missing %q in %s", interactive, want, got)
			}
		}
	}
}
