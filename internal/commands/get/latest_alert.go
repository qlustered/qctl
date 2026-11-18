package get

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/stats"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// renderOptions is everything the renderers need that is not in the payload.
type renderOptions struct {
	tableID   int
	tableName string // "" when only --table-id was given
	format    string // "table" | "json" | "yaml" | "name"
}

// label names the table the way the caller asked for it. --table-id alone
// leaves the name unknown, and looking it up would cost a request the view does
// not otherwise need.
func (o renderOptions) label() string {
	if o.tableName == "" {
		return fmt.Sprintf("id %d", o.tableID)
	}
	return fmt.Sprintf("%s (id: %d)", o.tableName, o.tableID)
}

// latestAlertPayload is the -o json|yaml shape: the menu-stats fields that
// answer "what should I look at", plus the table they were resolved for.
type latestAlertPayload struct {
	TableID                       int     `json:"table_id" yaml:"table_id"`
	TableName                     string  `json:"table_name,omitempty" yaml:"table_name,omitempty"`
	LatestAlertItemID             *int    `json:"latest_alert_item_id" yaml:"latest_alert_item_id"`
	LatestAlertItemMsg            *string `json:"latest_alert_item_msg" yaml:"latest_alert_item_msg"`
	LatestAlertItemType           *string `json:"latest_alert_item_type" yaml:"latest_alert_item_type"`
	UnresolvedAlertsCount         int     `json:"unresolved_alerts_count" yaml:"unresolved_alerts_count"`
	AlertCountThatUserCantResolve int     `json:"alert_count_that_user_cant_resolve" yaml:"alert_count_that_user_cant_resolve"`
	ErrorsCount                   int     `json:"errors_count" yaml:"errors_count"`
}

// NewLatestAlertCommand creates the `get latest-alert` command.
func NewLatestAlertCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "latest-alert [table]",
		Short: "Show the alert to look at first on a table",
		Long: `Show the one alert worth looking at on a table, with the counts behind it.

This is the notification view, not a listing: it answers "is there anything I
need to act on, and which one first". For the full list, use qctl get alerts.

The alert reported is not always the newest one. When it depends on another
alert, the backend substitutes the one that must be resolved first, so the id
you get is the id to act on.

A table is required: pass it as the positional argument, or with --table
<name> or --table-id <id>. There is no org-wide form -- the endpoint reports
no latest alert without a table.

Add --watch to poll and print the answer again each time it changes, until
--timeout runs out. A change is any movement in the latest alert or in the
counts, measured against the previous poll of this same endpoint. Add --exit-on-change to return on the first change and exit, which is
the form to call from a script or an agent:

  qctl get latest-alert --table orders --watch
  qctl get latest-alert --table orders --watch --exit-on-change --timeout 10m -o json

When its time limit (--timeout) runs out, a watch exits 0; with
--exit-on-change and no change seen, it exits 124.

Examples:
  qctl get latest-alert orders
  qctl get latest-alert --table orders
  qctl get latest-alert --table orders -o name
  qctl get latest-alert --table-id 42 -o json`,
		Annotations: output.WithNameKind(cmdutil.WithArgKinds(catalog.For("latest-alert"), "table"), "alert"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			watch, interval, budget, exitOnChange, err := parseWatchFlags(cmd)
			if err != nil {
				return err
			}

			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			tableName, _ := cmd.Flags().GetString("table")
			if tableName == "" && positional != "" {
				// The positional resolves ID-first, so only a non-numeric one
				// is a name. Passing a numeric positional through here would
				// print an ID in the slot the view labels as a name.
				if _, convErr := strconv.Atoi(positional); convErr != nil {
					tableName = positional
				}
			}
			opts := renderOptions{
				tableID:   tableID,
				tableName: tableName,
				format:    latestAlertFormat(cmd),
			}

			client := stats.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			fetch := func(waitCtx context.Context) (*stats.MenuStats, error) {
				return client.GetMenuStatsContext(waitCtx, ctx.Credential.AccessToken, tableID)
			}

			if !watch {
				snapshot, err := fetch(cmd.Context())
				if err != nil {
					return err
				}
				return renderLatestAlert(cmd.OutOrStdout(), opts, snapshot)
			}

			spec := cmdutil.WaitSpec{
				Kind:   cmdutil.WatchStream,
				Budget: budget,
				Doing:  fmt.Sprintf("watching the latest alert on table %s every %s", opts.label(), interval),
				Next:   cmdutil.ResumeCommand(cmd),
			}
			if exitOnChange {
				spec.Kind = cmdutil.WaitUntilDone
				spec.Doing = fmt.Sprintf("waiting for the latest alert on table %s to change, checking every %s", opts.label(), interval)
			}
			waitCtx, w := cmdutil.StartWait(cmd, spec)
			defer w.Stop()
			return w.Ended(runWatchLoop(waitCtx, watchDeps{
				fetch:  fetch,
				out:    cmd.OutOrStdout(),
				errOut: cmd.ErrOrStderr(),
				now:    time.Now,
			}, watchOptions{
				render:       opts,
				interval:     interval,
				exitOnChange: exitOnChange,
			}))
		},
	}

	cmdutil.AddTableFlags(cmd, "Table to report on")
	cmd.Flags().Bool("watch", false, "Poll every --watch-interval seconds and print each change")
	cmd.Flags().Int("watch-interval", 3, "Interval in seconds for watch mode")
	cmd.Flags().Bool("exit-on-change", false, "In watch mode, print the first change and exit (requires --watch)")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)
	return cmd
}

// parseWatchFlags validates the watch flag group and, with --watch, resolves
// the watch's time budget (0 means no limit). Bad combinations are usage
// errors (exit 2), not runtime failures -- a script has to tell them apart.
func parseWatchFlags(cmd *cobra.Command) (watch bool, interval, budget time.Duration, exitOnChange bool, err error) {
	watch, _ = cmd.Flags().GetBool("watch")
	exitOnChange, _ = cmd.Flags().GetBool("exit-on-change")
	if exitOnChange && !watch {
		return false, 0, 0, false, qerrors.NewBadUsage("--exit-on-change requires --watch")
	}
	if cmd.Flags().Changed("timeout") && !watch {
		return false, 0, 0, false, qerrors.NewBadUsage("--timeout requires --watch")
	}

	seconds, err := cmdutil.PositiveSecondsFlag(cmd, "watch-interval")
	if err != nil {
		return false, 0, 0, false, err
	}

	if watch {
		if budget, err = cmdutil.WatchBudget(cmd); err != nil {
			return false, 0, 0, false, err
		}
	}

	return watch, time.Duration(seconds) * time.Second, budget, exitOnChange, nil
}

// latestAlertFormat reads -o, defaulting to the text view. Package get spells
// these as literals (see rule.go) -- the outputFormatJSON/outputFormatYAML
// constants live in package describe and are not visible here.
func latestAlertFormat(cmd *cobra.Command) string {
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "json", "yaml", "name": //nolint:goconst // Keep output format handling local to this command.
		return format
	default:
		return "table" //nolint:goconst // Keep output format handling local to this command.
	}
}

func buildLatestAlertPayload(opts renderOptions, s *stats.MenuStats) latestAlertPayload {
	payload := latestAlertPayload{
		TableID:                       opts.tableID,
		TableName:                     opts.tableName,
		LatestAlertItemID:             s.LatestAlertItemID,
		LatestAlertItemMsg:            s.LatestAlertItemMsg,
		UnresolvedAlertsCount:         s.UnresolvedAlertsCount,
		AlertCountThatUserCantResolve: s.AlertCountThatUserCantResolve,
		ErrorsCount:                   s.ErrorsCount,
	}
	if s.LatestAlertItemType != nil {
		issueType := string(*s.LatestAlertItemType)
		payload.LatestAlertItemType = &issueType
	}
	return payload
}

// alertTypeString renders the issue type, or "" when the backend omitted it.
func alertTypeString(t *api.AlertType) string {
	if t == nil {
		return ""
	}
	return string(*t)
}

// formatLatestAlertText renders the default view.
func formatLatestAlertText(opts renderOptions, s *stats.MenuStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s%s\n", "Table:", opts.label())
	if s.LatestAlertItemID == nil {
		fmt.Fprintf(&b, "%-14s%s\n", "Latest alert:", "none")
	} else {
		fmt.Fprintf(&b, "%-14s%d  %s\n", "Latest alert:", *s.LatestAlertItemID, alertTypeString(s.LatestAlertItemType))
		if s.LatestAlertItemMsg != nil && *s.LatestAlertItemMsg != "" {
			// The alert's Markdown message, on one line to keep the layout;
			// a marked value is printed whole, since the line has no width cap.
			fmt.Fprintf(&b, "%-14s%s\n", "", markdown.Inline(*s.LatestAlertItemMsg, markdown.InlineOptions{}))
		}
	}
	fmt.Fprintf(&b, "%-14s%d  (%d you cannot resolve), %d errors\n", "Unresolved:",
		s.UnresolvedAlertsCount, s.AlertCountThatUserCantResolve, s.ErrorsCount)
	if s.LatestAlertItemID != nil {
		fmt.Fprintf(&b, "%-14s-> qctl describe alert %d\n", "", *s.LatestAlertItemID)
	}
	return terminal.Sanitize(b.String())
}

// renderLatestAlert writes one snapshot in the requested format.
func renderLatestAlert(out io.Writer, opts renderOptions, s *stats.MenuStats) error {
	switch opts.format {
	case "json", "yaml":
		return encodeStructuredTo(out, opts.format, buildLatestAlertPayload(opts, s))
	case "name": //nolint:goconst // Keep output format handling local to this command.
		if s.LatestAlertItemID != nil {
			fmt.Fprintf(out, "alert/%d\n", *s.LatestAlertItemID)
		}
		return nil
	default:
		fmt.Fprint(out, formatLatestAlertText(opts, s))
		return nil
	}
}
