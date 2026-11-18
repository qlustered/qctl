package apierror

import (
	stderrors "errors"
	"net/http"
)

// isPiiColumnsRefusal reports whether e is atlas's 403 refusing a table PATCH's
// pii_columns (settings_view_core.py _refuse_pii_columns_change): status 403,
// loc ["body", "pii_columns"]. The table was found, so buildHTTPError attaches
// no organization-scope note to it.
func isPiiColumnsRefusal(e *OperationalError) bool {
	return e != nil && e.StatusCode == http.StatusForbidden &&
		len(e.Loc) == 2 && e.Loc[0] == "body" && e.Loc[1] == "pii_columns"
}

// IsPiiColumnsRefusal reports whether the *OperationalError in err's chain is
// a 403 at loc ["body", "pii_columns"].
func IsPiiColumnsRefusal(err error) bool {
	var opErr *OperationalError
	return stderrors.As(err, &opErr) && isPiiColumnsRefusal(opErr)
}
