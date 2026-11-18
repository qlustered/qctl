package paramflags

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// enumValuesForType returns the allowed values of a generated named string
// type used by a data query parameter.
//
// Derive classifies such a field KindString and Register gives it a plain
// string flag, so without this table `--quarantine-hold yes` is typed by the
// user, sent to the backend and comes back as a 422: the same exit code as a
// refusal here, but with a server message about a query parameter instead of
// the flag's own name and values, and one round trip (plus, on `get data`, one
// name-resolution request) spent to get it.
//
// Go reflection cannot enumerate constants. scripts/extract-spec-metadata.go
// therefore emits every string enum's membership into api.StringEnumValues;
// the generated Go type name is the OpenAPI component name used as its key.
// TestEveryNamedStringParamHasItsValues catches a future generator naming
// mismatch instead of letting validation silently disappear.
func enumValuesForType(typ reflect.Type) ([]string, bool) {
	values, ok := api.StringEnumValues[typ.Name()]
	return values, ok
}

// ValidateEnums refuses a value outside the declared set of an enum-valued
// flag. A client-side flag validation failure, so exit 2.
//
// Only a flag the user actually changed is checked: an untouched one keeps its
// pflag zero value, which for most of these is not a member of the set, and
// Build omits it from the request anyway.
func ValidateEnums(cmd *cobra.Command, flags []Flag) error {
	for _, f := range flags {
		values, ok := enumValuesForType(f.Type)
		if !ok || f.Kind != KindString || !cmd.Flags().Changed(f.FlagName) {
			continue
		}
		got, err := cmd.Flags().GetString(f.FlagName)
		if err != nil {
			return fmt.Errorf("failed to read --%s: %w", f.FlagName, err)
		}
		if containsString(values, got) {
			continue
		}
		return qerrors.NewBadUsagef("invalid value %q for --%s: must be one of %s",
			got, f.FlagName, quotedList(values))
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// quotedList renders a value set for an error message. Every value is quoted,
// including the empty one FilterOperator declares -- an unquoted list would
// render that member as nothing at all.
func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}
