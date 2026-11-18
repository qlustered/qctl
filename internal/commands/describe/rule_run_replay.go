package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/rule_run_replays"
	"github.com/spf13/cobra"
)

// NewRuleRunReplayCommand creates the describe rule-run-replay command.
func NewRuleRunReplayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-run-replay <id>",
		Short: "Show one rule-run replay and its comparison report",
		Long: `Show one rule-run replay: what it was asked to reproduce, how far it got,
and -- once it has finished -- how the verdicts it produced compared with the
ones the original run recorded.

Start a replay with 'qctl run rule-run-replay <rule-run-id>', which prints the
id this command takes.

status.progress is derived from the carrier job's state and from nothing else.
An absent status.report does NOT mean "still running": the backend writes the
report only on the path that finishes the carrier, so a replay whose carrier
crashed also has none. When there is no report, status.report_note says which
of the two it is.

  running   the carrier is outside the dry-run watch stop states
  finished  the replay completed; status.report holds the comparison
  failed    the carrier reached a watch stop state other than finished
  unknown   there is no carrier state to read, or it is one this build does not
            classify

The stop states are the same ones that end 'get dry-run-job --watch' and
'inspect dry-run-job --follow', including blocked and partitioned. A failed
attempt may later be retried; it does not mean the replay can never finish.

status.state is always printed verbatim beside it, so the raw value is never
hidden behind qctl's reading of it.

Examples:
  qctl describe rule-run-replay 7
  qctl describe rule-run-replay 7 -o json
  qctl describe rule-run-replay 7 -o table`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-run-replay"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := cmdutil.ParseIntArgs(args, "rule run replay id")
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := rule_run_replays.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			replay, err := client.GetReplay(ctx.Credential.AccessToken, ids[0])
			if err != nil {
				return err
			}

			manifest := rule_run_replays.APIResponseToManifest(replay)

			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			switch outputFormat {
			case outputFormatJSON:
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(manifest)
			case outputFormatTable:
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifest)
			default:
				encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(manifest)
			}
		},
	}

	return cmd
}
