package get

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/slack_config"
	"github.com/spf13/cobra"
)

// NewSlackConfigCommand creates the get slack-config command.
func NewSlackConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack-config",
		Short: "Show the organization's Slack settings",
		Long: `Show the current organization's effective Slack config (-O selects another).

The first table has one row per use case: broker_file_received posts a file
that arrived by email or from an external member's upload, alert_unattended
posts a blocking alert nobody resolved within its delay. Below it are alert_unattended's delay per tier, its reminder
interval, and the per alert type changes to a tier: turned on or off, or a
delay of its own. An alert type without a row follows its tier.

Change an alert type's entry with:
  qctl slack set-alert-type <type>... --enabled=false
  qctl slack set-alert-type <type>... --delay-hours=8
  qctl slack set-alert-type <type>... --reset

Examples:
  qctl get slack-config
  qctl get slack-config -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("slack-config"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runGetSlackConfig,
	}
	return cmd
}

func runGetSlackConfig(cmd *cobra.Command, _ []string) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	config, err := slack_config.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		Get(ctx.Credential.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to get the Slack config: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteRaw(cmd.OutOrStdout(), outputFormat, config)
	default:
		printContextBanner(cmd, ctx)
		return printSlackConfig(cmd.OutOrStdout(), config)
	}
}

// printSlackConfig writes the table view of config.
func printSlackConfig(out io.Writer, config *slack_config.Config) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USE CASE\tENABLED\tCHANNELS")
	fmt.Fprintf(tw, "broker_file_received\t%s\t%s\n",
		slack_config.EnabledText(&config.BrokerFileReceived.Enabled), channelsText(config.BrokerFileReceived.Channels))
	unattended := config.AlertUnattended
	fmt.Fprintf(tw, "alert_unattended\t%s\t%s\n", slack_config.EnabledText(&unattended.Enabled), channelsText(unattended.Channels))
	if err := tw.Flush(); err != nil {
		return err
	}

	tiers := make([]string, 0, len(unattended.DelayHours))
	for tier := range unattended.DelayHours {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	delays := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		hours := unattended.DelayHours[tier]
		delays = append(delays, tier+" "+slack_config.DelayText(&hours))
	}
	fmt.Fprintf(out, "\nalert_unattended delay by tier: %s; reminder every %dh\n", strings.Join(delays, ", "), unattended.ReminderHours)

	alertTypes := slack_config.SortedAlertTypes(config)
	if len(alertTypes) == 0 {
		fmt.Fprintln(out, "No alert type changes: every alert type follows its tier.")
		return nil
	}
	fmt.Fprintln(out)
	tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALERT TYPE\tENABLED\tDELAY")
	for _, alertType := range alertTypes {
		entry := unattended.AlertTypes[alertType]
		fmt.Fprintf(tw, "%s\t%s\t%s\n", alertType, slack_config.EnabledText(entry.Enabled), slack_config.DelayText(entry.DelayHours))
	}
	return tw.Flush()
}

func channelsText(channels []api.SlackDestination) string {
	if len(channels) == 0 {
		return "-"
	}
	names := make([]string, len(channels))
	for i, channel := range channels {
		names[i] = string(channel)
	}
	return strings.Join(names, ",")
}
