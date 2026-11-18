package cache

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/spf13/cobra"
)

func newReleasesPinCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pin <version>",
		Short: "Keep a qctl release through 'cache releases gc'",
		Long: `Pin a qctl release so 'qctl cache releases gc' keeps it however long it goes
unused. <version> is a release version, MAJOR.MINOR.PATCH, such as 2.135.1.

The version need not be installed: a pin records the intent to keep it, and
applies once qctl builds it. Pinning does not build a release, and does not
make qctl run it against a server that needs another one.

Pinning a pinned release changes nothing.`,
		Args:        cobra.ExactArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("release"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, err := releaseArg(args[0])
			if err != nil {
				return err
			}
			layout, err := releases.DefaultLayout()
			if err != nil {
				return err
			}
			var already, installed bool
			err = layout.UpdateIndex(func(ix *releases.Index) error {
				already, installed = ix.IsPinned(tag), ix.Installed[tag] != nil
				ix.Pin(tag)
				return nil
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case already:
				fmt.Fprintf(out, "qctl release %s is already pinned.\n", tag)
			case installed:
				fmt.Fprintf(out, "Pinned qctl release %s.\n", tag)
			default:
				fmt.Fprintf(out, "Pinned qctl release %s; it is not installed, and gc keeps it once it is.\n", tag)
			}
			return nil
		},
	}
}

func newReleasesUnpinCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unpin <version>",
		Short: "Let 'cache releases gc' remove a qctl release again",
		Long: `Unpin a qctl release, so 'qctl cache releases gc' removes it once nothing else
keeps it. <version> is a release version, MAJOR.MINOR.PATCH.

Unpinning a release that is not pinned changes nothing. Unpinning removes
nothing by itself.`,
		Args:        cobra.ExactArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("release"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, err := releaseArg(args[0])
			if err != nil {
				return err
			}
			layout, err := releases.DefaultLayout()
			if err != nil {
				return err
			}
			var was bool
			err = layout.UpdateIndex(func(ix *releases.Index) error {
				was = ix.IsPinned(tag)
				ix.Unpin(tag)
				return nil
			})
			if err != nil {
				return err
			}
			if was {
				fmt.Fprintf(cmd.OutOrStdout(), "Unpinned qctl release %s.\n", tag)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "qctl release %s is not pinned.\n", tag)
			}
			return nil
		},
	}
}

// releaseArg validates a <version> argument, exiting 2 when it is not
// MAJOR.MINOR.PATCH.
func releaseArg(arg string) (string, error) {
	v, err := releases.ParseVersion(arg)
	if err != nil {
		return "", qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	}
	return v.String(), nil
}
