# Author, produce and deliver a spreadsheet

A **deliverable** is a named spreadsheet filled from table data for a receiver.
Its **revision**, selected by a release label, holds the template and bindings.
A **run** supplies the parameters for one report, then moves through render,
sign-off and delivery. The API calls the template a **skeleton**.

This example produces a monthly claims bordereau. It assumes an existing table
`claims-2026-09` with applied columns `claim_id`, `paid_amount` and `region`,
and organization backup settings for storing templates and rendered files.
Use your actual table, workbook and receiver in place of these examples.

Most deliverable operations need `deliverable:admin`, held by ops, admin,
member, editor and agent roles. Sign-off needs `deliverable:run:signoff`, held
by ops, admin and member roles. An agent or editor hands the rendered report
to a human with sign-off permission, then resumes after it is signed off.

## Author

### 1. Put the period table in a series

A series picks a different table for each explicit period key. Save this as
`table-series.yaml`:

```yaml
apiVersion: qluster.ai/v1
kind: TableSeries
metadata:
  name: Monthly claims
spec:
  members:
    - table: claims-2026-09
      period_key: "2026-09"
```

    qctl diff -f table-series.yaml
    qctl apply -f table-series.yaml --yes
    qctl describe table-series "Monthly claims"

Diff exits 0 for no changes and 1 when there are changes; an error also needs
attention before apply. A period key names one period of the series frequency:
`YYYY-MM` for a monthly series, the default, or `YYYY-QN` for a quarterly one.
The server derives each member's period dates from its key, and a manifest
cannot set them. Membership is explicit: dates do not choose the table.
When adding the next period, keep existing members in the manifest, because
omitted members are removed. A table belongs to only one series.

To identify the schema field holding each row's period, add both
`spec.period_column: report_month` and `spec.period_column_kind: period_key`
to the manifest. Use kind `date` for a date column. Set both fields to `null`
to clear the setting, or omit both to preserve it. Exports include both. Apply
saves a changed pair after the membership edits; a refusal leaves earlier
writes applied. An unchanged pair and membership-only edits do not revalidate it.

### 2. Prepare the template

Create `bordereau.xlsx` beside the deliverable manifest below, with a sheet
named `Claims`, headers in row 1 and columns A and B labelled `Claim ID` and
`Paid amount`. Leave row 2 and everything below it empty: the renderer fills
that data region. Put totals above the data region or on a separate sheet.
`.xlsm` templates are supported too.

`spec.template` uploads and inspects the workbook when apply creates a release.
If inspection reports fidelity losses, review them before setting
`spec.acknowledge_lossy: true`. Diff lists the planned upload without uploading
or inspecting the workbook.

### 3. Apply a draft release and its bindings

Save this as `deliverable.yaml`:

```yaml
apiVersion: qluster.ai/v1
kind: Deliverable
metadata:
  slug: monthly-bordereau
spec:
  name: Monthly bordereau
  receiver_label: Example carrier
  filename_pattern: "bordereau-{period}.xlsx"
  period_param: period
  release: "2026.1"
  template: ./bordereau.xlsx
  sheets:
    Claims:
      table: claims-2026-09
      series: Monthly claims
      header_rows: [1]
      data_start_row: 2
      columns:
        A: {column: claim_id}
        B: {column: paid_amount}
      row_filter:
        description: EU claims only
        conditions:
          - column_name: region
            operator: eq
            value: EU
            case_sensitive: true
```

    qctl diff -f deliverable.yaml
    qctl apply -f deliverable.yaml --yes
    qctl describe deliverable-revision monthly-bordereau --release 2026.1

Apply creates a draft; it does not enable it. The slug identifies the
deliverable and, on create, must match the slug generated from `spec.name`.
Sheet names match the workbook exactly. `table` is the reference table for
binding validation; `series` makes each run read the member matching its
`period` parameter. A fixed-table sheet omits `series`.

`columns` maps template column letters to exactly one of `column`, `run_param`
or `literal`, with an optional Excel `format`. Use `C: {literal: "Acme"}` for
fixed text on every kept row, or `C: {literal: "0.25", format: "0%"}`. An
empty string is valid; null does not select a literal source. A literal
holds at most 32767 characters and no control character but tab, newline and
carriage return; `diff -f` and `apply -f` refuse any other literal.
`spec.cells` can bind fixed text, run values and totals; a total with
`same_rows_as: Claims` follows that sheet's rows. Inspect the manifest schema
before extending the example:

    qctl explain deliverable --recursive

For an existing deliverable, start from its export:

    qctl get deliverable monthly-bordereau -o yaml > deliverable.yaml

Enabled and disabled releases are frozen. Bump `spec.release` to author the
next draft, which copies the enabled release when one exists. Omitted
`spec.sheets` or `spec.cells` preserves those bindings; supplying either map
manages its whole set, removing entries omitted from it. Review diff before
confirming removals; a protected context also requires `--context <context>`.
Basic fields such as receiver and filename pattern take effect immediately
when applied, independently of enabling the draft.

On an existing draft, `spec.template` is not compared or replaced by apply.
Replace it explicitly, then inspect any moved or broken bindings:

    qctl upload deliverable-template ./bordereau.xlsx --deliverable monthly-bordereau --release 2026.1

Review columns flagged `needs_confirmation` before confirming their mappings:

    qctl deliverable confirm-columns monthly-bordereau --release 2026.1 --sheet Claims --column A,B

Review cells flagged `needs_confirmation` before confirming their positions:

    qctl deliverable confirm-cells monthly-bordereau --release 2026.1 --sheet Summary --cell B2,D2

The cell sheet need not have a sheet binding. These commands re-save only the
named flagged bindings with their existing source, filter and format; matching
manifests leave them unchanged and show a confirmation command in apply/diff.

### 4. Check the row filter and preview the report

    qctl deliverable test-filter monthly-bordereau --release 2026.1 --sheet Claims --period 2026-09
    qctl inspect deliverable monthly-bordereau --release 2026.1 --period 2026-09

The saved filter report shows kept, left-out and undecided (blocked) counts,
plus a sample of left-out rows. A series sheet requires `--period`; a
fixed-table sheet does not accept it. To count an edited filter before saving
it, use the manifest form with the period table explicitly selected:

    qctl deliverable test-filter -f deliverable.yaml --sheet Claims --table claims-2026-09

That form counts the unsaved filter; it does not save it or produce a file.
Preview reads live data. Neither preview nor filter counts replace a run's
readiness check.

### 5. Validate, then enable

    qctl enable deliverable monthly-bordereau --release 2026.1 --dry-run
    qctl enable deliverable monthly-bordereau --release 2026.1

`--dry-run` validates without enabling: errors exit 2 and warnings print on
stderr. Fix errors in the draft manifest, apply again and repeat validation.
Enabling validates again and replaces the enabled release. Read the listed
runs that will follow the new release; rendered ones that move need a
re-render. A re-issue using its delivered setup stays on that setup.

## Produce

### 1. Create one run

    qctl create deliverable-run monthly-bordereau --period 2026-09

Save the printed `deliverable-run/<id>`; use that id as `<run-id>` below.
Do not create again to check progress:

    qctl describe deliverable-run <run-id>
    qctl get deliverable-runs monthly-bordereau

For several parameters, save a YAML scalar mapping in `params.yaml` and use
`--params-file params.yaml` instead of `--period`:

```yaml
period: "2026-09"
```

    qctl create deliverable-run monthly-bordereau --params-file params.yaml

These are alternative ways to create one run. Keep parameter types and the
whole key set consistent. Parameters matching a delivered run start a
re-issue on its delivered revision; `--use-enabled-revision` selects the
enabled revision instead. Otherwise they start a new period on the enabled
revision. Only the latest delivered period can be re-issued. A new period
already in flight must be delivered or cancelled before starting another.
Use `--allow-empty` at create only when an empty report is intended.

### 2. Read readiness; fix or waive blocked rows

    qctl deliverable-run readiness <run-id>
    qctl deliverable-run readiness <run-id> --bucket blocked_failing_rules
    qctl deliverable-run readiness <run-id> --bucket blocked_awaiting_review

Read both setup errors and blocked rows. Fix data upstream with
`qctl guide edit-data`, or request the necessary human review with
`qctl guide review-data`. For an eligible row deliberately left out of this
run, name its table and row from readiness and record why:

    qctl deliverable-run waive <run-id> --table-id <table-id> --row-id <row-id> --reason "Duplicate claim; excluded from this report"

Waivers leave eligible blocked or unbound carried rows out of this run and
change no table data. They do not exclude an already included row. Save the
created waiver id: readiness shows the reason, but no waiver id, and there
is no waiver listing. To include it again before sign-off:

    qctl deliverable-run unwaive <run-id> <waiver-id> --context <context>

Replace `<context>` with the intended named context. Unwaive permanently
removes the waiver and its reason.

    qctl deliverable-run readiness <run-id>

Proceed when setup errors and unwaived blockers are resolved. A clear
readiness result alone does not approve the rendered file.

### 3. Render and review the draft

    qctl deliverable-run render <run-id> --wait --wait-timeout 900
    qctl describe deliverable-run <run-id>
    qctl download deliverable-run <run-id> --output bordereau-review.xlsx

Render queues a worker; `--wait` follows the first attempt after the request.
Exit 0 means rendered without a render error, 1 means a failed or stale
outcome, and 124 means only that the wait timed out. On timeout, read the run
before requesting another render. A failed re-render can leave the older
file available, so check the render result and `render_error` before reviewing
or signing off. If readiness or the setup changes, resolve it and re-render.

The review download is a draft. qctl verifies its SHA-256 against the run's
recorded signature. An existing output file requires a new path or `--force`.
Review the workbook, parameters, last-render totals and left-out rows.

### 4. Have a human sign off

The member, admin or ops reviewer uses their own session after reviewing the
report. An agent or editor stops at this handoff until sign-off is complete:

    qctl deliverable-run sign-off <run-id> --yes --context <context>

`--yes` skips confirmation after the reviewer has approved the action; it
does not satisfy the protected-context gate, which needs the named context.
The command prints the attempt, parameters, totals, waived count and live
readiness before sending that attempt as `expected_attempt_id`. On
`STALE_ATTEMPT`, review the current render before trying again. On
`SIGNOFF_BLOCKED`, read the refusal and readiness; changed rows can require a
re-render even when blocked counts are zero.

Sign-off locks the report. A signed-off new-period run must be delivered;
it cannot be cancelled. Verify the handoff result:

    qctl get deliverable-run <run-id>

### 5. Download, send and record delivery

After state is `signed_off`, download the final file:

    qctl download deliverable-run <run-id>

The server filename now omits `-DRAFT`; qctl verifies the file signature.
Send this file to the receiver through your agreed email or portal channel.
Then record what was sent:

    qctl deliverable-run mark-delivered <run-id> --note "Sent to the carrier through the portal" --yes --context <context>

Mark-delivered records delivery; it does not send the file. It requires a
signed-off run, and delivery cannot be reversed. To abandon an open, rendered
or stale run instead:

    qctl deliverable-run cancel <run-id> --yes --context <context>

Cancellation cannot be reversed. Read the server's period-order refusal if
sign-off or delivery is blocked by another pending run.

## Follow up

    qctl deliverable completeness monthly-bordereau
    qctl deliverable-run drift <run-id>

Completeness reports live clean rows missed by delivered reports, with their
waiver, carrier or re-issue disposition. Drift compares a delivered run's
recorded rows with live data; it is available only after delivery. Both are
reports: they do not create a correction or change the delivered file.

Read drift's `remedy`, `can_reissue`, `reissue_run_id` and
`superseded_by_run_id` (`-o json` preserves the full response). A latest-period
correction can use a re-issue with the delivered run's exact parameters, then
repeat readiness, render, human sign-off and delivery. If a re-issue already
exists, inspect it first. An older period's `restatement` needs an operator
to arrange the correction; do not invent a different parameter set to bypass
the latest-period rule.

## UI words and qctl commands

| Web app wording | qctl |
|---|---|
| Setup version | `deliverable-revision`, selected with `--release` |
| Turn on | `enable deliverable` |
| Make the draft file / Re-make draft | `deliverable-run render` |
| Leave out / Include again | `deliverable-run waive` / `deliverable-run unwaive` |
| Change table | `deliverable-run override-source` |
| Missed rows | `deliverable completeness` |
| Changes after delivery | `deliverable-run drift` |
| Delete / Restore from trash | `delete deliverable` / `undelete deliverable` |

## See also

- `qctl explain deliverable`
- `qctl explain deliverable-revision`
- `qctl explain deliverable-template`
- `qctl explain deliverable-run`
- `qctl explain table-series`
- `qctl guide edit-data`
- `qctl guide review-data`
