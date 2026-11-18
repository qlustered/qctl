package cmdutil

import (
	"errors"
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// IsRegisteredAlias reports whether input has the shape and prefix of any
// registered alias resource. It performs no config, cache, or API access.
// Callers use it only to decide whether a separate scope selector is required;
// ResolveTableRuleAlias remains responsible for validating the resource type
// and resolving the alias for the active server.
func IsRegisteredAlias(input string) bool {
	prefix, _, err := aliasstore.ParseAlias(input)
	if err != nil {
		return false
	}
	_, err = aliasstore.ResourceTypeForPrefix(prefix)
	return err == nil
}

// ResolveTableRuleAlias reports whether input is a registered alias reference and, if it is a
// table-rule (r-*) alias, resolves it to a dataset-rule UUID for the active server.
//
// Returns:
//   - (uuid, true, nil)   when input was an r-* alias resolved successfully.
//   - ("", false, nil)    when input is not alias-shaped, or its prefix is not a registered
//     alias prefix (so a real rule named like "v2-1" still resolves normally).
//   - ("", false, err)    when input uses a registered prefix for a different resource, or is an
//     unknown / cross-server r-* alias. The error is surfaced before any API request.
func ResolveTableRuleAlias(ctx *CommandContext, input string) (string, bool, error) {
	prefix, _, perr := aliasstore.ParseAlias(input)
	if perr != nil {
		return "", false, nil // not alias-shaped
	}
	rt, rerr := aliasstore.ResourceTypeForPrefix(prefix)
	if rerr != nil {
		return "", false, nil // unregistered prefix: treat as an ordinary identifier
	}
	if rt != aliasstore.ResourceDatasetRule {
		return "", false, qerrors.NewBadUsagef("alias %q is not a table-rule alias", input)
	}

	serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
	if err != nil {
		return "", false, err
	}
	store, err := aliasstore.OpenReadOnly()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, qerrors.NewNotFound(apierror.WithScopeNote(
				fmt.Sprintf("unknown alias %q for the current server", input)))
		}
		return "", false, err
	}
	defer store.Close()

	uuid, err := store.ResolveTyped(serverKey, aliasstore.ResourceDatasetRule, input)
	if err != nil {
		if errors.Is(err, aliasstore.ErrAliasNotFound) {
			return "", false, qerrors.NewNotFound(apierror.WithScopeNote(err.Error()))
		}
		return "", false, err
	}
	return uuid, true, nil
}
