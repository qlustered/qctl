package apply

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewLinkedTableCommand creates the apply linked-table command.
func NewLinkedTableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "linked-table",
		Short: "Apply a linked table (composite dataset) from a file",
		Long: `Apply a linked table configuration from a YAML file.

To create a plain single table (dataset) instead, see 'qctl apply table'.

A linked table (the backend calls it a "composite dataset") joins one or more
upstream tables onto a spine table to produce a downstream table. This command
is idempotent: the backend creates the linked table if it does not exist, or
converges the existing one.

SCHEMA
======
  apiVersion: qluster.ai/v1            # Required. Must be "qluster.ai/v1"
  kind: LinkedTable                    # Required. "LinkedTable" or "CompositeDataset"
  metadata:
    name: <string>                     # Required. group slug + dataset name
  spec:
    destination: <string>              # Required. Destination name
    backup_settings: <string>          # Optional
    table:
      database: <string>               # Required
      schema: <string>                 # Optional, default "public"
      name: <string>                   # Required
    spine:
      source: <string>                 # Required. A key in spec.sources
      natural_key_columns: [<string>]  # Required, >= 1
      natural_key_normalization:       # Optional
        <column>: <rule>
      identity_hash_version: <int>     # Optional, default 1
    sources:                           # Required, >= 1 entry
      <key>:
        dataset: <string>              # Required. Upstream table name
        arity: <string>                # Optional: exactly_one|zero_or_one|at_least_one
        join_fields:                   # Optional
          <spine_col>: <source_col>    # shorthand string, OR an expanded clause:
          <spine_col>:
            source_fields: [<string>]  # Required in clause form, >= 1
            spine_fields: [<string>]   # Optional
            spine_expr: <string>       # Optional SQL template, ${0}, ${1}, ...
            source_expr: <string>      # Optional SQL template
            constant: <any>            # Optional (mutually exclusive with spine_fields)
    output:                            # Required, >= 1 entry
      <field>:
        source: <string>               # Required. A key in spec.sources
        field: <string>                # Required. Source column name
    desired_state: <string>            # Optional: enabled|draft (default enabled)
    release: <string>                  # Optional
    description: <string>              # Optional
    dry_run:                           # Optional. Knobs for the composite dry
      max_spine_rows: <int>            #   run the apply launches (default 1000);
      seed: <int>                      #   NOT the --validate-only flag.

OUTPUT
======
  linked-table/<name> created, configured (a new spec revision) or unchanged
  (the spec matches an existing revision), then:
    revision : the revision's release and its state after the apply
    dry run  : the composite dry run the apply launched, if any, and whether
               the revision is enabled when it succeeds
  and a Next: line. Warnings print to stderr as 'Warning:' lines. -o json and
  -o yaml print the server's answer, and the lines above go to stderr.

WAIT
====
  --wait polls the composite dry run the apply launched until it finishes
  and, with desired_state: enabled, until the backend has decided whether to
  enable the revision; a line naming the wait, then each change of state,
  prints to stderr. It then prints how the dry run ended -- its state, the
  revision's state and auto-enable outcome, the sample and activation-blocker
  counts -- and a Next: line. A dry run that does not finish, or an
  auto-enable the backend withheld or failed, exits 1 with the reason. An
  apply that launched no dry run returns at once. --wait-timeout (seconds,
  default 900) bounds the wait; running out exits 124, and the dry run
  carries on, as it does when Ctrl-C stops the wait (exit 130). Under -o json
  or yaml stdout is the server's answer as without --wait, and how the dry run
  ended goes to stderr.

VALIDATE-ONLY
=============
  --validate-only sets validate_only=true: the backend validates the manifest
  and persists NOTHING. A manifest that fails exits 2, listing each error as
  '<loc>: <msg> (<code>)'; warnings print to stderr as 'Warning:' lines. It
  launches no dry run; spec.dry_run only tunes the one a real apply launches.
  To save a non-active linked table, omit --validate-only and set
  spec.desired_state: draft.

EXAMPLE
=======
qctl apply linked-table -f linked-table.yaml
qctl apply linked-table -f linked-table.yaml --validate-only
qctl apply linked-table -f linked-table.yaml --wait`,
		Annotations: cmdutil.WithFormats(catalog.For("linked-table"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)")
			}
			if _, _, err := linkedTableWaitFlags(cmd); err != nil {
				return err
			}
			return applyStream(cmd, filePath, onlyKind(applyLinkedTable))
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	cmd.Flags().Bool("validate-only", false, "Validate the manifest server-side without persisting")
	cmdutil.AddWaitFlags(cmd, "the composite dry run the apply launches to settle")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// linkedTableWaitFlags reads --wait and --wait-timeout (cmdutil.WaitFlags),
// refusing --wait beside --validate-only, which launches no dry run.
func linkedTableWaitFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	validateOnly, _ := cmd.Flags().GetBool("validate-only")
	if wait, _ := cmd.Flags().GetBool("wait"); wait && validateOnly {
		return 0, false, qerrors.NewBadUsage("cannot use both --validate-only and --wait flags")
	}
	return cmdutil.WaitFlags(cmd)
}

// stateEnabled is the revision_state and desired_state value of an enabled revision.
const stateEnabled = "enabled"

const stateDraft = "draft"

// linkedTableApplyOutcome exists only to fit Apply's three return values
// through RetryOnThrottle's single type parameter.
type linkedTableApplyOutcome struct {
	response *linked_tables.ApplyResponse
	raw      []byte
}

// applyLinkedTable is shared by the linked-table subcommand and generic dispatch.
func applyLinkedTable(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	manifest, err := linked_tables.LoadManifest(doc.Data)
	if err != nil {
		return nil, err
	}
	waitTimeout, wait, err := linkedTableWaitFlags(cmd)
	if err != nil {
		return nil, err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}

	apiClient, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	if validateOnly, _ := cmd.Flags().GetBool("validate-only"); validateOnly {
		return validateLinkedTable(cmd, apiClient, ctx.Credential.AccessToken, manifest)
	}

	// Converging a manifest usually launches a composite dry run (atlas
	// capi/capi/controllers/composite_dataset_apply_controller.py _converge; an
	// enabled revision, or one that already has a current successful dry run,
	// converges without one), so a full exploratory budget refuses the apply
	// with 429. Retrying the identical request is the right response: the 429
	// rolls the whole request back (get_session_for_fastapi,
	// cettings/cettings/db.py) and apply is create-or-converge, so nothing is
	// half-written to re-send.
	//
	// Progress goes to stderr, not stdout: under -o json or yaml this
	// command's stdout is a document a script parses. (applyTableRuleInstantiate passes
	// cmd.OutOrStdout() instead -- a pre-existing interleaving risk documented
	// on RetryOnThrottle, not a pattern to copy.)
	outcome, err := cmdutil.RetryOnThrottle(cmd.ErrOrStderr(), "dry runs", func() (linkedTableApplyOutcome, error) {
		response, raw, applyErr := apiClient.Apply(ctx.Credential.AccessToken, manifest)
		return linkedTableApplyOutcome{response: response, raw: raw}, applyErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to apply linked table: %w", err)
	}
	response, raw := outcome.response, outcome.raw
	if response != nil {
		linked_tables.WriteWarnings(cmd.ErrOrStderr(), response.Warnings)
	}

	waiting := wait && response != nil && response.CompositeDryRunJobID != nil
	out := humanOut(cmd)
	writeLinkedTableApplied(out, manifest.Metadata.Name, response, waiting)
	var id any = labelValue(manifest.Metadata.Name)
	if response != nil && response.DatasetID > 0 {
		id = response.DatasetID
	}
	result := namedApplyResult(linkedTableAppliedDocument(manifest.Metadata.Name, raw), "linked-table", id)
	if !waiting {
		return result, nil
	}
	return result, waitForLinkedTableDryRun(cmd, ctx, out, response, waitTimeout)
}

// linkedTableAppliedDocument is the apply's -o json|yaml document: the
// server's answer, or a {name, status, action} stand-in when the body is
// empty or null.
func linkedTableAppliedDocument(name string, raw []byte) any {
	if body, ok := rawJSONDocument(raw); ok {
		return body
	}
	return struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Action string `json:"action"`
	}{name, "applied", "applied"}
}

// waitForLinkedTableDryRun is --wait: it polls the composite dry run the apply
// launched until it settles, printing progress to stderr, and prints how it
// ended to out. A dry run that fails composite_dry_runs.Verdict is the
// returned error.
func waitForLinkedTableDryRun(cmd *cobra.Command, ctx *cmdutil.CommandContext, out io.Writer,
	resp *linked_tables.ApplyResponse, timeout time.Duration,
) error {
	client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	detail, err := awaitLinkedTableDryRun(cmd, client, ctx.Credential.AccessToken, *resp.CompositeDryRunJobID, timeout)
	if err != nil {
		return err
	}
	if err := composite_dry_runs.Verdict(detail); err != nil {
		return err
	}
	revision := resp.Release
	if revision == "" {
		revision = resp.CompositeSpecRevisionID.String()
	}
	composite_dry_runs.WriteOutcome(out, detail, revision)
	return nil
}

// awaitLinkedTableDryRun is client.Wait for dry run jobID, writing progress
// to stderr, inside a cmdutil.StartWait whose banner names the describe
// command, and returns what Waiter.Ended makes of the wait's error.
func awaitLinkedTableDryRun(cmd *cobra.Command, client *composite_dry_runs.Client, token string,
	jobID int, timeout time.Duration,
) (*composite_dry_runs.Detail, error) {
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for composite dry run %d to settle", jobID),
		Next:      fmt.Sprintf("qctl describe composite-dry-run-job %d", jobID),
		NextVerb:  "check with",
	})
	defer w.Stop()
	detail, err := client.Wait(ctx, token, jobID, composite_dry_runs.WaitOptions{
		Timeout:  timeout,
		Progress: func(d *composite_dry_runs.Detail) { composite_dry_runs.WriteProgress(cmd.ErrOrStderr(), d) },
	})
	return detail, w.Ended(err)
}

// writeLinkedTableApplied prints the apply outcome: a result line in kubectl's
// words, the revision and dry-run lines, and a Next: line, left out for a
// launched dry run when waiting is set. A nil response prints only
// "linked-table/<name> applied".
func writeLinkedTableApplied(w io.Writer, name string, resp *linked_tables.ApplyResponse, waiting bool) {
	name = terminal.Sanitize(name)
	if resp == nil {
		fmt.Fprintf(w, "linked-table/%s applied\n", name)
		return
	}
	result := terminal.Sanitize(resp.Result)
	if resp.Result == "revision_added" {
		result = "configured"
	}
	fmt.Fprintf(w, "linked-table/%s %s\n", name, result)

	release := terminal.Sanitize(resp.Release)
	enabled := resp.RevisionState == stateEnabled
	if resp.Release != "" {
		state := terminal.Sanitize(resp.RevisionState)
		if enabled && resp.DesiredState == stateDraft {
			state += "; desired_state: draft never disables an enabled revision"
		}
		fmt.Fprintf(w, "  revision : %s (%s)\n", release, state)
	}

	switch {
	case resp.CompositeDryRunJobID != nil:
		carrier := ""
		if resp.IngestionJobID != nil {
			carrier = fmt.Sprintf(" (ingestion job %d)", *resp.IngestionJobID)
		}
		then := "desired_state is draft, so it does not enable the revision"
		if resp.DesiredState == stateEnabled {
			then = "the revision is enabled if it succeeds and the enable gates pass"
		}
		fmt.Fprintf(w, "  dry run  : composite dry run %d launched%s; %s\n", *resp.CompositeDryRunJobID, carrier, then)
		if !waiting {
			fmt.Fprintf(w, "Next: qctl describe composite-dry-run-job %d\n", *resp.CompositeDryRunJobID)
		}
	case enabled:
		if resp.CompositeSpecGroupID != uuid.Nil {
			fmt.Fprintf(w, "Next: qctl get composite-run-jobs --group-id %s\n", resp.CompositeSpecGroupID)
		}
	case resp.Converged && resp.DesiredState == stateDraft:
		fmt.Fprintln(w, "  dry run  : the revision already has a current successful dry run")
		// --revision because the revision an apply matched by spec hash can
		// be older than the table's latest one, which enable picks by default.
		if resp.CompositeSpecRevisionID != uuid.Nil {
			fmt.Fprintf(w, "Next: qctl enable linked-table --table %s --revision %s\n",
				cmdutil.ShellArg(name), resp.CompositeSpecRevisionID)
		}
	}
}

// validateLinkedTable is --validate-only: the apply route with
// validate_only=true, which persists nothing. The route answers 200 whether or
// not the manifest passes, so the verdict is read from the body, and a
// valid:false answer exits 2 -- the code the real apply's 400 gives the same
// manifest. Warnings go to stderr either way.
func validateLinkedTable(cmd *cobra.Command, apiClient *linked_tables.Client, token string, manifest *linked_tables.LinkedTableManifest) (*applyResult, error) {
	result, raw, err := apiClient.Validate(token, manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to validate linked table: %w", err)
	}
	linked_tables.WriteWarnings(cmd.ErrOrStderr(), result.Warnings)

	if result.Valid {
		fmt.Fprintf(humanOut(cmd), "linked-table/%s validated\n", terminal.Sanitize(manifest.Metadata.Name))
	}
	var document any
	if body, ok := rawJSONDocument(raw); ok {
		document = body
	}
	applied := &applyResult{document: document}
	if result.Valid {
		applied = namedApplyResult(document, "linked-table", labelValue(manifest.Metadata.Name))
	}
	return applied, result.Err(manifest.Metadata.Name)
}

// rawJSONDocument returns a non-null JSON response body as a document, and
// false for an empty, null or malformed one.
func rawJSONDocument(raw []byte) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" || !json.Valid(trimmed) { //nolint:goconst // Keep the JSON literal beside the raw-response check.
		return nil, false
	}
	return json.RawMessage(trimmed), true
}
