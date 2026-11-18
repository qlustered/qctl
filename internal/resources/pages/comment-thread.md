---
summary: a discussion anchored to cells or rows of a table
aliases:
kind:
schema:
guide:
signature: 9e62c8a
---

## USAGE

    qctl comment-thread create
    qctl comment-thread delete-comment <thread-id> <comment-id>
    qctl comment-thread edit-comment <thread-id> <comment-id>
    qctl comment-thread reply <thread-id>
    qctl comment-thread resolve <thread-id>
    qctl comment-thread unresolve <thread-id>
    qctl describe comment-thread <thread-id>
    qctl get comment-threads [thread-id...]

## EXAMPLES

    # one page of readable unresolved threads on this table
    qctl get comment-threads --table orders

    # fetch another page, with the raw thread summaries and paging metadata
    qctl get comment-threads --table-id 42 --page 2 --limit 20 -o json

    # one thread's summary row, by id
    qctl get comment-threads --table orders 0190bbbb-1111-2222-3333-444444444444

    # several at once; --table-id is an ID where --table is a name
    qctl get comment-threads --table-id 42 <id-a> <id-b>

    # a List of the raw API responses, transcripts included
    qctl get comment-threads --table orders <id> -o yaml

    # the full transcript, with the [c: <uuid>] tags reply/edit-comment/delete-comment take
    qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table orders

    # the same thread as the raw API response
    qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table-id 42 -o json

    # open a thread on two cells of one row, with its first comment
    qctl comment-thread create --table orders --row 1042 --field amount --field currency --body "FX mismatch?"

    # no --field targets the whole rows instead
    qctl comment-thread create --table-id 42 --row 1042 --row 1043 --title "dupes" --body-file notes.md

    # add a comment to an existing thread
    qctl comment-thread reply 0190bbbb-1111-2222-3333-444444444444 --table orders --body "still wrong"

    # nest it under a comment, and notify someone who will not see it otherwise
    qctl comment-thread reply 0190bbbb-1111-2222-3333-444444444444 --table-id 42 --reply-to 0190cccc-1111-2222-3333-444444444444 --body "fixed" --mention alice@corp.com

    # replace the body of a comment you wrote
    qctl comment-thread edit-comment 0190bbbb-1111-2222-3333-444444444444 0190cccc-1111-2222-3333-444444444444 --table orders --body "fixed, PTAL"

    # delete one of your comments, unprompted
    qctl comment-thread delete-comment 0190bbbb-1111-2222-3333-444444444444 0190cccc-1111-2222-3333-444444444444 --table orders --yes

    # settle a thread — anyone with comment:write may, not just its author
    qctl comment-thread resolve 0190bbbb-1111-2222-3333-444444444444 --table orders

    # put it back, clearing resolved_at/by/reason
    qctl comment-thread unresolve 0190bbbb-1111-2222-3333-444444444444 --table-id 42

## GOTCHAS

- `get comment-threads` shows the duplicate group's UUID in `duplicate_group` (`-` for an ordinary thread); `describe comment-thread` names it as `duplicate-group/<uuid>` in the header. Read the group with `qctl describe duplicate-group <uuid>` and the same `--table` or `--table-id`.
- Name and email resolution for `--mention` reads all pages of the user directory, including users beyond the first 200.

- thread ids come from the context you are already in: `qctl get data <table> --include-comment-threads` for the rows on a page, a data review (`qctl describe data-review` and `qctl data-review comment` print its thread and table), the id `comment-thread create` prints, or a user flag — the `discussion: comment-thread/<uuid>` line `alert submit` and `warning submit` print (`results[].thread_id` under `-o json`), and the FLAGGED BY section of `describe alert` and `describe warning` (`user_submitted_issues[].thread_id`).
- without ids, `get comment-threads` lists one page of readable unresolved threads, most recently updated first; `--page` defaults to 1 and `--limit` to 20 (1-100). The listing includes targets and `comment_count`, with `comments: []`; external callers cannot see internal threads and see only readable threads and targets on rows of their assigned sources. Pass ids to fetch specific threads, including resolved ones, with their transcripts. [backend]
- `--page` and `--limit` are refused with thread ids (exit 2). Listing output under `-o json|yaml` is `{kind: List, items, page, limit, total, next_page}`; by-id output uses the same envelope with null paging. Table and name output put the next-page hint on stderr.
- the by-rows overlay reports UNRESOLVED threads only, so a resolved thread never appears in it and can be reached only by its id. [backend]
- the overlay carries each thread's title but `get data`'s Comment Threads column drops it, printing `<thread-id> [<comments>] <targets>` — read a title with `get comment-threads <id>` or `describe comment-thread <id>`.
- a thread 404 can mean no such thread in this table, a thread hidden from an external caller because it is internal or outside that caller's source access, or a rule review's discussion, which these routes refuse; read rule-review discussions with `qctl describe rule-review <review-id>`. [backend]
- a thread is created WITH its first comment: there is no empty thread and no create-then-comment two-step, which is why `create` refuses to run without `--body` or `--body-file`. [backend]
- `alert submit` and `warning submit` open a thread too: one per submit, whose first comment is the flag's `--comment`, authored by the flagger and targeting every flagged cell of the rows that submit newly flagged. It is an ordinary thread — reply, resolve and `edit-comment` work on it as on any other — but editing that first comment changes the discussion only, never the alert's or warning's message, and retracting the flag leaves the thread as it is. [backend]
- `create` is a partial success: targets are validated one at a time, the thread is built from the ones that pass, and the dropped cells are reported on stderr at exit 0 with a reason (`row_not_found`, `reserved_field`, `unknown_field`) — a request whose targets ALL fail is a 422 that creates nothing. [backend]
- under `-o json` and `-o yaml`, `create` prints `{thread, dropped_targets}` and `resolve` and `unresolve` the thread, each with `comment-thread/<uuid>` under `-o name`; `reply` prints the created comment, whose `id` is what `--reply-to` and `edit-comment` take, and `edit-comment` the edited one, and both exit 2 on `-o name`. Each moves its result line to stderr under those formats. `delete-comment` prints prose only and exits 2 on `-o json`, `-o yaml` or `-o name`.
- the caps are the backend's — 500 target rows, 2000 targeted cells, a 1-10000 character body, a 500-character title — and qctl refuses all four client-side (exit 2) rather than letting them come back as a 422. [backend]
- `create` targets are the cross product of `--row` and `--field` — every named field on every named row; the wire also allows a different field set per row and qctl deliberately does not expose that, so a heterogeneous thread means one `create` per shape. [backend]
- whole-row and per-cell targets are independent: `whole-row` in the FIELDS column is a target in its own right, not the absence of named fields, and neither may be inferred from the other being empty. [backend]
- the thread routes are dataset-scoped, so `--table <name>` or `--table-id <id>` is always required; neither flag accepts the other's value, and passing both is an error rather than a precedence rule.
- thread and comment ids are never prefix-resolved: pass the full UUID. A typo is a usage error (exit 2) and costs no API call.
- the 403 hints on replies, comment edits/deletions and thread resolutions/reopenings name permission and table/source access requirements; editing/deleting also name authorship, and resolving/reopening require external callers to have every target row on their assigned sources.
- `reply` adds `--reply-to` advice only when the server identifies that parent comment as missing; mention refusals and other validation failures retain their own diagnostic at exit 2.
- replies nest to depth 3 (the same `MAX_THREAD_DEPTH` rule reviews use), and a deeper `--reply-to` is refused with `COMMENT_DEPTH_EXCEEDED` — reply to the parent comment one level up instead. [backend]
- a bare `@alice` in a body notifies nobody: only a structured `[@label](mention:<uuid>)` link does, wherever it is written — a `create` body, a `reply`, an `edit-comment` that adds it, or the `--comment` of `alert submit` and `warning submit` or of `rows send-to-quarantine` and `rows reprocess-clean` given `--row-id`, each of which opens a thread with it as the first comment (on a `--shared` table that comment may mention no one). qctl writes the link for you only on `reply`, whose `--mention` (email, "First Last", or UUID) appends it on a trailing `cc` line; anywhere else type it yourself, or `create` then `reply --mention`. For an author who is not `external` the backend drops a mentioned UUID that is not an active member of the organization; an `external` author's comment is refused with 422 (exit 2) when it mentions anyone but an active non-external member who flagged rows of the author's sources in that table or commented in one of its threads the author can read. The backend never notifies you of your own mention. [backend]
- `reply --mention` reads the org user directory only to resolve an email or a "First Last" name or to label a UUID; that read needs `org:users:list`, which the `editor` and `external` roles lack although both may comment, so when it is refused qctl reads the table's mentionable users instead: the active members, other than `external` ones, who flagged rows of the table or commented in its threads — for an `external` caller only flags on rows of its sources and threads it can read count, which makes the list exactly whom it may mention. A name or an email outside that list exits 2; a full UUID resolves without either read, and its `cc` label falls back to the id, after one warning on stderr, only when both reads fail — who gets notified is unaffected. [backend]
- `reply` has no `--dry-run-job` on purpose: the backend accepts `linked_dry_run_job_id` on these routes and silently discards it, because dry-run jobs belong to the rule-review arc. [backend]
- only a comment's author may edit or delete it — an org admin cannot, and there is no time window; anyone else gets a 403 (exit 4). [backend]
- `resolve` and `unresolve` answer with the thread's STATE, not its transcript — the response carries an empty `comments` list with a real `comment_count` — so they print one line and the transcript is re-read with `describe comment-thread`. [backend]
- resolving is not author-only: anyone with `comment:write` on the table may resolve or reopen any thread of it, which is the opposite of the rule for editing and deleting a comment; an external caller may do so only on a thread whose target rows are all on its assigned sources, and gets a 403 when it also covers other rows. [backend]
- a thread can turn up resolved with nobody having touched it: a backend sweep stamps `resolved_reason: row_gone` once every row it targets is gone, and keyless mirror, composite recompute and append-mode re-upload all re-mint row ids. [backend]
- `describe comment-thread` prints that reason on its Status line, which is the only place the difference between a manual resolution and `row_gone` is visible.
- `unresolve` reopens a `row_gone` thread the same as a manual one, but that only makes it a sweep candidate again — while every row it targets is still gone, the next sweep of the table re-resolves it. [backend]
- `delete-comment` deletes a COMMENT, never the thread: a leaf comment is removed outright, one with replies is tombstoned with its body rewritten to `[comment deleted]`, and there is no delete-thread route at all. [backend]
- `qctl get data <table> --comment-thread-id <uuid>` filters the data to the rows a thread targets, and still returns them after the thread is resolved. [backend]

## SEE ALSO

- `qctl explain data`
- `qctl explain data-review`
- `qctl explain rule-review`
- `qctl guide inspect-data`
