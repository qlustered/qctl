package rows

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

// deleteQuarantinedSupport is the backend's matrix, not a qctl preference: the
// delete routes identify rows by signature at every scope, and only the dataset
// route accepts quarantine_status or the delete-all-bad-rows shortcut.
var deleteQuarantinedSupport = support{
	table:     []selectorKind{selSignatures, selStatus, selAll},
	cloud:     []selectorKind{selSignatures, selAll},
	file:      []selectorKind{selSignatures},
	isBadData: true,
	rowIDs:    rowIDsRefused,
	// Both delete-all-bad-rows routes name the quarantine table by
	// construction, and this states that where the prompt can read it.
	all: allQuarantined,
}

func newDeleteQuarantinedCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-quarantined [table-id-or-name]",
		Short: "Delete quarantined rows; --signature also keeps them from loading again",
		Long: `Delete rows from the quarantine table.

  qctl rows delete-quarantined orders --signature 9f2a... --reason "duplicate upload"
  qctl rows delete-quarantined orders --status failed_validation --reason "bad export"
  qctl rows delete-quarantined orders --all --reason "bad export"
  qctl rows delete-quarantined orders --cloud-source-id 7 --all --reason "bad export"

--status failed_validation affects MORE rows than the matching 'qctl get data'
filter or 'qctl describe table' count shows: its bulk filter is "FALSE or NULL"
while every read path is "FALSE". qctl therefore states no number BEFORE YOU
CONFIRM for it, and none for --all; the count printed afterwards is the
server's own.

Rows are named by SIGNATURE, never by id.

What a delete keeps from loading again depends on the selector:

  --signature      writes an exclusion entry for each deleted line to the
                   table's, cloud source's or file's exclusion list (a
                   table-series source writes the table's), scoped to the
                   line's source and identity (the line itself, or its key
                   on a keyed table), so future ingests from that source
                   drop a line with the same identity. A line whose identity
                   a live clean row of the same source shares is deleted but
                   not excluded.
  --status, --all  exclude nothing: the rows are deleted with the reason and
                   load again through the rules if the sender re-sends them.

Signatures that match no quarantined row in scope are listed on stderr; when
none match, the command exits 3. A --signature delete that names some but not
all live copies of a repeated key is refused before anything is deleted, and
the error lists every copy. --status and --all leave such a group in place and
list it on stderr.

Clean rows cannot be deleted: the server refuses a clean-table delete with
CLEAN_ROWS_IMMUTABLE. Move a clean row to quarantine first (it keeps its id),
then delete it here by the initial_signature_ the quarantine listing shows:

  qctl rows send-to-quarantine orders --row-id 1042
  qctl get data orders --is-bad-data --filter-by-field-name id --filter-by-field-value 1042 -o json
  qctl rows delete-quarantined orders --signature 9f2a... --reason "test rows"` + deleteReasonDoc + deleteOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDelete(cmd, firstArg(args), deleteQuarantinedSupport,
				action{verb: "delete-quarantined", gerund: "Delete"})
		},
	}
	addScopeFlags(cmd, deleteQuarantinedSupport)
	addSelectorFlags(cmd, deleteQuarantinedSupport, selRowIDs, selSignatures, selStatus, selAll)
	addReasonFlag(cmd)
	addYesFlag(cmd)
	return cmd
}

// maxDeleteReasonLength is the backend's limit on a delete comment, in
// characters.
const maxDeleteReasonLength = 10000

const deleteReasonDoc = `

--reason is required: it is sent as the delete's comment and recorded with
who deleted which rows, which 'qctl get deleted-rows' lists. It must not be
blank and is at most 10000 characters.`

func addReasonFlag(cmd *cobra.Command) {
	cmd.Flags().String("reason", "", "Why the rows are deleted (required), recorded with who deleted them")
}

// deleteReason returns --reason as given, refusing a missing, blank or
// over-long value with exit 2.
func deleteReason(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("reason") {
		return "", qerrors.NewBadUsage("--reason is required: say why the rows are deleted")
	}
	r, _ := cmd.Flags().GetString("reason")
	if strings.TrimSpace(r) == "" {
		return "", qerrors.NewBadUsage("--reason cannot be blank")
	}
	if n := utf8.RuneCountInString(r); n > maxDeleteReasonLength {
		return "", qerrors.NewBadUsage(fmt.Sprintf("--reason is %d characters; the limit is %d", n, maxDeleteReasonLength))
	}
	return r, nil
}

// runDelete is the delete verb. sup.isBadData is sent as the routes'
// is_bad_data parameter.
//
// The prompt's consequence lines are chosen from the resolved selector.
func runDelete(cmd *cobra.Command, tableArg string, sup support, act action) error {
	var reason string
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, sup, act.verb, func(c *cobra.Command) error {
		var rerr error
		reason, rerr = deleteReason(c)
		return rerr
	})
	if err != nil {
		return err
	}
	act.extra, err = deleteConsequences(act.verb, sel, sc)
	if err != nil {
		return err
	}
	if err := confirmAction(cmd, ctx, sc, sel, sup, act); err != nil {
		return err
	}

	res, err := deleteRows(ctx, sc, sel, sup.isBadData, act.verb, reason)
	if err != nil {
		return explainDeleteError(err, sc)
	}
	return reportDeleteResult(cmd, sc, sel, sup, act, res)
}

// deleteRows picks the route and sends reason as the delete's comment. Each
// scope has its own client; one function per scope keeps each readable on its
// own.
//
// EVERY switch here and below has named arms and a refusing default, this one
// included. A `default:` that WAS one of the routes is the shape this group
// exists to avoid: it makes a fourth scopeKind, or a selector a later support{}
// edit adds, into a silent wrong route rather than a visible error.
func deleteRows(ctx *cmdutil.CommandContext, sc scope, sel selector, isBadData bool, verb, reason string) (*deleteResult, error) {
	switch sc.kind {
	case scopeFile:
		return deleteRowsInFile(ctx, sc, sel, isBadData, reason)
	case scopeCloudSource:
		return deleteRowsInCloudSource(ctx, sc, sel, isBadData, verb, reason)
	case scopeTable:
		return deleteRowsInTable(ctx, sc, sel, isBadData, verb, reason)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}

// deleteRowsInFile has ONE route and one selector, so it needs no switch:
// resolveSelector has already refused everything but --signature at file scope
// for delete-quarantined, and no all-bad-rows route exists per stored item.
func deleteRowsInFile(ctx *cmdutil.CommandContext, sc scope, sel selector, isBadData bool, reason string) (*deleteResult, error) {
	siClient, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create files client: %w", err)
	}
	siClient = siClient.WithThirdParty(sc.thirdParty)
	// dataset_id is the selected table; a server that checks it refuses a
	// file that belongs to another table.
	tableID := sc.tableID
	return siClient.DeleteRows(ctx.Credential.AccessToken, sc.fileID, sel.signatures, isBadData, &tableID, reason)
}

// deleteRowsInCloudSource is cased on sel.kind like the table form below, and
// for the same reason: as an `if selAll {} else {}` the else arm was every
// OTHER selector, so a --status or --row-id that a support{} edit let through
// would have gone out as a signature delete carrying an EMPTY signature list.
func deleteRowsInCloudSource(ctx *cmdutil.CommandContext, sc scope, sel selector, isBadData bool, verb, reason string) (*deleteResult, error) {
	csClient, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud sources client: %w", err)
	}
	csClient = csClient.WithThirdParty(sc.thirdParty)
	token := ctx.Credential.AccessToken
	var r *cloud_sources.DeleteResult
	switch sel.kind {
	case selAll:
		if err := refuseAllBadRows(isBadData, verb, sel, sc); err != nil {
			return nil, err
		}
		r, err = csClient.DeleteAllBadRows(token, sc.cloudSourceID, sc.cloudSourceTableID, reason)
	case selSignatures:
		r, err = csClient.DeleteRows(token, sc.cloudSourceID, sel.signatures, isBadData, sc.cloudSourceTableID, reason)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// deleteRowsInTable is the only scope with all three routes.
//
// Named arms and a refusing default, for the reason spelled out in
// reprocessRows: a fall-through here would send an empty signature list with no
// status, which is a bulk route carrying no selector at all.
func deleteRowsInTable(ctx *cmdutil.CommandContext, sc scope, sel selector, isBadData bool, verb, reason string) (*deleteResult, error) {
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	token := ctx.Credential.AccessToken
	var r *datasets.DeleteResult
	var err error
	switch sel.kind {
	case selAll:
		if err := refuseAllBadRows(isBadData, verb, sel, sc); err != nil {
			return nil, err
		}
		r, err = dsClient.DeleteAllBadRows(token, sc.tableID, reason)
	case selStatus:
		// DeleteRows takes *QuarantineStatus, where nil is the "no status
		// filter" form, so the status needs an address to be sent at all.
		st := sel.status
		r, err = dsClient.DeleteRows(token, sc.tableID, nil, isBadData, &st, reason)
	case selSignatures:
		r, err = dsClient.DeleteRows(token, sc.tableID, sel.signatures, isBadData, nil, reason)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// refuseAllBadRows rejects an all-quarantined selector when isBadData is false.
func refuseAllBadRows(isBadData bool, verb string, sel selector, sc scope) error {
	if isBadData {
		return nil
	}
	return unroutableSelector(verb, sel, sc)
}
