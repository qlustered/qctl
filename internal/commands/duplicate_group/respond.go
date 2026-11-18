package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// The error code a withdrawn, replaced or otherwise ended response answers with.
const codeResponseNotLive = "DUPLICATE_RESPONSE_NOT_LIVE"

const respondPermissionNote = "(responding and withdrawing a response need the DATASET_INGEST_UPLOAD permission, which every role but " +
	"read_only has and a read_only grant on the table withholds, and access to the table or one of its cloud sources)"

func newRespondCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "respond <group-id>",
		Short: "Answer a duplicate group for one of your cloud sources",
		Long: `Record a sender's response on a duplicate group with a copy on one of your
cloud sources:

  separate-rows           the copies are separate rows and should all load.
                          This is a request, not a decision: the table's
                          reviewers decide.
  corrected-file-follows  a corrected file will follow

--comment is required and must not be blank. When your copies in the group
are on several cloud sources, name the one you answer for with
--cloud-source-id. A response replaces that cloud source's live response on
the group and changes no rows; the internal users who marked, decided or
discussed the group are notified. 'qctl duplicate-group withdraw-response'
withdraws it.

-o json|yaml prints the server's response record and moves the result lines
to stderr; -o name prints duplicate-group/<uuid>.`,
		Example: `  qctl duplicate-group respond 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --kind separate-rows --comment "two shipments, same day"
  qctl duplicate-group respond 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --kind corrected-file-follows --comment "resending tonight" --cloud-source-id 7`,
		Annotations: catalog.For(resource),
		Args:        cobra.ExactArgs(1),
		RunE:        runRespond,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("kind", "", "Response: separate-rows or corrected-file-follows (required)")
	cmd.Flags().String("comment", "", "Why (required, not blank)")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source",
		"Cloud source you answer for; required when your copies are on several")
	return cmd
}

// responseKindFlag returns --kind as its wire value; hyphens read as
// underscores. A missing or unknown kind exits 2.
func responseKindFlag(cmd *cobra.Command) (duplicate_groups.ResponseKind, error) {
	raw, _ := cmd.Flags().GetString("kind")
	if strings.TrimSpace(raw) == "" {
		return "", qerrors.NewBadUsage("--kind is required: separate-rows or corrected-file-follows")
	}
	kind := duplicate_groups.ResponseKind(strings.ReplaceAll(strings.TrimSpace(raw), "-", "_"))
	switch kind {
	case duplicate_groups.ResponseSeparateRows, duplicate_groups.ResponseCorrectedFileFollows:
		return kind, nil
	}
	return "", qerrors.NewBadUsagef("invalid --kind %q: expected separate-rows or corrected-file-follows", raw)
}

// responseCommentFlag returns --comment trimmed; a missing or blank one
// exits 2.
func responseCommentFlag(cmd *cobra.Command) (string, error) {
	comment, _ := cmd.Flags().GetString("comment")
	if err := cmdutil.RequireCommentBody(comment); err != nil {
		return "", fmt.Errorf("--comment is required: %w", err)
	}
	return strings.TrimSpace(comment), nil
}

func runRespond(cmd *cobra.Command, args []string) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	kind, err := responseKindFlag(cmd)
	if err != nil {
		return err
	}
	comment, err := responseCommentFlag(cmd)
	if err != nil {
		return err
	}
	var sourceID *int
	id, ok, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	if ok {
		sourceID = &id
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	result, err := t.client.Respond(t.token(), t.tableID, groupID, kind, comment, sourceID)
	if err != nil {
		return mapSourceError(err, t.tableID, groupID, sourceID != nil)
	}
	out := cmdutil.NewResultWriter(cmd)
	writeResponseResult(out.Human, groupID, "responded", result)
	if result.Kind == duplicate_groups.ResponseSeparateRows {
		fmt.Fprintln(out.Human, "This is a request, not a decision: the table's reviewers decide whether the copies load as separate rows.")
	}
	writeSenderNext(out.Human, t.tableID, groupID)
	return out.Write(result, groupRef(groupID))
}

func newWithdrawResponseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "withdraw-response <group-id> <response-id>",
		Short: "Withdraw a live response of one of your cloud sources",
		Long: `Withdraw a live response one of your cloud sources recorded on a duplicate
group. 'qctl describe duplicate-group <group-id> --sender' lists the live
responses with their ids. Withdrawing changes no rows; responding again
records a new response.

-o json|yaml prints the server's response record and moves the result lines
to stderr; -o name prints duplicate-group/<uuid>.`,
		Example:     `  qctl duplicate-group withdraw-response 0190aaaa-1111-4222-8333-444444444444 0190eeee-1111-4222-8333-444444444444 --table-id 42`,
		Annotations: cmdutil.WithArgKinds(catalog.For(resource), resource, "-"),
		Args:        cobra.ExactArgs(2),
		RunE:        runWithdrawResponse,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	return cmd
}

func runWithdrawResponse(cmd *cobra.Command, args []string) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	responseID, err := duplicate_groups.ParseResponseID(args[1])
	if err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	result, err := t.client.WithdrawResponse(t.token(), t.tableID, groupID, responseID)
	if err != nil {
		return mapSourceError(err, t.tableID, groupID, false)
	}
	out := cmdutil.NewResultWriter(cmd)
	writeResponseResult(out.Human, groupID, "withdrew", result)
	writeSenderNext(out.Human, t.tableID, groupID)
	return out.Write(result, groupRef(groupID))
}

// writeResponseResult prints a respond or withdraw-response result.
func writeResponseResult(out io.Writer, groupID openapi_types.UUID, verb string, r *duplicate_groups.SourceResponse) {
	fmt.Fprintf(out, "%s %s %s [r: %s] (cloud-source/%d)\n",
		groupRef(groupID), verb, terminal.Sanitize(string(r.Kind)), r.ID, r.DataSourceModelID)
}

// senderDescribeCmd is the quoted describe duplicate-group --sender command
// for a group.
func senderDescribeCmd(tableID int, groupID openapi_types.UUID) string {
	return fmt.Sprintf("'qctl describe duplicate-group %s --sender --table-id %d'", groupID, tableID)
}

// writeSenderNext prints the describe line that follows a respond or
// withdraw-response result.
func writeSenderNext(out io.Writer, tableID int, groupID openapi_types.UUID) {
	fmt.Fprintf(out, "Next: qctl describe duplicate-group %s --sender --table-id %d\n", groupID, tableID)
}

// mapSourceError explains the source (sender) routes' refusals. %w keeps
// each exit code.
func mapSourceError(err error, tableID int, groupID openapi_types.UUID, sourceGiven bool) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	describe := senderDescribeCmd(tableID, groupID)
	switch apiErr.Code {
	case codeResponseNotLive:
		return fmt.Errorf("%w\n(the response was replaced, withdrawn or ended meanwhile; %s shows the live ones)", err, describe)
	case codeGroupEnded:
		return fmt.Errorf("%w\n(an ended group takes no responses; %s shows why it ended)", err, describe)
	case apierror.CodePeriodLocked:
		return fmt.Errorf("%w\n(no response can be recorded or withdrawn while the table's period is locked; %s shows the group)",
			err, describe)
	}
	switch apiErr.StatusCode {
	case http.StatusUnprocessableEntity:
		if !sourceGiven && namesField(err, "data_source_model_id") {
			return fmt.Errorf("%w\n(your copies in the group are on several cloud sources: name the one you answer for "+
				"with --cloud-source-id; %s lists them)", err, describe)
		}
	case http.StatusNotFound:
		if sourceGiven {
			return fmt.Errorf("%w\n(--cloud-source-id must name one of your cloud sources with a copy in the group; "+
				"%s lists them)", err, describe)
		}
	case http.StatusForbidden:
		if apiErr.Code == "" {
			return fmt.Errorf("%w\n%s", err, respondPermissionNote)
		}
	}
	return err
}

// namesField reports whether err's validation location ends in field.
func namesField(err error, field string) bool {
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || len(opErr.Loc) == 0 {
		return false
	}
	return fmt.Sprint(opErr.Loc[len(opErr.Loc)-1]) == field
}
