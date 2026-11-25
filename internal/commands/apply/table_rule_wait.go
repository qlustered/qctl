package apply

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// tableRuleApplyOptions holds a TableRule document's apply options and clients.
type tableRuleApplyOptions struct {
	yes         bool
	suggestions *rule_suggestions.Client
	// wait is --wait; nil without it.
	wait *tableRuleWait
}

// tableRuleWait is --wait for a TableRule document: its timeout, and the
// clients a wait reads beyond the rule itself.
type tableRuleWait struct {
	// cmd is the command a wait's cmdutil.StartWait runs under.
	cmd     *cobra.Command
	timeout time.Duration
	// canPrompt is cmdutil.CanPrompt(), which decides the --yes suffix of a
	// suggested apply_migration line.
	canPrompt     bool
	dryRuns       *dry_runs.Client
	notifications *notifications.Client
	alerts        *alerts.Client
	datasets      *datasets.Client
}

// tableRuleOptions builds the apply options and clients.
func tableRuleOptions(cmd *cobra.Command, ctx *cmdutil.CommandContext) (tableRuleApplyOptions, error) {
	yes, _ := cmd.Flags().GetBool("yes")
	suggestions, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return tableRuleApplyOptions{}, err
	}
	opts := tableRuleApplyOptions{yes: yes, suggestions: suggestions}
	timeout, wait, err := cmdutil.WaitFlags(cmd)
	if err != nil || !wait {
		return opts, err
	}
	opts.wait = &tableRuleWait{
		cmd:           cmd,
		timeout:       timeout,
		canPrompt:     cmdutil.CanPrompt(),
		dryRuns:       dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity),
		notifications: notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity),
		alerts:        alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity),
		datasets:      datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity),
	}
	return opts, nil
}

// writeNothingToWaitFor prints the line --wait leaves for a document whose
// apply started no wait.
func writeNothingToWaitFor(w io.Writer, why string) {
	fmt.Fprintf(w, "--wait: nothing to wait for: %s\n", why)
}

// writeStagedNothingToWaitFor is writeNothingToWaitFor for a staged
// instantiate; it does nothing when wait is nil.
func writeStagedNothingToWaitFor(w io.Writer, staged *dataset_rules.InstantiateSuggestion, wait *tableRuleWait) {
	if wait == nil {
		return
	}
	writeNothingToWaitFor(w, fmt.Sprintf("the instantiation was staged for review as suggestion %s, so no rule exists yet", staged.ID))
}

// nothingToWaitFor is why a 201 starts no wait, "" when it starts one: a wait
// starts for a rule in pending_validation whose validation_state is queued,
// in_flight or absent.
func nothingToWaitFor(created *dataset_rules.DatasetRuleDetail) string {
	label := fmt.Sprintf("table rule %s (%s)", terminal.Sanitize(created.InstanceName), created.ID)
	var validation api.ValidationEvidenceKind
	if created.ValidationState != nil {
		validation = *created.ValidationState
	}
	switch {
	case created.State == api.DatasetRuleStateEnabled:
		return label + " was created enabled"
	case created.State != api.DatasetRuleStatePendingValidation:
		return fmt.Sprintf("%s was created %s", label, terminal.Sanitize(string(created.State)))
	}
	switch validation {
	case "", api.ValidationEvidenceKindQueued, api.ValidationEvidenceKindInFlight:
		return ""
	case api.ValidationEvidenceKindNone:
		return label + " has no validation dry run queued (validation: none)"
	}
	return fmt.Sprintf("%s was created pending_validation (validation: %s)", label, terminal.Sanitize(string(validation)))
}

// waitForCreatedTableRule is --wait for a 201, and returns the rule as last
// read: created itself unless a poll read it again. It does nothing when wait
// is nil, and prints one line to errOut when the rule starts no wait
// (nothingToWaitFor). Otherwise it polls the rule until its validation
// settles, printing progress to errOut, and prints how it ended to out. The
// returned error is the timeout, a poll's permanent failure, the
// *qerrors.Interrupted of a signal, or dataset_rules.ValidationVerdict for an
// outcome that did not pass.
func waitForCreatedTableRule(out, errOut io.Writer, client *dataset_rules.Client, token string, created *dataset_rules.DatasetRuleDetail, wait *tableRuleWait) (*dataset_rules.DatasetRuleDetail, error) {
	if wait == nil {
		return created, nil
	}
	if why := nothingToWaitFor(created); why != "" {
		writeNothingToWaitFor(errOut, why)
		return created, nil
	}

	snapshot, outcome, err := awaitTableRuleValidation(errOut, client, token, created, wait)
	latest := created
	if snapshot != nil && snapshot.Rule != nil {
		latest = snapshot.Rule
	}
	if err != nil {
		return latest, err
	}

	dataset_rules.WriteValidationOutcome(out, snapshot, outcome)
	if outcome == dataset_rules.ValidationPassedPendingMigration {
		renderValidatedMigrationFollowUp(out, token, snapshot.Rule, wait)
	}
	fmt.Fprintln(out)
	return latest, dataset_rules.ValidationVerdict(snapshot, outcome, validationReason(token, snapshot, outcome, wait))
}

// awaitTableRuleValidation is dataset_rules.WaitForValidation for created,
// writing progress to errOut, inside a cmdutil.StartWait on wait.cmd whose
// banner names dataset_rules.ValidationCheckCommand, and returns what
// Waiter.Ended makes of the wait's error.
func awaitTableRuleValidation(errOut io.Writer, client *dataset_rules.Client, token string,
	created *dataset_rules.DatasetRuleDetail, wait *tableRuleWait,
) (*dataset_rules.ValidationSnapshot, dataset_rules.ValidationOutcome, error) {
	ctx, w := cmdutil.StartWait(wait.cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    wait.timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for table rule %s to finish validation", created.InstanceName),
		Next:      dataset_rules.ValidationCheckCommand(created),
		NextVerb:  "check with",
	})
	defer w.Stop()
	ruleID := created.ID.String()
	snapshot, outcome, err := dataset_rules.WaitForValidation(ctx, created,
		func(ctx context.Context) (*dataset_rules.DatasetRuleDetail, error) {
			return client.GetDatasetRuleDetailContext(ctx, token, ruleID)
		},
		func(ctx context.Context, jobID int) (*api.DryRunJobFullSchema, error) {
			return wait.dryRuns.GetDryRunJobContext(ctx, token, jobID)
		},
		dataset_rules.ValidationWaitOptions{
			Timeout:  wait.timeout,
			Progress: func(s *dataset_rules.ValidationSnapshot) { dataset_rules.WriteValidationProgress(errOut, s) },
			LockedPeriod: func(ctx context.Context, datasetID int) (*datasets.LockedPeriod, error) {
				table, err := wait.datasets.GetDatasetContext(ctx, token, datasetID)
				if err != nil {
					return nil, err
				}
				return datasets.LockedPeriodOf(table), nil
			},
		})
	return snapshot, outcome, w.Ended(err)
}

// validationReason gathers what ValidationVerdict reports for an outcome that
// did not pass: the caller's validation_failed notification for the rule, for
// a failed rule, one with no validation queued or one held by a locked
// period, and the rule's validation
// dry run -- the snapshot's, or for a failed rule, read by id. A read that
// fails leaves its part out.
func validationReason(token string, s *dataset_rules.ValidationSnapshot, outcome dataset_rules.ValidationOutcome, wait *tableRuleWait) dataset_rules.ValidationReason {
	if outcome.Passed() {
		return dataset_rules.ValidationReason{}
	}
	reason := dataset_rules.ValidationReason{Job: s.Job}
	switch outcome {
	case dataset_rules.ValidationFailed, dataset_rules.ValidationNotQueued, dataset_rules.ValidationHeldLocked:
		reason.Notification = validationFailedNotification(token, s.Rule, wait.notifications)
	}
	if reason.Job == nil && outcome == dataset_rules.ValidationFailed && s.Rule.ValidationDryRunJobID != nil {
		if job, err := wait.dryRuns.GetDryRunJob(token, *s.Rule.ValidationDryRunJobID); err == nil {
			reason.Job = job
		}
	}
	return reason
}

// validationNotificationLimit is the notification listing's maximum page
// size.
const validationNotificationLimit = 100

// validationFailedNotification is the msg of the caller's validation_failed
// notification for rule, dismissed ones included, or "" when the first page
// holds none.
func validationFailedNotification(token string, rule *dataset_rules.DatasetRuleDetail, client *notifications.Client) string {
	list, err := client.List(token, notifications.ListOptions{
		Types:            []notifications.Type{api.ValidationFailed},
		IncludeDismissed: true,
		Limit:            validationNotificationLimit,
	})
	if err != nil {
		return ""
	}
	for _, n := range list.Results {
		if n.DatasetRuleID != nil && *n.DatasetRuleID == rule.ID && n.Msg != nil {
			return *n.Msg
		}
	}
	return ""
}

// renderValidatedMigrationFollowUp prints the draft's review alert or its lookup,
// with unlock guidance when the table's period is locked.
func renderValidatedMigrationFollowUp(out io.Writer, token string, rule *dataset_rules.DatasetRuleDetail, wait *tableRuleWait) {
	migrationID := *rule.PendingMigrationModelID
	applyMigration := string(api.AlertActionApplyMigration)
	alertID, found := wait.alerts.FindMigrationReviewAlert(token, rule.DatasetID, migrationID)
	if !found {
		if table, err := wait.datasets.GetDataset(token, rule.DatasetID); err == nil {
			if locked := datasets.LockedPeriodOf(table); locked != nil {
				renderLockedMigrationFollowUp(out, rule.DatasetID, locked, wait.canPrompt)
				return
			}
		}
	}
	if found {
		fmt.Fprintf(out, "  alert       : %d (needs_review_migration for draft migration %d)\n", alertID, migrationID)
	} else {
		fmt.Fprintf(out, "  alert       : no unresolved needs_review_migration alert on table %d names draft migration %d\n", rule.DatasetID, migrationID)
	}
	fmt.Fprintf(out, "  note        : %s applies the table's WHOLE draft migration, not only this rule's columns\n", applyMigration)
	fmt.Fprintln(out, "  key columns : a mirror or append table's first migration also takes --key-columns <a,b> or --no-key-columns")
	fmt.Fprintln(out)
	if found {
		renderBreadcrumbs(out, []breadcrumb{
			{fmt.Sprintf("qctl alert resolve %d --action %s%s", alertID, applyMigration, alerts.YesFlag(applyMigration, wait.canPrompt)), "apply the draft; that enables the rule"},
			{fmt.Sprintf("qctl describe table-schema --table-id %d", rule.DatasetID), "the draft carrying the columns"},
		})
		return
	}
	renderBreadcrumbs(out, []breadcrumb{
		{fmt.Sprintf("qctl get alerts --table-id %d --issue-type needs_review_migration", rule.DatasetID), "find the review alert"},
		{fmt.Sprintf("qctl describe table-schema --table-id %d", rule.DatasetID), "the draft carrying the columns"},
	})
}

// renderLockedMigrationFollowUp prints the lock and how to find the review alert after unlocking.
func renderLockedMigrationFollowUp(out io.Writer, datasetID int, locked *datasets.LockedPeriod, canPrompt bool) {
	fmt.Fprintf(out, "  alert       : %s is locked; unresolved migration alerts are hidden\n\n", locked)
	unlock := locked.UnlockCommand()
	if !canPrompt {
		unlock += " --yes"
	}
	renderBreadcrumbs(out, []breadcrumb{
		{unlock, "unlock the period (requires deliverable:admin)"},
		{fmt.Sprintf("qctl get alerts --table-id %d --issue-type needs_review_migration", datasetID), "find the review alert after unlocking"},
		{fmt.Sprintf("qctl describe table-schema --table-id %d", datasetID), "the draft carrying the columns"},
	})
}
