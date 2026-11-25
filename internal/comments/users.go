package comments

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Directory is an org-membership snapshot for name→UUID resolution and
// UUID→name display. Fetch once per command invocation.
type Directory struct {
	users []api.UserListItemSchema
	// missNote is appended to the error for an input that matches nobody.
	missNote string
}

// FetchUsers reads every user page into a Directory.
func FetchUsers(apiClient *client.Client, orgID openapi_types.UUID) (*Directory, error) {
	var users []api.UserListItemSchema
	var total *int
	page, limit := 1, 200
	orderBy := api.UserOrderByCreatedAt
	for {
		params := &api.GetUsersListParams{Page: &page, Limit: &limit, OrderBy: &orderBy}
		resp, err := apiClient.API.GetUsersListWithResponse(context.Background(), orgID, params)
		if err != nil {
			return nil, WrapAPIError(apiClient, err, "list users failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, HandleError(resp.StatusCode(), resp.Body, "list users failed")
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		if page == 1 {
			total = resp.JSON200.TotalRows
		}
		users = append(users, resp.JSON200.Results...)
		if len(resp.JSON200.Results) == 0 {
			break
		}
		if total != nil {
			if len(users) >= *total {
				break
			}
		} else if resp.JSON200.Next == nil {
			break
		}
		page++
	}
	return &Directory{users: users}, nil
}

// FetchMentionableUsers reads the people a comment on dataset datasetID may
// mention into a Directory. It holds only the people the mentionable-users
// route names, so a name or an email it does not hold fails to resolve with a
// note saying so.
func FetchMentionableUsers(apiClient *client.Client, orgID openapi_types.UUID, datasetID int) (*Directory, error) {
	resp, err := apiClient.API.ListMentionableUsersWithResponse(context.Background(), orgID, datasetID, nil)
	if err != nil {
		return nil, WrapAPIError(apiClient, err, "list mentionable users failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, HandleError(resp.StatusCode(), resp.Body, "list mentionable users failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	users := make([]api.UserListItemSchema, 0, len(*resp.JSON200))
	for _, u := range *resp.JSON200 {
		users = append(users, api.UserListItemSchema{
			ID: u.ID, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName, Role: u.Role,
		})
	}
	return &Directory{users: users, missNote: mentionableMissNote}, nil
}

// mentionableMissNote explains a miss against a FetchMentionableUsers
// directory.
const mentionableMissNote = "; only the people who flagged rows of this table or commented in its threads" +
	" resolve by email or name — name anyone else by full UUID (an external author may mention no one else)"

// Resolve maps input → user UUID. Order: full-UUID passthrough (no
// membership check), case-insensitive email, case-insensitive "First Last".
func (d *Directory) Resolve(input string) (openapi_types.UUID, error) {
	trimmed := strings.TrimSpace(input)
	if id, err := uuid.Parse(trimmed); err == nil {
		return id, nil
	}
	needle := strings.ToLower(trimmed)
	var matches []api.UserListItemSchema
	for _, u := range d.users {
		if strings.ToLower(u.Email) == needle {
			matches = append(matches, u)
		}
	}
	if len(matches) == 0 {
		for _, u := range d.users {
			full := strings.ToLower(strings.TrimSpace(u.FirstName + " " + u.LastName))
			if full != "" && full == needle {
				matches = append(matches, u)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		// A --mention value naming nobody is a flag validation failure, not a
		// server one: exit 2, like every other thing the user typed wrong.
		return openapi_types.UUID{}, qerrors.NewBadUsagef(
			"no user found matching %q (use an email, a \"First Last\" name, or a full UUID)%s", input, d.missNote)
	default:
		candidates := make([]string, 0, len(matches))
		for _, u := range matches {
			candidates = append(candidates, fmt.Sprintf("%s %s <%s>", u.FirstName, u.LastName, u.Email))
		}
		return openapi_types.UUID{}, qerrors.NewBadUsagef(
			"user %q is ambiguous; matches: %s (use the email or UUID)", input, strings.Join(candidates, ", "))
	}
}

// ResolveAll resolves every input, failing on the first unresolvable one.
func (d *Directory) ResolveAll(inputs []string) ([]openapi_types.UUID, error) {
	ids := make([]openapi_types.UUID, 0, len(inputs))
	for _, input := range inputs {
		id, err := d.Resolve(input)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DisplayName returns "First Last", falling back to email, then the UUID.
func (d *Directory) DisplayName(id openapi_types.UUID) string {
	for _, u := range d.users {
		if u.ID == id {
			if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
				return name
			}
			return u.Email
		}
	}
	return id.String()
}

// CCLine is the trailing "cc" line every --mention verb appends to a comment
// body: one structured MentionLink per id, labelled with its display name,
// after a blank line. The link is the only form the backend notifies on, so a
// bare "@alice" in the body reaches nobody. It returns "" for no ids.
func (d *Directory) CCLine(ids []openapi_types.UUID) string {
	if len(ids) == 0 {
		return ""
	}
	links := make([]string, 0, len(ids))
	for _, id := range ids {
		links = append(links, MentionLink(d.DisplayName(id), id))
	}
	return "\n\ncc " + strings.Join(links, " ")
}
