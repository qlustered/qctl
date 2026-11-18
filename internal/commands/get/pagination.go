package get

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// paginationFlags reads the --page/--limit pair for the server-paginated `get`
// commands listed in pagination_flags_test.go's sweep table, refusing
// non-positive values instead of forwarding them.
//
// It is called from each command's parameter builder, which runs before any
// request — including before the API call that resolves a --table name — so a
// bad page costs nothing and reports as a usage error (exit 2) rather than as
// a whole-organization listing at exit 0.
//
// `get cloud-sources`, `get files` and `get data` take --limit but are
// deliberately not callers:
//
//   - `get cloud-sources` and `get files` take no --page: they auto-paginate
//     client-side and read --limit as "0 = unlimited", a meaning this
//     validator's "must be positive" rule cannot express. Both validate --limit
//     themselves via
//     cmdutil.NonNegativeCountFlag instead (`get cloud-sources`' separate
//     --chunk-size, which carries no "unlimited" meaning, goes through
//     cmdutil.PositiveCountFlag like this function does).
//   - `get data` fetches one backend page per call. Its int flags, including
//     page and limit, are registered with default 0 (RegisterDataFlags in
//     data_flags.go), and BuildDataParams substitutes 1/20 for them only when
//     the flag is unchanged (data_flags.go's pinPageAndLimit). Calling
//     paginationFlags here would read that default 0 and reject every
//     *unset* invocation, not just a user-typed one — so it validates
//     --page/--limit itself instead, guarding on cmd.Flags().Changed before
//     calling cmdutil.PositiveCountFlag directly: see validateDataPagination
//     in data_flags.go, called from data.go's RunE before ResolveID.
func paginationFlags(cmd *cobra.Command) (page, limit int, err error) {
	if page, err = cmdutil.PositiveCountFlag(cmd, "page"); err != nil {
		return 0, 0, err
	}
	if limit, err = cmdutil.PositiveCountFlag(cmd, "limit"); err != nil {
		return 0, 0, err
	}
	return page, limit, nil
}
