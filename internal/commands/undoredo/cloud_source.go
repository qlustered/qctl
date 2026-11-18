package undoredo

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/history"
	"github.com/spf13/cobra"
)

// newCloudSourceCommand builds `qctl undo cloud-source` / `qctl redo cloud-source`.
//
// A numeric id only. Cloud sources have no name resolution anywhere in qctl
// today, and adding it here would be the wrong place to start.
func newCloudSourceCommand(dir history.Direction) *cobra.Command {
	word := string(dir)
	cmd := &cobra.Command{
		Use:   "cloud-source <id>",
		Short: shortPhrase(dir, "one cloud source"),
		Long: `Walk your eligible change history for one cloud source ` + directionPhrase(dir) + `.

` + callerStackNotice(dir) + `

Ids come from 'qctl get cloud-sources'.

Examples:
  qctl ` + word + ` cloud-source 4
  qctl ` + word + ` cloud-source 4 --yes`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("history"), "table", "json", "yaml"), "cloud-source"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := cmdutil.ParseIntArgs(args, "cloud source id")
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			return runFlow(cmd, ctx, dir, history.CloudSourceTarget(ids[0]))
		},
	}
	addFlags(cmd)
	return cmd
}
