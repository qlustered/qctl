package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/spf13/cobra"
)

// FetchDisplayDirectory sweeps the users directory a view names people with.
// The directory is presentation, not the answer: when it cannot be fetched
// (the editor and external roles lack org:users:list) the view still renders,
// with user UUIDs, after one stderr warning. fetch is a domain client's
// FetchUsers; every one returns comments.Directory under its own alias.
func FetchDisplayDirectory(
	cmd *cobra.Command,
	fetch func(accessToken string) (*comments.Directory, error),
	accessToken string,
) *comments.Directory {
	directory, err := fetch(accessToken)
	if err == nil {
		return directory
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user directory unavailable; displaying user UUIDs: %v\n", err)
	return new(comments.Directory)
}

// ResolveUsers turns --reviewer / --mention values into user ids. A value is
// an email, a "First Last" name, or a full UUID, and only the first two need
// the org directory: when every value is a UUID the sweep is skipped, because
// comments.Directory.Resolve passes a UUID through unchecked and would return
// the same ids. That is a permission question rather than a speed one — the
// sweep needs org:users:list, which the editor and external roles do not hold
// even where they may request a data review, comment, or mention.
//
// The directory returned is the one the sweep produced, or nil when no sweep
// happened; a command that also names those people in its own output passes it
// to EnsureDisplayDirectory.
func ResolveUsers(
	fetch func(accessToken string) (*comments.Directory, error),
	accessToken string,
	refs []string,
) ([]openapi_types.UUID, *comments.Directory, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if ids, ok := parsedUserIDs(refs); ok {
		return ids, nil, nil
	}
	directory, err := fetch(accessToken)
	if err != nil {
		return nil, nil, directoryReadError(err, refs)
	}
	ids, err := directory.ResolveAll(refs)
	if err != nil {
		return nil, nil, err
	}
	return ids, directory, nil
}

// EnsureDisplayDirectory returns directory when a resolve already swept the
// membership, and a best-effort sweep otherwise: ResolveUsers reads nothing
// for a UUID, which leaves a command that prints names with nothing to name
// them with. Like FetchDisplayDirectory it never fails — names fall back to
// UUIDs after one warning on stderr.
func EnsureDisplayDirectory(
	cmd *cobra.Command,
	directory *comments.Directory,
	fetch func(accessToken string) (*comments.Directory, error),
	accessToken string,
) *comments.Directory {
	if directory != nil {
		return directory
	}
	return FetchDisplayDirectory(cmd, fetch, accessToken)
}

// MentionDirectory returns a fetch for ResolveUsers and EnsureDisplayDirectory
// that sweeps the org directory and, when that read is refused with a 403,
// reads the table's mentionable users instead. When the fallback fails too,
// the directory's error is returned with the fallback's appended.
func MentionDirectory(
	directory func(accessToken string) (*comments.Directory, error),
	mentionable func(accessToken string) (*comments.Directory, error),
) func(accessToken string) (*comments.Directory, error) {
	return func(accessToken string) (*comments.Directory, error) {
		dir, err := directory(accessToken)
		var apiErr *comments.APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
			return dir, err
		}
		fallback, fallbackErr := mentionable(accessToken)
		if fallbackErr != nil {
			return nil, fmt.Errorf("%w (mentionable users: %s)", err, fallbackErr.Error())
		}
		return fallback, nil
	}
}

// parsedUserIDs parses every value as a UUID, reporting false as soon as one
// is a name or an email. Surrounding whitespace is ignored, as
// comments.Directory.Resolve ignores it.
func parsedUserIDs(refs []string) ([]openapi_types.UUID, bool) {
	ids := make([]openapi_types.UUID, 0, len(refs))
	for _, ref := range refs {
		id, err := uuid.Parse(strings.TrimSpace(ref))
		if err != nil {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

// directoryReadError reports a failed sweep against the value that forced it,
// naming the permission and the way past it on a 403. The exit code rides the
// wrapped error, so that 403 stays 4: this caller's role really cannot read
// the directory.
func directoryReadError(err error, refs []string) error {
	message := fmt.Sprintf("failed to list users to resolve %q", firstNamedUser(refs))
	var apiErr *comments.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s: %w\n"+
			"(resolving a name or an email needs the org:users:list permission, which the editor and external"+
			" roles do not hold; name the user by full UUID to resolve without the directory)", message, err)
	}
	return fmt.Errorf("%s: %w", message, err)
}

// firstNamedUser is the first value that made the sweep necessary.
func firstNamedUser(refs []string) string {
	for _, ref := range refs {
		if _, err := uuid.Parse(strings.TrimSpace(ref)); err != nil {
			return ref
		}
	}
	return ""
}
