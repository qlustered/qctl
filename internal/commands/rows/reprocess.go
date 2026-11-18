package rows

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// reprocessSupport: reprocess-bad-rows takes ids or quarantine_status at
// dataset scope; at cloud-source scope only the reprocess-all-bad-rows route
// exists, so --all is the sole selector there (spec §4.4). File scope has no
// reprocess route at all, which is why sup.file stays nil.
//
// 'rows reprocess-clean' is NOT here: it is the move-rows-to-quarantine route,
// so it lives in quarantine.go beside send-to-quarantine, the verb it differs
// from by one wire value.
var reprocessSupport = support{
	table: []selectorKind{selRowIDs, selStatus, selAll},
	cloud: []selectorKind{selAll},
	// reprocess_bad_rows_core reaches
	// nullify_alert_id_and_mark_bad_rows_for_processing, which is an
	// `UPDATE {bad_rows_table}` on "the bad row ids that are passed": these are
	// QUARANTINE ids, and clean-table ids silently match nothing.
	rowIDs: rowIDsQuarantined,
	// The --status form appends `awaiting_processing_ IS NOT TRUE`, which
	// GetQuarantineCountByStatus does not apply — so no honest number exists
	// for --status passed_validation here. See support.statusSkipsQueuedRows.
	statusSkipsQueuedRows: true,
	// reprocess-all-bad-rows takes no ids and no status: the route names the
	// quarantine table and is the whole selector.
	all: allQuarantined,
	// isBadData is left at its zero value because neither of this verb's routes
	// has an is_bad_data parameter at all — see support.isBadData. Stated on
	// the support row and not at the call site, which is where the two move
	// verbs state theirs (quarantine.go) and where the field is declared.
}

func newReprocessQuarantinedCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reprocess-quarantined [table-id-or-name]",
		Short: "Send quarantined rows back through validation",
		Long: `Send quarantined rows back through validation.

  qctl rows reprocess-quarantined orders --row-id 1042
  qctl rows reprocess-quarantined orders --status failed_validation
  qctl rows reprocess-quarantined orders --all
  qctl rows reprocess-quarantined orders --cloud-source-id 7 --all

Row ids come from the QUARANTINE listing, 'qctl get data <table> --is-bad-data'.
Ids read from the clean listing match nothing, and the route still answers ok.

--status skips rows already queued for processing; --row-id and --all do not.

At cloud-source scope only --all is available: the all-bad-rows route IS the
per-source form there, and no narrower route exists.

For rows in the CLEAN table use 'qctl rows reprocess-clean'.` + affectedRowsOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReprocessQuarantined(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, reprocessSupport)
	addSelectorFlags(cmd, reprocessSupport, selRowIDs, selStatus, selAll)
	addYesFlag(cmd)
	return cmd
}

func runReprocessQuarantined(cmd *cobra.Command, tableArg string) error {
	act := action{
		verb:   "reprocess-quarantined",
		gerund: "Reprocess",
		extra:  []string{"Reprocessed rows leave quarantine only if they pass; nothing in the response says which did."},
	}
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, reprocessSupport, act.verb)
	if err != nil {
		return err
	}
	// No act.extra line for the skipped-queue fact, and that is not an
	// omission. confirmAction prints it already, on BOTH paths and in exactly
	// this case: unsizedClause's statusSkipsQueuedRows arm says "a --status
	// reprocess skips rows already queued for processing, …", and the prompt
	// path reaches it through unsizedReason while --yes reaches it through
	// skippedPromptReason. It is unconditional here because --status is
	// table-only for this verb (reprocessSupport.cloud is []{selAll}) and that
	// arm fires for every --status at table scope.
	//
	// An act.extra line saying the same thing printed one line above the
	// reason clause, in both forms of the same sentence, on every run. The
	// clause is the one that stays: it also explains what the duplicate did
	// not, namely why no number is shown.
	if err := confirmAction(cmd, ctx, sc, sel, reprocessSupport, act); err != nil {
		return err
	}

	res, err := reprocessRows(ctx, sc, sel, act.verb)
	if err != nil {
		return err
	}
	// getDataLine, never a hand-built line: at cloud-source scope it adds the
	// --data-source-model-id narrowing this run acted within, so the user is
	// not sent to read back the whole table's quarantine.
	//
	// The literal true is this verb's single declaration of its population, and
	// getDataLine's doc records it as such. There is nothing on the wire for it
	// to contradict: why support.isBadData is not the source here is recorded
	// on reprocessSupport, beside the field it is about.
	return reportResult(cmd, sc, sel, reprocessSupport, act, res, sc.getDataLine(true))
}

// reprocessRows picks the route. Cloud-source scope has exactly one —
// reprocess-all-bad-rows, which passes limit_to_data_source_model_id and so IS
// the scoped form there; resolveSelector has already refused everything else.
//
// The SCOPE dispatch is a switch with named arms and a refusing default, for
// the same reason deleteRows' is. It was `if sc.kind == scopeCloudSource { … }`
// falling THROUGH to the dataset route, which made a fourth scopeKind reprocess
// the whole table's quarantine instead of the narrower scope it named.
//
// verb is THREADED rather than written out, as deleteRows threads it. The three
// refusing defaults below spelled "reprocess-quarantined" as a literal, which is
// correct while this function has one caller and silently mis-names the verb in
// its own bug report the moment a second one arrives — the name-based-source
// defect that rowIDPopulation, allPopulation and support.statusSkipsQueuedRows
// each exist to keep out of a user-facing message.
func reprocessRows(ctx *cmdutil.CommandContext, sc scope, sel selector, verb string) (*bulkResult, error) {
	switch sc.kind {
	case scopeCloudSource:
		return reprocessRowsInCloudSource(ctx, sc, sel, verb)
	case scopeTable:
		return reprocessRowsInTable(ctx, sc, sel, verb)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}

// reprocessRowsInCloudSource reprocesses all quarantined source rows in the selected table.
func reprocessRowsInCloudSource(ctx *cmdutil.CommandContext, sc scope, sel selector, verb string) (*bulkResult, error) {
	if sel.kind != selAll {
		return nil, unroutableSelector(verb, sel, sc)
	}
	csClient, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud sources client: %w", err)
	}
	csClient = csClient.WithThirdParty(sc.thirdParty)
	r, err := csClient.ReprocessAllBadRows(ctx.Credential.AccessToken, sc.cloudSourceID, sc.cloudSourceTableID)
	if err != nil {
		return nil, err
	}
	return &bulkResult{AffectedRows: r.AffectedRows}, nil
}

// reprocessRowsInTable is the scope with all three routes.
func reprocessRowsInTable(ctx *cmdutil.CommandContext, sc scope, sel selector, verb string) (*bulkResult, error) {
	token := ctx.Credential.AccessToken
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	var r *datasets.BulkResult
	var err error
	// Every arm is named and the default REFUSES. A default that fell through
	// to ReprocessBadRows would send {"ids":[]} with no status on any selector
	// a later support{} edit added.
	//
	// That does NOT reach a WHERE-less UPDATE: the view refuses it first, with
	// `if not body.ids and quarantine_status is None: raise APIError(422, "Bad
	// Request. Either provide ids or a quarantine_status filter.")` (atlas
	// reprocess_bad_rows_for_dataset_view, data_view.py), before
	// reprocess_bad_rows_core is called at all.
	// The controller-level derivation still holds one layer down — empty
	// `filters` yields `where = ""` and
	// `UPDATE {bad_rows_table} SET awaiting_processing_ = TRUE` over the whole
	// table — but nothing qctl can send gets there.
	//
	// So this default is not the last line of defence; it is what keeps the
	// failure a legible client-side qctl error instead of a round trip and a
	// 422 about a request the user never typed. See unroutableSelector.
	// Unreachable today; one line of support{} away, which is precisely what
	// rows.go's package doc warns about.
	switch sel.kind {
	case selAll:
		r, err = dsClient.ReprocessAllBadRows(token, sc.tableID)
	case selStatus:
		// ReprocessBadRows takes *QuarantineStatus, where nil is the "no status
		// filter" form, so the status needs an address to be sent at all. The
		// empty ids list plus a status IS the status form to the backend.
		st := sel.status
		r, err = dsClient.ReprocessBadRows(token, sc.tableID, nil, &st)
	case selRowIDs:
		r, err = dsClient.ReprocessBadRows(token, sc.tableID, sel.rowIDs, nil)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
	if err != nil {
		return nil, err
	}
	return &bulkResult{AffectedRows: r.AffectedRows}, nil
}
