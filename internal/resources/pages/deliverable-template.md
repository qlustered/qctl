---
summary: the .xlsx or .xlsm workbook a deliverable revision's bindings fill
aliases:
kind:
schema: openapi:SkeletonFinalizeResponse
guide: deliverables
signature: 2cbe168
---

## USAGE

    qctl upload deliverable-template [<file>]

## EXAMPLES

    # upload a template to the organization without attaching it
    qctl upload deliverable-template bordereau.xlsx

    # attach a template that keeps its macros to the deliverable's only draft
    qctl upload deliverable-template bordereau.xlsm --deliverable monthly-bordereau

    # replace a named draft release's template and list the bindings that moved
    qctl upload deliverable-template bordereau.xlsx --deliverable monthly-bordereau --release 2026.2

    # accept the losses a refusal listed, without uploading again
    qctl upload deliverable-template --template-id 42 --acknowledge-lossy

    # attach an uploaded lossy template to the only draft, accepting its losses again
    qctl upload deliverable-template --template-id 42 --deliverable monthly-bordereau --acknowledge-lossy

    # print only the template's reference
    qctl upload deliverable-template bordereau.xlsx -o name

    # keep the server's finalize response
    qctl upload deliverable-template bordereau.xlsx --deliverable monthly-bordereau -o json

    # see which template a draft uses, with its inventory
    qctl describe deliverable-revision monthly-bordereau --release 2026.2

## GOTCHAS

- A 403 from a deliverable API request keeps the server message and exits 4, with the permission that operation requires, the roles that hold it and `qctl auth me` to check your role. The added advice describes the endpoint requirement; the server message explains the refusal.
- UI words: the deliverable wizard's "Template" step and its "Replace template" button upload a template as this command does, and its "I understand — generated files will differ in these parts" checkbox is `--acknowledge-lossy`; the API's "skeleton" means template. A template is named by its integer id: `-o name` prints `deliverable-template/<id>`, and `--template-id` takes either form.
- The server takes a template only by a name ending in `.xlsx` or `.xlsm`, in any case, and that extension sets its VBA setting: a `.xlsm` template keeps its macros in generated files, and a `.xlsx` template keeps none. [backend]
- qctl checks the flags and the file before authenticating: a name not ending in `.xlsx` or `.xlsm`, a directory, both or neither of `<file>` and `--template-id`, a blank `--deliverable`, an empty `--release`, a `--revision` that is not a full UUID, both selectors, or either selector without `--deliverable` exits 2, and a file it cannot access exits 1.
- Only a draft revision's template can change. The server refuses an enabled or disabled revision with 409 and no error code; qctl refuses one with exit 2 before uploading anything. [backend]
- The upload answers 409 with no error code (exit 2), before anything is stored, when the organization has no backup settings, which hold template files. [backend]
- Finalizing inspects the workbook, and a template whose generated files would lose parts of it is refused unless `--acknowledge-lossy` is given (exit 2, each loss listed). The refusal still saves the inspection and attaches nothing, so the `--template-id <id> --acknowledge-lossy` line it prints finalizes the template again from that inspection, with no upload. [backend]
- The acknowledgement is recorded on the revision a template is attached to, not on the template, so a lossy template uploaded without `--deliverable` needs `--acknowledge-lossy` again when it is attached; the `Next:` line includes it. [backend]
- Finalizing hashes the workbook (sha256), and an identical template the organization already finalized is reused: the output names that template's id, says `Reused existing template: true`, and keeps that template's VBA setting whatever the new file's extension, with a `Note:` when they differ. The template the upload created stays behind, unattached and unfinalized, and so does its stored file. [backend]
- When finalizing fails with exit 1 after the upload, such as on a 5xx, qctl prints the `--template-id` line that finalizes the uploaded file again without uploading it. A file that is not a usable workbook is refused with 422 (exit 2) and no such line: it stays unfinalized, and finalizing it again reads the same bytes, so upload a corrected file instead. [backend]
- No route lists templates, or reads or downloads one by its id, so qctl has no `get` or `describe` for one: `get deliverable-revisions` and `describe deliverable-revision` show the template id each revision uses, and describe shows the attached template's inventory and, with `--sheet`, its rows. `--template-id <id>` without `--deliverable` prints a finalized template's saved inspection again and changes nothing (a lossy one only with `--acknowledge-lossy`). [backend]
- `--template-id <id> --deliverable <ref>` attaches any template of the organization to the draft without uploading, including one another revision or deliverable already uses; a template not finalized yet is finalized first. [backend]
- Attaching a different template to a draft that already has one re-anchors its column and cell bindings by their labels, and qctl prints each one not left unchanged: `moved` follows its label to the new position; `broken` stays where it was, with the reason `label_missing`, `label_ambiguous`, `target_taken` or `target_out_of_range`; a binding with no label stays and is flagged `needs_confirmation`. Attaching to a draft with no template, or the same template again, re-anchors nothing. [backend]
- Unlike the web app's "Replace template", qctl does not change the deliverable's `filename_pattern` to the new extension. A download's name ends in `.xlsx` or `.xlsm` by the template of the revision its run rendered: an extension ending the pattern is replaced by the template's. [backend]
- The table view prints the template's id, signature, VBA setting, reuse flag, sheet inventory and fidelity losses, then any re-anchored bindings and a `Next:` line. `-o json|yaml` print the server's finalize response and `-o name` prints `deliverable-template/<id>`; under those three the prose goes to stderr.

## SEE ALSO

- `qctl explain deliverable-revision`
- `qctl explain deliverable`
