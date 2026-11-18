package agentskill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SidecarName is the install manifest qctl writes beside the skill files.
//
// It is qctl's, not the host's: the leading dot keeps it out of the skill's
// own surface, and nothing in it is addressed to an agent. No host reads it.
const SidecarName = ".qctl-skill.json"

// SidecarSchema is the sidecar's own format version, independent of the skill
// content's. A sidecar from a schema this binary does not know is treated as
// unrecognized rather than guessed at, so an older qctl never edits a newer
// one's install in place.
const SidecarSchema = 1

// Sidecar records what an install wrote, so a later binary can tell an intact
// older copy from one a user edited without carrying every historical version
// of the skill. It describes a managed file set and nothing host-specific, so
// one format serves every target.
//
// Files is the complete managed-file list. Ownership stops there: a file under
// the skill directory that Files does not name is not qctl's, and is why an
// install or uninstall that would destroy it refuses without --force.
type Sidecar struct {
	SchemaVersion int               `json:"schemaVersion"`
	SkillVersion  string            `json:"skillVersion"`
	QctlVersion   string            `json:"qctlVersion"`
	Files         map[string]string `json:"files"`
}

// readSidecar loads dir's install manifest. A missing, unreadable, malformed,
// empty or future-schema sidecar all return an error: every one of them means
// this binary cannot claim the directory, which the caller reports as
// "unrecognized".
func readSidecar(dir string) (*Sidecar, error) {
	raw, err := os.ReadFile(filepath.Join(dir, SidecarName))
	if err != nil {
		return nil, err
	}
	var s Sidecar
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s is not readable as a qctl install manifest: %w", SidecarName, err)
	}
	if s.SchemaVersion != SidecarSchema {
		return nil, fmt.Errorf("%s declares schema version %d, and this qctl writes %d",
			SidecarName, s.SchemaVersion, SidecarSchema)
	}
	if len(s.Files) == 0 {
		return nil, fmt.Errorf("%s names no managed files", SidecarName)
	}
	return &s, nil
}

// marshalSidecar renders the manifest an install writes for the bundled files.
func marshalSidecar(hashes map[string]string, qctlVersion string) ([]byte, error) {
	s := Sidecar{
		SchemaVersion: SidecarSchema,
		SkillVersion:  versionOf(hashes),
		QctlVersion:   qctlVersion,
		Files:         hashes,
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", SidecarName, err)
	}
	return append(raw, '\n'), nil
}
