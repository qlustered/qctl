package agentskill

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/pkg/privatefile"
	"github.com/qlustered/qctl/internal/prompt"
)

const noticeInterval = 24 * time.Hour

type noticeStamp struct {
	Fingerprint string    `json:"fingerprint"`
	ShownAt     time.Time `json:"shownAt"`
}

// Notify reports outdated or modified managed skills and legacy Codex copies.
func Notify(w io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	notify(w, home, time.Now(), New)
}

func notify(w io.Writer, home string, now time.Time, installer func(Target) *Installer) {
	for _, target := range Targets() {
		message := noticeMessage(installer(target))
		if message == "" {
			continue
		}
		cache := filepath.Join(home, ".qctl", "skill-notice-"+string(target)+".json")
		fingerprint := hashBytes([]byte(message))
		if noticeRecent(cache, fingerprint, now) {
			continue
		}
		if _, err := fmt.Fprintln(w, "Notice: "+message); err != nil {
			continue
		}
		saveNotice(cache, noticeStamp{Fingerprint: fingerprint, ShownAt: now})
	}
}

func noticeMessage(inst *Installer) string {
	var parts []string
	rep, err := inst.Status()
	if err == nil {
		if message := managedNotice(inst.Target, rep, ""); message != "" {
			parts = append(parts, message)
		}
	}
	if inst.Target == TargetCodex {
		old := *inst
		old.LegacyCodex = true
		legacy, legacyErr := old.Status()
		if legacyErr == nil && legacy.State != StateAbsent && legacy.Dir != rep.Dir {
			if message := managedNotice(inst.Target, legacy, " --legacy"); message != "" {
				parts = append(parts, message)
			}
			parts = append(parts, fmt.Sprintf("legacy Codex skill at %q; migrate with qctl skill install codex%s, then qctl skill uninstall codex --legacy%s (inspect edits with qctl skill status codex --legacy)", legacy.Dir, prompt.YesFlag(false), prompt.YesFlag(false)))
		}
	}
	return strings.Join(parts, "; ")
}

func managedNotice(target Target, rep Report, flags string) string {
	switch rep.State {
	case StateUpdateAvailable:
		return fmt.Sprintf("%s qctl skill at %q (installed %s) differs from this binary's bundled skill (%s); update with qctl skill install %s%s%s", target, rep.Dir, rep.InstalledVersion, rep.BundledVersion, target, flags, prompt.YesFlag(false))
	case StateModified:
		return fmt.Sprintf("%s qctl skill at %q has local changes; inspect with qctl skill status %s%s", target, rep.Dir, target, flags)
	default:
		return ""
	}
}

func noticeRecent(cache, fingerprint string, now time.Time) bool {
	raw, err := os.ReadFile(cache)
	if err != nil {
		return false
	}
	var stamp noticeStamp
	if json.Unmarshal(raw, &stamp) != nil {
		return false
	}
	return stamp.Fingerprint == fingerprint && !now.Before(stamp.ShownAt) && now.Sub(stamp.ShownAt) < noticeInterval
}

func saveNotice(cache string, stamp noticeStamp) {
	raw, err := json.Marshal(stamp)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(cache), 0o700) != nil {
		return
	}
	_ = privatefile.Write(cache, raw, "skill-notice")
}
