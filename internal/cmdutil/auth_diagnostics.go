package cmdutil

import (
	"errors"
	"fmt"
	"slices"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func authFailure(cause error, reason, server, id, name string, source config.OrgSource) error {
	scope := "no organization selected"
	if id != "" {
		display := id
		if name != "" {
			display = fmt.Sprintf("%s (%s)", name, id)
		}
		scope = fmt.Sprintf("organization %s (%s)", display, source)
	}
	message := fmt.Sprintf("%s for server %s, %s", reason, server, scope)
	if cause != nil {
		return fmt.Errorf("%w\n%s", qerrors.Wrap(qerrors.ExitUnauthorized, cause, message), apierror.LoginHint)
	}
	return qerrors.NewUnauthorized(message + ".\n" + apierror.LoginHint)
}

func checkServerCredentials(cfg *config.Config, store *auth.CredentialStore, endpoint, server string) error {
	ids, err := store.StoredOrganizations(endpoint)
	if err != nil {
		return unreadableCredentialError(err, "", false)
	}
	for _, ctx := range cfg.Contexts {
		key, keyErr := config.NormalizeEndpointKey(ctx.Server)
		if keyErr != nil || key != endpoint {
			continue
		}
		if ctx.Organization != "" {
			ids = append(ids, ctx.Organization)
		}
		for _, ref := range ctx.Organizations {
			if ref.ID != "" {
				ids = append(ids, ref.ID)
			}
		}
	}
	slices.Sort(ids)
	var expired bool
	for _, id := range slices.Compact(ids) {
		cred, retrieveErr := store.Retrieve(endpoint, id)
		if errors.Is(retrieveErr, auth.ErrCredentialNotFound) {
			continue
		}
		if retrieveErr != nil {
			return unreadableCredentialError(retrieveErr, auth.MakeCredentialKey(endpoint, id), !auth.IsPlaintextAllowed())
		}
		if !cred.IsExpired() {
			return nil
		}
		expired = true
	}
	if expired {
		return authFailure(nil, "token expired", server, "", "", "")
	}
	return authFailure(auth.ErrCredentialNotFound, "not logged in", server, "", "", "")
}
