package privilege_requests //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// statuses and actions are the two closed vocabularies a command takes from
// the user. Every element is an api.* constant, so a RENAMED value fails to
// build; vocab_test.go catches the other half — a value ADDED to the spec that
// nobody added here.
//
// A regen can flip which of these two enums carries the type-name prefix
// (PrivilegeAction's constants are bare today, PrivilegeRequestStatus's are
// prefixed). When one stops compiling, take the new name from client.gen.go;
// do not alias the old one.
var (
	statuses = []api.PrivilegeRequestStatus{
		api.PrivilegeRequestStatusApproved,
		api.PrivilegeRequestStatusExpired,
		api.PrivilegeRequestStatusPending,
		api.PrivilegeRequestStatusRejected,
	}
	actions = []api.PrivilegeAction{
		api.Demote,
		api.Escalate,
	}
)

// StatusNames returns the request-status vocabulary in its wire spelling
// (capitalised: "Pending", not "pending"), sorted.
func StatusNames() []string { return names(statuses) }

// ActionNames returns the action vocabulary in its wire spelling, sorted.
func ActionNames() []string { return names(actions) }

func names[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

// ParseStatus matches a user-typed status case-insensitively and returns the
// canonical wire value, so `--status pending` sends "Pending". An unknown
// value is exit 2, naming the wire spellings, before any request is made.
func ParseStatus(s string) (api.PrivilegeRequestStatus, error) {
	v, ok := match(statuses, s)
	if !ok {
		return "", qerrors.NewBadUsagef("unknown privilege request status %q\nvalid statuses: %s",
			s, strings.Join(StatusNames(), ", "))
	}
	return v, nil
}

// ParseAction matches a user-typed action case-insensitively and returns the
// canonical wire value. An unknown value is exit 2, naming the wire
// spellings, before any request is made.
func ParseAction(s string) (api.PrivilegeAction, error) {
	v, ok := match(actions, s)
	if !ok {
		return "", qerrors.NewBadUsagef("unknown privilege action %q\nvalid actions: %s",
			s, strings.Join(ActionNames(), ", "))
	}
	return v, nil
}

func match[T ~string](values []T, s string) (T, bool) {
	for _, v := range values {
		if strings.EqualFold(string(v), s) {
			return v, true
		}
	}
	return "", false
}

// ActionOf names the change a request (or an audit row) describes, from the
// old_value/new_value pair the wire carries in place of an action field.
//
// The controller only ever writes permission_key "is_ops" with the strings
// "true"/"false", so the two flips map back onto the action vocabulary.
// Anything else — the model's comment leaves room for other permission keys —
// is rendered "<old>→<new>" verbatim rather than guessed at.
func ActionOf(oldValue, newValue string) string {
	switch {
	case oldValue == "false" && newValue == "true":
		return string(api.Escalate)
	case oldValue == "true" && newValue == "false":
		return string(api.Demote)
	default:
		return oldValue + "→" + newValue
	}
}
