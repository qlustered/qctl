---
summary: a person assigned to send files to a cloud source's email intake
aliases: [email-senders]
kind:
schema: openapi:EmailIntakeSenderSchema
guide:
signature: 4743232
---

## USAGE

    qctl email-sender add <email> --cloud-source-id <cloud-source-id> --first-name <first-name> --last-name <last-name>
    qctl email-sender remove <email|user-id|email-sender/<user-id>> --cloud-source-id <cloud-source-id>
    qctl email-sender resend-welcome <email|user-id|email-sender/<user-id>> --cloud-source-id <cloud-source-id>
    qctl get email-senders --cloud-source-id <cloud-source-id>

## EXAMPLES

    # who may send files to a cloud source's email intake, and whether mail to each is delivered
    qctl get email-senders --cloud-source-id 42

    # why delivery is suppressed, and the user ids remove and resend-welcome take
    qctl get email-senders --cloud-source-id cloud-source/42 --columns email,delivery,reason,user_id

    # machine-readable sender records
    qctl get email-senders --cloud-source-id 42 -o json

    # the welcome email a new sender would get; adds nobody and sends nothing
    qctl email-sender add carol@example.com --first-name Carol --last-name Smith --cloud-source-id 42 --dry-run

    # assign the sender and schedule their welcome email
    qctl email-sender add carol@example.com --first-name Carol --last-name Smith --cloud-source-id 42

    # schedule another welcome email, whether or not they had one
    qctl email-sender resend-welcome carol@example.com --cloud-source-id 42

    # revoke one sender's assignment; their account is kept
    qctl email-sender remove carol@example.com --cloud-source-id 42 --yes

    # the same by the reference get email-senders -o name prints
    qctl email-sender remove email-sender/0190cccc-0000-4000-8000-000000000001 --cloud-source-id 42 --yes

    # turn the intake on, which mails each sender its address (see qctl explain cloud-source)
    qctl enable email-intake 42

## GOTCHAS

- Name a sender by email address (matched case-insensitively), user id, or `email-sender/<user-id>` as `get email-senders -o name` prints it. `remove` and `resend-welcome` look the sender up in the source's sender listing first: one not in it exits 3 with no write, and a malformed user id exits 2. `add` takes the email address only. `--cloud-source-id` is required on every command and takes an id or `cloud-source/<id>`.
- `get email-senders` lists only the source's active external senders, never an operations user, ordered by email, so an assignment left on an account that is no longer an active external member is not listed and `remove` cannot reach it. DELIVERY is `ok` or `suppressed`, and SINCE is when it was suppressed; `--columns reason` adds why (`hard_bounce`, `complaint` or `repeated_soft_bounce`). [backend]
- Every sender route refuses an external user with 403 (exit 4). `add`, `add --dry-run`, `remove` and `resend-welcome` need `org:users:external:data_sources:manage`, the listing needs `source:read`, and none of them is subject to the table's change review policy. [backend]
- `email-sender add` creates an external user for a new address or reuses the existing account, which keeps the first and last names it already has (qctl warns on stderr when they differ from those given) and its assignments to other sources; the server trims and lowercases the address. It answers 409 (exit 2) for an operations user's address or an account whose membership in the organization is not an active external one, and 400 (exit 2) when the person is already assigned to another source of the same table series. On a dev or sandbox server it first refuses with 422 (exit 2), adding nothing, an address that is neither on its `MAIL_DEV_ALLOWED_RECIPIENTS` nor an SES simulator address. [backend]
- A sender gets one of two welcome emails: the portal variant, with no address, when added while intake is off, and the email variant, with the address, when added while it is on or when `enable email-intake` turns it on. Each variant is scheduled at most once per sender and source, and that record survives `email-sender remove`, so removing and re-adding a sender sends nothing new (`add` reports `already_scheduled`); `resend-welcome` is the way to send one again. [backend]
- `add` reports what became of the welcome: `scheduled` (which does not confirm delivery), `already_scheduled` (this person already had this variant from this source, so nothing was sent), `source_inactive` (the source is not `active`, so nothing was sent) or `recipient_refused` (mail to the address is refused, with the reason). The sender is added in all four; qctl exits 0 and warns on stderr for all but `scheduled`. [backend]
- `email-sender remove` revokes this source's assignment only: the account, its organization membership and its assignments to other sources are kept. [backend]
- `email-sender resend-welcome` schedules a new welcome on every run, whether or not the sender had one, in the variant for the intake's current state. A source that is not `active` answers 409 (exit 2), and so does a sender who cannot be emailed (409 `INTAKE_WELCOME_RECIPIENT_REFUSED`, `context.reason` says why); on a dev or sandbox server that includes an address that is neither on its `MAIL_DEV_ALLOWED_RECIPIENTS` nor an SES simulator address. It prints the outbound email id; scheduling does not confirm delivery. [backend]
- `add --dry-run` renders the welcome for the address and adds nobody, but does not check that the address can be added: an operations user's address, or one whose account is not an active external member here, previews fine and the real add answers 409. On a source that is not `active` the preview answers 409 (exit 2), while the real add succeeds as `source_inactive` and sends nothing. [backend]
- `add --dry-run` still requires `--first-name` and `--last-name`, so deleting `--dry-run` gives the real command. It prints the subject and the text body, or `{subject, html, text}` under `-o json|yaml`, and refuses `-o name` (exit 2). A blank email or name exits 2 before any request.
- `remove` prompts unless `--yes` and honors protected contexts, so `--yes` does not replace `--context`; `add` and `resend-welcome` never prompt. `-o json|yaml` emits a List of the sender records for `get email-senders`, the sender with its `welcome` outcome for `add`, `{outbound_email_id}` for `resend-welcome` and `{result: removed, cloud_source_id, user_id, email}` for `remove`; `-o name` prints `email-sender/<user-id>`.

## SEE ALSO

- `qctl explain cloud-source`
