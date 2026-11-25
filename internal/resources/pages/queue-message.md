---
summary: one message on the platform's internal job queue, and the ops verbs that repair it
aliases:
kind:
schema: openapi:QueueSchema
guide:
signature: bf521da
---

## USAGE

    qctl describe queue-message <id>
    qctl get queue-messages
    qctl queue-message resurrect <id>
    qctl queue-message resurrect-dead
    qctl queue-message retire --older-than <interval>
    qctl queue-message retry <id> [<id>...]

## EXAMPLES

    # what is on the queue right now
    qctl get queue-messages

    # only the messages that have used up their retries
    qctl get queue-messages --dead

    # one queue, oldest first (the server sorts newest-first by default)
    qctl get queue-messages --queue CUEUE_INGESTION --order-by enqueued_at --reverse

    # only one table's messages
    qctl get queue-messages --table orders

    # one message in full, including the payload its producer enqueued
    qctl describe queue-message 42

    # retry one message -- works on ANY unacknowledged message, not only dead ones
    qctl queue-message retry 42

    # retry several
    qctl queue-message retry 42 43 44

    # revive a dead message now
    qctl queue-message resurrect 42

    # revive it, but hold it back for five minutes
    qctl queue-message resurrect 42 --backoff 300

    # see what is waiting, without touching anything
    qctl queue-message resurrect-dead --dry-list

    # see exactly what a one-hour window would revive
    qctl queue-message resurrect-dead --dry-list --older-than '1 hour'

    # revive everything idle for over an hour
    qctl queue-message resurrect-dead --older-than '1 hour'

    # revive at most 50, staggering redelivery by a minute
    qctl queue-message resurrect-dead --limit 50 --backoff 60 --yes

    # retire anything enqueued more than 90 days ago
    qctl queue-message retire --older-than '90 days'

    # take the oldest 100 only
    qctl queue-message retire --older-than '90 days' --limit 100

## GOTCHAS

- A watch cancels an HTTP poll in flight when its timeout or SIGINT/SIGTERM ends the watch, including a request waiting to retry; a successful response arriving after cancellation is discarded.

- Every route behind these commands requires the ops role: an admin or editor gets a 403, and is_ops on your user is an eligibility flag that grants nothing on its own. [backend]
- A message is listed only when the server can tie it to your organization: by its cloud source, or, for one keyed by a negative id on CUEUE_DATASET_LEARN, CUEUE_MIGRATION, CUEUE_TRAINING_JOB_COMBINER, CUEUE_COMPOSITE, CUEUE_RULE_VALIDATION or CUEUE_DELIVERABLE, by the table, profiling job, composite spec group or deliverable run it is for -- and a negative-id message's data_source_model_id comes back empty. One exception: the automatic retry of a failed group-scoped composite run is keyed by the failed run job's id, so it is also listed to, and reachable by every verb here from, any organization that owns a table with that id. A composite or deliverable message handed to a warm worker moves to CUEUE_WORKER with its negative id and is not listed while it sits there, including after the worker acknowledges it there. A message enqueued with no data_source_model_id at all, such as the CUEUE_KILL message that stops an earlier profiling job of a table when a newer one starts, belongs to no organization: it is never listed and no verb here can reach it. [backend]
- For a message keyed by a negative id, the dataset_id column and `--table` follow what the message is about, with one exception below. A composite message raised by a change to an upstream table lists under that table, not under the composite tables it invalidates. A group-scoped composite message lists under the composite table it rebuilds until it carries a composite run job id, which a warm-worker hand-off or a stale-worker recovery adds; from then on it has no table, and no deliverable render ever has one -- no `--table` finds those, so select them with `--queue`. The exception is the automatic retry of a failed group-scoped composite run, whose data carries `trigger.retry_of_composite_run_job_id`: it is keyed by the failed run job's id rather than a table's, so it lists under whichever table happens to have that id, if any, and only `--table` for that table finds it; select it with `--queue` and read its `target_group_ids` for the group it rebuilds. [backend]
- "Dead" means two different sets. The `--dead` filter and the `is_dead` field mean unacknowledged with the receive count at the ceiling; the candidate list behind `resurrect-dead` additionally requires the message to have been idle, so a message a consumer picked up seconds ago is dead in the listing and not a candidate for the batch verb. [backend]
- `retry` and `resurrect` are not the same verb with two names: retry resets ANY unacknowledged message and makes it deliverable immediately, while resurrect only touches one that has used up its retries and can delay redelivery with --backoff. [backend]
- `retry` reports no number because there is none: the route answers a flat "ok" and discards the UPDATE's rowcount, so an id that is acknowledged or belongs to another organization is a silent no-op. [backend]
- `resurrect` on a message that is not dead answers 500, not 400 or 404, so qctl reads the message first and refuses locally at exit 2 instead -- a pre-flight check, which a consumer acknowledging the message between the two calls can still race. [backend]
- `--older-than` is measured from a DIFFERENT column on the two bulk verbs: resurrect-dead measures idle time from the last delivery attempt (a message never delivered always qualifies), while retire measures age from when the message was enqueued and applies no idle condition at all. [backend]
- `retire` does not delete anything: it sets acknowledged = true and the rows stay readable under `--acknowledged`. What makes it final is that no route un-acknowledges a message, so retry and resurrect -- which both act only on unacknowledged messages -- can never reach it again. [backend]
- The queue's own cleanup of old acknowledged rows deletes a retired message's row only if its `dequeued_at` is set. That cleanup runs elsewhere in the platform, never from the retire call, and counts a retention window the deployment sets from `dequeued_at` -- the last delivery attempt, not the retire -- so a message last tried long ago can go at its next run. Re-publishing under a message's key can empty its `dequeued_at` without resetting the retry count, before a retire (which still takes a dead message left that way) or after one, and the cleanup never deletes a row whose `dequeued_at` is empty. [backend]
- `--dry-list` lists what the same command line would revive at that moment: it sends the run's `--older-than` and `--limit` to a route that applies the repair's own candidate rule, order and default window. Messages still enter and leave that set on their own before you run it. [backend]
- Left unset, `resurrect-dead --older-than` falls back to one platform-wide idle window for every queue, not each queue's own, so on a queue configured with a longer window -- the deliverable queue's is longer by default -- a message can be revived before its own window has run out. [backend]
- `--search` needs at least 3 characters; the backend silently ignores a shorter one and answers with the unfiltered listing, so qctl refuses it locally as bad usage instead. [backend]
- `--reverse` un-reverses. This endpoint defaults to reverse=true, so the listing arrives newest-first and `--reverse` is what gives you oldest-first. [backend]
- `retire --older-than` is required by qctl even though the server would default it to 30 days, because that is not a window anyone chose on the one verb whose effect nothing undoes.
- `--dry-list` and `--yes` together are refused (exit 2) rather than one winning silently: --dry-list changes nothing, so there is no confirmation for --yes to skip.
- `resurrect-dead --limit` defaults to 1000 and is sent to the preview route as well, whose own default is 100; without that the two would describe slices an order of magnitude apart.
- `resurrect-dead` prints the server's `{count, resurrected_ids}` under `-o json` and `-o yaml`, or with `--dry-list` the dead route's `{dead_ids, total_count}`, and `retire` its `{count, retired_ids}`; `-o name` prints `queue-message/<id>` for each of those ids, and the prose moves to stderr. `retry` and `resurrect` print prose only and exit 2 on `-o json`, `-o yaml` or `-o name`.
- `data` is never a default column in the listing -- it is an arbitrary JSON payload with no schema, and it differs per queue. Read it with `qctl describe queue-message <id>`.
- A queue message is identified by an integer primary key, and every command here rejects a non-numeric id locally as bad usage (exit 2), before config or credentials are loaded.
- `get queue-messages --watch` writes the page again only when it changes, as one stream: `-o json` writes one compact List per line, `-o yaml` one document after each `---`, `-o name` the `queue-message/<id>` of each message added, changed or gone from the page, and the table is redrawn in place on a terminal and appended elsewhere; after a poll retry notice, an HTTP retry notice under `--retries`, or verbose HTTP diagnostics under `-vvvvvvv` and above, the next changed table starts below it and later updates redraw there; a json line holds the whole page, so a large `--limit` can outgrow a line reader's buffer, such as the 64 KiB `bufio.Scanner` starts with.
- `get queue-messages --watch` stops after `--timeout`, a duration with a unit (`90s`, `10m`; `0` means no limit) -- a bare `60` is bad usage, unlike `--watch-interval`, which counts whole seconds. Left unset it is no limit, from a script or an agent as from a terminal, unless `QCTL_WATCH_TIMEOUT`, in the same syntax, sets a default; an explicit `--timeout` beats it. Running out of time is a clean end: exit 0. `--timeout` without `--watch` (even `--timeout 0`), a negative `--timeout`, and a malformed or negative `QCTL_WATCH_TIMEOUT` when no `--timeout` is given are bad usage (exit 2).
- `get queue-messages --watch` writes its own lines to stderr, so stdout carries only the pages: before the first poll one such as `watching queue messages every 5s, for up to 10m0s; resume with: qctl get queue-messages --timeout 10m0s --watch` under `--timeout 10m` (no `for up to` clause when there is no limit), then `timed out after 10m0s; resume with: …` when time runs out, or `interrupted; resume with: …` on a signal. The resume command repeats every flag you set, global ones such as `--org` and `--output` included, and `--timeout` too, so the resumed watch gets the same limit again.
- `get queue-messages --watch` ends on SIGINT (Ctrl-C) or SIGTERM by re-raising that signal, which a shell reports as exit 130 or 143. A failed first poll ends it with that failure's exit code. A later failure ends it with its own exit code only when retrying cannot help -- exit 2, 3, 4 or 5, which is any 4xx but a 429, or an incompatible API; any other, such as a 5xx, a 429 or a network failure, is written to stderr as `poll failed (<error>); retrying in <wait>` and retried, first after twice `--watch-interval`, doubling on each further failure up to 30s (or the interval itself when that is longer), and back on the interval once a poll succeeds.

## SEE ALSO

- `qctl explain ingestion-job`
- `qctl explain error-incident`
- `qctl explain table`
