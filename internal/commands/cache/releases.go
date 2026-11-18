package cache

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/spf13/cobra"
)

// newReleasesCommand creates the `cache releases` parent command.
func newReleasesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "releases",
		Short: "Manage the qctl releases built for your servers",
		Long: `Manage the qctl releases qctl builds and runs for each server's atlas version,
installed under ~/.qctl/versions.

qctl builds a release the first time a server needs it and keeps it there.
'gc' removes the ones nothing needs any more; a removed release is built again
the next time a server needs it. 'pin' keeps a release regardless.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newReleasesListCommand())
	cmd.AddCommand(newReleasesGCCommand())
	cmd.AddCommand(newReleasesPinCommand())
	cmd.AddCommand(newReleasesUnpinCommand())
	return cmd
}

func newReleasesListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed and pinned qctl releases",
		Long: `List the qctl releases recorded as installed under ~/.qctl/versions, and the
pinned ones, with the platform each was built for, when it was installed and
last used, whether it is pinned, and the saved contexts whose server last
required it.

A context's requirement is the release qctl selected the last time it checked
that context's server; this command contacts no server.`,
		Args:        cobra.NoArgs,
		Annotations: cmdutil.WithFormats(catalog.For("release"), "table", "json", "yaml"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, contexts, err := releasesState()
			if err != nil {
				return err
			}
			ix, err := layout.ReadIndex()
			if err != nil {
				return err
			}
			rows := releaseRows(ix, contexts)

			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				printer, err := output.NewPrinterFromCmd(cmd)
				if err != nil {
					return err
				}
				return printer.Print(rows)
			}

			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No qctl releases installed or pinned.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "VERSION\tPLATFORM\tINSTALLED\tLAST USED\tPINNED\tCONTEXTS")
			for _, r := range rows {
				platform, installed, lastUsed := "-", "not installed", "-"
				if r.Installed {
					platform = r.OS + "/" + r.Arch
					installed = relativeOr(r.InstalledAt, "unknown")
					lastUsed = relativeOr(r.LastUsed, "never")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Version, platform, installed, lastUsed,
					yesNo(r.Pinned), orDash(strings.Join(r.Contexts, ",")))
			}
			return w.Flush()
		},
	}
}

// releasesState returns the default versions layout and every saved
// context's server by context name.
func releasesState() (releases.Layout, map[string]string, error) {
	layout, err := releases.DefaultLayout()
	if err != nil {
		return releases.Layout{}, nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return releases.Layout{}, nil, fmt.Errorf("failed to load config: %w", err)
	}
	contexts := make(map[string]string, len(cfg.Contexts))
	for name, c := range cfg.Contexts {
		if c != nil {
			contexts[name] = c.Server
		}
	}
	return layout, contexts, nil
}

// releaseRow is one release as list prints it.
type releaseRow struct {
	Version     string     `json:"version"`
	Installed   bool       `json:"installed"`
	OS          string     `json:"os"`
	Arch        string     `json:"arch"`
	Commit      string     `json:"commit"`
	InstalledAt *time.Time `json:"installed_at"`
	LastUsed    *time.Time `json:"last_used"`
	Pinned      bool       `json:"pinned"`
	Contexts    []string   `json:"contexts"`
}

// releaseRows returns a row for each installed or pinned release whose tag
// is a release version, oldest version first.
func releaseRows(ix *releases.Index, contexts map[string]string) []releaseRow {
	required := ix.ContextsRequiring(contexts)
	versions := map[string]releases.Version{}
	for _, tag := range append(slices.Collect(maps.Keys(ix.Installed)), ix.Pins...) {
		if v, err := releases.ParseVersion(tag); err == nil {
			versions[tag] = v
		}
	}
	rows := make([]releaseRow, 0, len(versions))
	for tag := range versions {
		row := releaseRow{Version: tag, Pinned: ix.IsPinned(tag), Contexts: required[tag]}
		if row.Contexts == nil {
			row.Contexts = []string{}
		}
		if inst := ix.Installed[tag]; inst != nil {
			row.Installed, row.OS, row.Arch, row.Commit = true, inst.OS, inst.Arch, inst.Commit
			row.InstalledAt, row.LastUsed = timePtr(inst.InstalledAt), timePtr(inst.LastUsed)
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b releaseRow) int { return versions[a.Version].Compare(versions[b.Version]) })
	return rows
}

// timePtr returns nil for the zero time and &t otherwise.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// relativeOr returns t as a relative time, or none when t is nil.
func relativeOr(t *time.Time, none string) string {
	if t == nil {
		return none
	}
	return timeutil.FormatRelative(*t)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
