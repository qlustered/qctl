package cache

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/spf13/cobra"
)

func newReleasesGCCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove qctl releases nothing needs any more",
		Long: `Remove the qctl releases under ~/.qctl/versions that nothing needs, and build
directories left behind by builds that no longer run. Prints KEEP or REMOVE
for each, with the reason; --dry-run prints the same decisions and removes
nothing.

A release is kept when any of these holds:
  - a saved context's server last required it (the server's last recorded
    check; this command contacts no server, so a server that is unreachable
    now keeps what it last required)
  - it is pinned ('qctl cache releases pin')
  - a qctl is running it, or building it
  - it was last used (or installed, if never used) within 30 days

Everything else is removed, including a version directory the index does not
record. A removed release is built again the next time a server needs it.
Build directories under ~/.qctl/versions/.staging are removed once they are
an hour old and their build is not running.

gc removes only release directories (~/.qctl/versions/<version>/), their index
entries, and build directories (~/.qctl/versions/.staging/<version>-*/); the
lock files and the per-server records beside them stay.
Where the platform has no file locks, gc cannot prove a release idle and keeps
it.`,
		Args:        cobra.NoArgs,
		Annotations: cmdutil.WithFormats(catalog.For("release"), "table", "json", "yaml"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			layout, contexts, err := releasesState()
			if err != nil {
				return err
			}
			entries, gcErr := layout.GC(releases.GCOptions{Contexts: contexts, DryRun: dryRun, Now: time.Now()})
			if gcErr != nil && entries == nil {
				return gcErr
			}
			if err := printGC(cmd, entries, dryRun); err != nil {
				return err
			}
			return errors.Join(gcErr, removalFailures(entries))
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print what gc would keep and remove, and remove nothing")
	return cmd
}

// gcReport is gc's output under -o json and -o yaml.
type gcReport struct {
	DryRun  bool        `json:"dry_run"`
	Entries []gcRowJSON `json:"entries"`
}

// gcRowJSON is one gc decision under -o json and -o yaml.
type gcRowJSON struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Action   string   `json:"action"`
	Removed  bool     `json:"removed"`
	Reasons  []string `json:"reasons"`
	Reason   string   `json:"reason"`
	Contexts []string `json:"contexts"`
	Error    *string  `json:"error"`
}

func printGC(cmd *cobra.Command, entries []releases.GCEntry, dryRun bool) error {
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		report := gcReport{DryRun: dryRun, Entries: make([]gcRowJSON, 0, len(entries))}
		for _, e := range entries {
			row := gcRowJSON{Kind: e.Kind, Name: e.Name, Path: e.Path, Action: "keep", Removed: e.Removed,
				Reasons: e.Reasons, Reason: gcReason(e), Contexts: e.Contexts}
			if e.Remove {
				row.Action = "remove"
			}
			if row.Contexts == nil {
				row.Contexts = []string{}
			}
			if e.Err != nil {
				msg := e.Err.Error()
				row.Error = &msg
			}
			report.Entries = append(report.Entries, row)
		}
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return err
		}
		return printer.Print(report)
	}

	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "No qctl releases or build directories to collect.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACTION\tENTRY\tREASON")
	removing := 0
	for _, e := range entries {
		action := "KEEP"
		if e.Remove {
			action = "REMOVE"
			removing++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", action, gcEntryName(e), gcReason(e))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, "\nDry run: %d to remove, %d to keep; nothing was removed.\n", removing, len(entries)-removing)
		return nil
	}
	removed := 0
	for _, e := range entries {
		if e.Removed {
			removed++
		}
	}
	fmt.Fprintf(out, "\nRemoved %d, kept %d.\n", removed, len(entries)-removing)
	return nil
}

// gcEntryName is the ENTRY cell: the tag for a release, .staging/<dir> for a
// build directory.
func gcEntryName(e releases.GCEntry) string {
	if e.Kind == releases.KindStaging {
		return ".staging/" + e.Name
	}
	return e.Name
}

// gcReason explains e's decision in words.
func gcReason(e releases.GCEntry) string {
	parts := make([]string, 0, len(e.Reasons)+1)
	for _, r := range e.Reasons {
		parts = append(parts, gcReasonPhrase(e, r))
	}
	if e.Err != nil && e.Remove {
		parts = append(parts, "removal failed: "+e.Err.Error())
	}
	return strings.Join(parts, "; ")
}

func gcReasonPhrase(e releases.GCEntry, reason string) string {
	switch reason {
	case releases.ReasonContext:
		noun := "context"
		if len(e.Contexts) > 1 {
			noun = "contexts"
		}
		return fmt.Sprintf("required by %s %s", noun, strings.Join(e.Contexts, ", "))
	case releases.ReasonPinned:
		return "pinned"
	case releases.ReasonRecent:
		return sincePhrase(e)
	case releases.ReasonInUse:
		return "in use by a running qctl"
	case releases.ReasonBuilding:
		return "a build of this release is running"
	case releases.ReasonLockUnsupported:
		return "cannot prove it is idle: this platform has no file locks"
	case releases.ReasonLockFailed:
		return fmt.Sprintf("cannot prove it is idle: %v", e.Err)
	case releases.ReasonNotDirectory:
		return "not a directory, so not a release qctl installed"
	case releases.ReasonUnused:
		phrase := sincePhrase(e)
		if phrase == "" {
			phrase = "never used"
		}
		phrase += ", not pinned or required by a saved context"
		if !e.InIndex {
			phrase += ", not in the index"
		}
		return phrase
	case releases.ReasonStale:
		return "abandoned build, " + sincePhrase(e)
	}
	return reason
}

// sincePhrase says what happened at e.Since and how long ago, such as
// "used 3 days ago".
func sincePhrase(e releases.GCEntry) string {
	if e.Since.IsZero() {
		return ""
	}
	return e.SinceBasis + " " + timeutil.FormatRelative(e.Since)
}

// removalFailures returns an error naming every entry gc failed to remove.
func removalFailures(entries []releases.GCEntry) error {
	var failed []string
	for _, e := range entries {
		if e.Remove && e.Err != nil {
			failed = append(failed, gcEntryName(e))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("failed to remove %s; see the reasons above", strings.Join(failed, ", "))
}
