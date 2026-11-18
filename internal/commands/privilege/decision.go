package privilege

import (
	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// pendingRequest is what approve and reject have in hand once the request
// has been read and found Pending: the request, and a client to decide it
// with.
type pendingRequest struct {
	token  string
	client *privilege_requests.Client
	detail *privilege_requests.Detail
}

// fetchPending is step 1 of both decision verbs. Once the id parses and the
// context loads, it makes exactly one request, and when it refuses, that is
// the verb's only request.
//
// The decision routes answer an unknown id with 400, so this GET's 404 is
// what gives an unknown id exit 3, as describe does. A request that is no
// longer Pending is refused here at exit 2, and no decision is sent. The
// server would refuse it with a 400 as well, but only after the approval
// prompt had asked about a decision that can no longer be made.
func fetchPending(cmd *cobra.Command, arg, verb string) (*pendingRequest, error) {
	// Before Bootstrap: a malformed id costs no credential read and no request.
	id, err := cmdutil.ParseUUIDArg(arg, "privilege request id")
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := privilege_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	detail, err := client.Get(ctx.Credential.AccessToken, id)
	if err != nil {
		return nil, err
	}
	if detail.Status != api.PrivilegeRequestStatusPending {
		return nil, qerrors.NewBadUsagef("privilege-request/%s is already %s; nothing to %s",
			id, terminal.Sanitize(string(detail.Status)), verb)
	}
	return &pendingRequest{token: ctx.Credential.AccessToken, client: client, detail: detail}, nil
}

// emailOf names a person by email, or by UUID when the server embedded no
// user or one with no email (every embedded user on the detail is nullable).
func emailOf(u *privilege_requests.UserInfo, id uuid.UUID) string {
	if u == nil || u.Email == "" {
		return id.String()
	}
	return u.Email
}
