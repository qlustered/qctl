# Review quarantined rows: request, decide, release

> **Which review guide?** Quarantined rows: `qctl guide review-data`. A rule
> revision, as its author: `qctl guide request-review`; as a reviewer:
> `qctl guide review-rules`. Whether rows need review at all: `qctl guide
> data-review-policy`; whether rules do: `qctl explain rule-review-policy`.

The requester submits quarantined rows and answers feedback; reviewers decide
each edited cell or the whole row. Every flag and edge case is in
`qctl explain data-review`.

A **data review** is one row's review (a UUID every `qctl data-review` verb
takes). A **submission** (`data-review-group`) is one `request`, a titled
batch of rows. A **change** is one edited cell, named by field and history id
(`[h: 811]`). Statuses are derived, never set: `open`, `changes_requested`,
`approved`, `closed` (final; "cancel" closes).

## Who can do what

| Workflow | Server checks |
|---|---|
| `get data-reviews` | Read the data page and its by-rows review overlay, with access to the table or assigned sources. |
| `request` | Submit a batch of rows, with access to the table or assigned sources. |
| `describe`, group and change listings, `progress` | Review-list access; progress also reads row counts. |
| `assign`, `unassign`, `re-request`, `close`, and the `data-review-group` verbs | Review-edit access; callers other than ops/admin also need an editor or owner table grant. |
| `approve`, `withdraw`, `accept-cell` | Approval access; cell verbs also need review-edit access. |
| `clear-cell`, `withdraw-cell` | Review-edit access, plus approval access over a stored veto; withdrawal also checks ownership. |
| `comment` | Comment-write access and access to the review's table or its row's assigned source. |

Check your current organization role with `qctl auth me`. A refused command
exits 4. Requesting and commenting do not grant approval rights: an agent can
clear or withdraw a non-blocking cell decision, but cannot accept a cell, lift
a veto or decide a row.

External callers can read their assigned source rows with `get data` and
`get data-reviews`, submit those rows with `request`, and comment on their
reviews. Use `--table-id` for row reads and requests, and full UUIDs for
`--reviewer` and `--mention`: table-name and user-directory lookups need
separate access. Review detail, groups, progress and approval remain separate
checks, so the full requester loop below is for callers with that access.
The `read_only` role can comment on reviews of tables it can access.

## The requester loop

### 1. Find and submit the rows

```bash
qctl get data-review-policy --table orders
qctl get data orders --is-bad-data --held-for-approval
qctl data-review request --table orders --row-id 1001,1002 \
  --title "Fix Q3 amounts" --reviewer ada@example.com
```

```
data-review-group/11111111-2222-4333-8444-555555555555 created "Fix Q3 amounts": 2 review(s)
data-review/0190bbbb-1111-4222-8333-444444444444 row 1001 (status: open, approvals required: 2)
data-review/0190cccc-1111-4222-8333-555555555555 row 1002 (status: open, approvals required: 2) (re-submitted)
Next: qctl get data-reviews --table orders --group 11111111-2222-4333-8444-555555555555
```

Take row ids from `qctl get data orders --is-bad-data`: the backend does not
check them. `(re-submitted)` means the row's open review joined this
submission. `--reviewer` asks for a review; it grants no right to approve.

### 2. Watch it

```bash
qctl get data-review-groups --table orders
qctl data-review progress --table orders --group 11111111-2222-4333-8444-555555555555
```

```
rows: awaiting_decision 7 · rejected 2 · accepted 11  (scope: submission 11111111-2222-4333-8444-555555555555)
sign-off: done 14 of 20 (remaining 6) · you: no reviews assigned to you
```

Decisions arrive in your inbox as `data_review_decision`
(`qctl guide check-inbox`).

The groups listing shows each request's description and its reviewers: those
requested on submission and any added with `data-review-group assign`. Use `--columns id,title,description,reviewers` to focus on those.

### 3. Answer feedback

```bash
qctl describe data-review <review-id>
qctl rows set-cell orders --row-id 1001 --field amount --value 125.00 --yes
qctl data-review re-request <review-id>
```

Feedback arrives as comments. A cell can also carry a `reject` or
`request_changes` verdict recorded before the backend stopped accepting them;
your edit replaces that change and needs a fresh verdict. An edit never lifts
a whole-row `reject`: its author must `withdraw`, or you close the review and
submit again.

### 4. Manage reviewers, or cancel

```bash
qctl data-review assign <review-id> --reviewer grace@example.com
qctl data-review unassign <review-id> --reviewer grace@example.com
qctl data-review close <review-id> --yes
```

`close` is final, releases nothing, and leaves the row in quarantine.

To act on a whole request instead of one row, use `data-review-group` with
the request's id from `get data-review-groups`:

```bash
qctl data-review-group assign <group-id> --reviewer grace@example.com
qctl data-review-group re-request <group-id>
qctl data-review-group cancel <group-id> --yes
```

`assign` notifies only reviewers new to the request. `re-request` reminds its
reviewers who have not decided every open row. `cancel` removes every review
from the request and closes the open ones no other request shares; rows stay in
quarantine and nobody is notified.

## The reviewer loop

### 1. Find work

```bash
qctl get notifications --unread --type data_review_assigned --type data_review_group_assigned
qctl get data-reviews --table orders
qctl get data-review-changes --table orders
qctl data-review progress --table orders
```

Reviews are listed one table at a time. In `get data-reviews`, `approvals`
is the row's count against what it needs (`1/2`), not counting anyone who
edited or submitted it, and `held` names what keeps a row in quarantine, or
`releasable`.

To review one request's ranked changes, scope the listing with `--group` and
continue through pages of at most 20 with `--offset`:

```bash
qctl get data-review-changes --table orders --group 11111111-2222-4333-8444-555555555555
qctl get data-review-changes --table orders --group 11111111-2222-4333-8444-555555555555 --offset 20
```

The footer counts up to the request's submitted changes, including superseded
ones, so a continuation hint says more "may be available". A short or empty
page ends the listing. Rankings can move as reviews or row edits change.

### 2. Read one review

```bash
qctl describe data-review 0190bbbb-1111-4222-8333-444444444444
```

```
# DATA REVIEW 0190bbbb-1111-4222-8333-444444444444
Row: 1001 | Table: 42
Status: changes_requested | Approvals required: 2 | Requester: Rita Req
Reviewers: Alice Chen, Bob Ng
Held: 1 cell(s) need a fix (cell_veto)

## SUBMISSIONS (1)
[g: 11111111-2222-4333-8444-555555555555] "Fix Q3 amounts" — 2 days ago
  (no description)

## CHANGES (2)
- amount  [h: 811]
  before: "10" → after: "100"
  verdict: request_changes by Bob Ng · 2 hours ago
  comment: "too large"
- email  [h: 812]
  before: ∅ → after: "ada@example.com"
  verdict: undecided

## DECISIONS (1)
- Alice Chen: approve · first decided 1 hour ago
Approvals: 1 of 2 required (current eligible approvals)
...
## NEXT ACTIONS
- approve the row:         qctl data-review approve 0190bbbb-1111-4222-8333-444444444444
...
```

`[g: …]` is what `--group` takes, `[h: …]` what `--history-id` takes, and a
comment's `[c: …]` what `--reply-to` takes. An empty `CHANGES (0)` can also
mean you cannot read the table's data. The `Approvals` meter uses the same
backend count as `get data-reviews`, excluding stale approvals, every submitter
and every recorded editor, including undo and redo actors. Recorded decisions
remain visible even when they do not count. The live meter is `unavailable`
for an approved or closed review, or when its active overlay cannot be read;
a failed overlay read also warns on stderr.

### 3. Decide cells

```bash
qctl data-review accept-cell <review-id> --field amount
qctl data-review accept-cell <review-id> --history-id 812 --comment "confirmed with the customer"
qctl data-review clear-cell <review-id> --field amount
```

A verdict is stored per change, so every row carrying the same edit shares it.
`clear-cell` lifts a stored veto but does not approve. Nothing rejects a cell
or a row any more: leave it undecided and say why with `comment`.

### 4. Decide whole rows

```bash
qctl data-review approve <review-id> --comment "spot-checked" --yes
qctl get data-reviews --table orders --group 11111111-2222-4333-8444-555555555555
qctl data-review approve --group 11111111-2222-4333-8444-555555555555 --yes
```

```
data-review/0190bbbb-1111-4222-8333-444444444444 approved (status: approved, approvals: 2/2, released: 1 row(s))
Next: qctl describe data-review 0190bbbb-1111-4222-8333-444444444444
```

Preview a `--group` with `get data-reviews --group` first. An approval never
overrides a veto, and one from anyone who submitted the row or ever edited
it, undo and redo included, is recorded but never counts. A call that
skips any review exits non-zero although the rest committed; `-o json` prints
the `{processed, skipped}` split.

Under a policy that requires review, or with `quarantine_rows_until_approved`
on, `qctl rows release orders --row-id 1001` approves a quarantined row too, with or without a review, and releases it
once it has enough approvals. It skips rows you edited or submitted, prints
the rows still short (`1 of 2 approvals`), and exits 0 even when it releases
nothing.

### 5. Take a decision back, or discuss

```bash
qctl data-review withdraw <review-id> --yes
qctl data-review withdraw-cell <review-id> --field amount
qctl data-review comment <review-id> --body "amount looks off by 10x" --mention ada@example.com
```

`withdraw` removes only your whole-row decision. `withdraw-cell` deletes only
your current decision on the named change, including its comment and suggestion,
and recomputes the affected reviews. It takes no comment; another reviewer's
decision exits 4, and no standing decision exits 2. Withdrawing a stored cell veto
needs approve permission too.

The web UI's "Withdraw fix request" replaces the verdict with `comment_only`,
which is `clear-cell` in qctl; `withdraw-cell` deletes your own ledger decision.

A bare `@name` notifies nobody; use `--mention`. Edit or resolve comments with
`qctl comment-thread`.

## Next steps

- Every flag and gotcha: `qctl explain data-review --gotchas`, `qctl explain data-review-group --gotchas`
- Fixing quarantined values: `qctl guide edit-data`
- Reviews of rules rather than rows: `qctl guide review-rules`
