package cmdutil

import (
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/user_submitted"
)

// NewQuarantineRowProber wires internal/user_submitted's failure reporting to
// the get_data read that can explain it.
//
// It lives here, and not in either domain package, because `alert submit` and
// `warning submit` are in two command packages that share nothing else, and
// because internal/user_submitted is deliberately cobra-free AND client-free —
// it names the states and does the rendering, and the adapter that knows how a
// row is read stays outside it.
//
// An error on the FIRST read becomes RowStateUnknown, which prints nothing.
// The caller is already reporting a failed submit with an exit code chosen
// from the server's own per-row statuses; a diagnostic that turned a 403 on
// the probe into a second, differently-shaped complaint would obscure the
// failure it exists to explain. The probe's silence costs the caller only the
// sentence it would have added.
//
// The second read is the clean side, and it runs ONLY when the quarantine side
// came back empty — the state that alone cannot say whether the row is sitting
// clean or does not exist, which are opposite fixes. So a row that IS
// quarantined still costs exactly one GET, and the doubling the cap has to
// absorb applies only to ids the quarantine table does not hold.
//
// An error on that second read does NOT fall back to RowStateUnknown: the
// first read succeeded, and RowStateAbsentFromQuarantine is precisely its
// verdict. Reporting nothing there would throw away a true answer because a
// refinement of it failed.
func NewQuarantineRowProber(ctx *CommandContext, datasetID int) user_submitted.RowProber {
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	return func(rowID int) user_submitted.RowState {
		row, err := client.LookupQuarantinedRow(ctx.Credential.AccessToken, datasetID, rowID)
		switch {
		case err != nil:
			return user_submitted.RowStateUnknown
		case row == nil:
			return cleanSideState(client, ctx.Credential.AccessToken, datasetID, rowID)
		case row.AwaitingProcessing:
			return user_submitted.RowStateAwaitingProcessing
		default:
			return user_submitted.RowStateQuarantined
		}
	}
}

// cleanSideState splits "the quarantine side does not hold this id" into the
// two states that need opposite actions from the caller: park the row, or fix
// the id.
func cleanSideState(client *datasets.Client, accessToken string, datasetID, rowID int) user_submitted.RowState {
	found, err := client.LookupCleanRow(accessToken, datasetID, rowID)
	switch {
	case err != nil:
		return user_submitted.RowStateAbsentFromQuarantine
	case found:
		return user_submitted.RowStateClean
	default:
		return user_submitted.RowStateAbsentFromTable
	}
}
