package deliverables

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
)

// minPrefixDigits is the shortest UUID prefix a reference matches by.
const minPrefixDigits = 8

// resolveLimit is the page size of the resolver's first listing.
const resolveLimit = 500

// listAttempts bounds the listings one resolution makes.
const listAttempts = 3

// ValidateReference rejects a blank deliverable reference.
func ValidateReference(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return qerrors.NewBadUsage("deliverable reference cannot be empty")
	}
	return nil
}

// ResolveID resolves a full UUID, an exact slug or name, or a UUID prefix of
// at least 8 hex digits. A full UUID is returned without a listing; otherwise
// includeArchived also matches deliverables in the trash.
func (c *Client) ResolveID(token, ref string, includeArchived bool) (uuid.UUID, error) {
	if err := ValidateReference(ref); err != nil {
		return uuid.Nil, err
	}
	if len(ref) == 36 {
		if id, err := uuid.Parse(ref); err == nil {
			return id, nil
		}
	}
	params := ListParams{Limit: resolveLimit, OrderBy: "created_at", IncludeArchived: includeArchived}
	if !org.IsUUIDLike(ref) && utf8.RuneCountInString(ref) >= 3 {
		params.Search = ref
	}
	items, err := c.listAll(token, params)
	if err != nil {
		return uuid.Nil, err
	}
	return Resolve(items, ref)
}

// listAll reads every deliverable params matches in one page, raising the
// limit to the server's count when the first page falls short of it.
func (c *Client) listAll(token string, params ListParams) ([]ListItem, error) {
	for attempt := 1; ; attempt++ {
		page, err := c.List(token, params)
		if err != nil {
			return nil, err
		}
		var items []ListItem
		if page.Results != nil {
			items = *page.Results
		}
		if page.TotalRows == nil {
			return nil, fmt.Errorf("the deliverable list reported no total; pass a full UUID")
		}
		if len(items) >= *page.TotalRows {
			return items, nil
		}
		if attempt == listAttempts {
			return nil, fmt.Errorf("listed %d of %d deliverables; retry or pass a full UUID", len(items), *page.TotalRows)
		}
		params.Limit = *page.TotalRows + 1
	}
}

// Resolve selects one deliverable from items by exact slug, else exact name,
// else UUID prefix.
func Resolve(items []ListItem, ref string) (uuid.UUID, error) {
	if err := ValidateReference(ref); err != nil {
		return uuid.Nil, err
	}
	prefix := compactUUID(ref)
	for _, match := range []func(ListItem) bool{
		func(item ListItem) bool { return item.Slug == ref },
		func(item ListItem) bool { return item.Name == ref },
		func(item ListItem) bool {
			return org.IsUUIDLike(ref) && len(prefix) >= minPrefixDigits && strings.HasPrefix(compactUUID(item.ID.String()), prefix)
		},
	} {
		var found []ListItem
		for _, item := range items {
			if match(item) {
				found = append(found, item)
			}
		}
		if len(found) == 1 {
			return found[0].ID, nil
		}
		if len(found) > 1 {
			labels := make([]string, len(found))
			for i, item := range found {
				labels[i] = label(item)
			}
			return uuid.Nil, qerrors.NewBadUsagef("%d deliverables match %q: %s; pass a slug or a full UUID",
				len(found), ref, strings.Join(labels, "; "))
		}
	}
	return uuid.Nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
		"no deliverable matches %q; use 'qctl get deliverables' to list them", ref)))
}

func label(item ListItem) string {
	s := fmt.Sprintf("%s (name %q, %s", item.Slug, item.Name, item.ID)
	if item.ArchivedAt != nil {
		s += ", in the trash"
	}
	return s + ")"
}

func compactUUID(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "-", ""))
}
