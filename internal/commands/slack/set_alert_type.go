package slack

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/slack_config"
	"github.com/spf13/cobra"
)

const defaultValue = "default"

func newSetAlertTypeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-alert-type <alert-type>...",
		Short: "Change how unattended alerts of one or more types post to Slack",
		Long: `Change how the unattended alert posts of one or more alert types behave, in
the current organization (-O selects another).

Every alert type that posts belongs to a tier, file_decision or blocking,
whose delay 'qctl get slack-config' shows; a type with no tier is off. An
alert type's entry changes that:

  --enabled=false        stop posting this type
  --enabled=true         post this type, whose tier is then blocking if it
                         has none
  --delay-hours=N        post N hours after the alert is raised instead of
                         after the tier's delay
  --enabled=default, --delay-hours=default
                         clear that half of the entry
  --reset                drop the entry: the type follows its tier again

--enabled and --delay-hours can be given together; a flag left out keeps that
half of the entry as it is. The server refuses --enabled=true for a row-level
alert type, and a type that pages ops never posts here, whatever its entry.

This is READ-MODIFY-WRITE. The server's write replaces the whole config, so
this command reads it, changes the named entries, and writes all of it back.
The last writer wins: a change made elsewhere between the read and the write
is overwritten. When no named entry changes, nothing is written.

Examples:
  qctl slack set-alert-type file_already_ingested --enabled=false
  qctl slack set-alert-type preload_check_held inbound_email_held --delay-hours=1
  qctl slack set-alert-type file_already_ingested --reset

-o json|yaml prints the resulting config and moves the prose to stderr.`,
		Annotations:       cmdutil.WithFormats(catalog.For("slack-config"), "table", "json", "yaml"),
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeAlertType,
		RunE:              runSetAlertType,
	}
	cmd.Flags().String("enabled", "", "true to post this type, false to stop, default to follow its tier")
	cmd.Flags().String("delay-hours", "", "hours to wait before posting (0 or more), or default for the tier's delay")
	cmd.Flags().Bool("reset", false, "drop the entry, so the type follows its tier again")
	return cmd
}

func runSetAlertType(cmd *cobra.Command, args []string) error {
	change, err := changeFromFlags(cmd)
	if err != nil {
		return err
	}
	alertTypes := uniqueInOrder(args)

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := slack_config.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	current, err := client.Get(token)
	if err != nil {
		return fmt.Errorf("failed to read the Slack config: %w", err)
	}
	result := current
	if updated, changed := slack_config.WithAlertTypes(*current, alertTypes, change); changed {
		result, err = client.Put(token, updated)
		if err != nil {
			return fmt.Errorf("failed to update the Slack config: %w", err)
		}
	}

	w := cmdutil.NewResultWriter(cmd)
	for _, alertType := range alertTypes {
		fmt.Fprintln(w.Human, alertTypeLine(alertType,
			slack_config.Entry(current, alertType), slack_config.Entry(result, alertType)))
	}
	return w.Write(result)
}

// changeFromFlags parses --enabled, --delay-hours and --reset; any misuse
// exits 2.
func changeFromFlags(cmd *cobra.Command) (slack_config.Change, error) {
	var change slack_config.Change
	flags := cmd.Flags()
	change.Reset, _ = flags.GetBool("reset")
	if change.Reset {
		if flags.Changed("enabled") || flags.Changed("delay-hours") {
			return change, qerrors.NewBadUsage("--reset cannot be combined with --enabled or --delay-hours")
		}
		return change, nil
	}
	if !flags.Changed("enabled") && !flags.Changed("delay-hours") {
		return change, qerrors.NewBadUsage("give --enabled, --delay-hours or --reset")
	}
	if flags.Changed("enabled") {
		value, _ := flags.GetString("enabled")
		switch value {
		case "true", "false":
			change.Enabled = &slack_config.FieldValue[bool]{Value: value == "true"}
		case defaultValue:
			change.Enabled = &slack_config.FieldValue[bool]{Default: true}
		default:
			return change, qerrors.NewBadUsagef("--enabled must be true, false or default, not %q", value)
		}
	}
	if flags.Changed("delay-hours") {
		value, _ := flags.GetString("delay-hours")
		if value == defaultValue {
			change.DelayHours = &slack_config.FieldValue[int]{Default: true}
		} else {
			hours, err := strconv.Atoi(value)
			if err != nil || hours < 0 {
				return change, qerrors.NewBadUsagef("--delay-hours must be a whole number of hours, 0 or more, or default, not %q", value)
			}
			change.DelayHours = &slack_config.FieldValue[int]{Value: hours}
		}
	}
	return change, nil
}

func uniqueInOrder(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// alertTypeLine is the one line set-alert-type prints per named alert type.
func alertTypeLine(alertType string, before, after *slack_config.AlertTypeEntry) string {
	change := "unchanged"
	if was := slack_config.Describe(before); was != slack_config.Describe(after) {
		change = "was " + was
	}
	return fmt.Sprintf("%s: %s (%s)", alertType, slack_config.Describe(after), change)
}

func completeAlertType(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, 0, len(api.IssueTypeAlertActions))
	for name := range api.IssueTypeAlertActions {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}
