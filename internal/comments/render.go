package comments

import (
	"fmt"
	"regexp"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// UserDisplay returns "First Last", falling back to email, then the id.
func UserDisplay(u *api.UserInfoTinyDictSchema, fallback openapi_types.UUID) string {
	if u != nil {
		if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
			return name
		}
		if u.Email != "" {
			return u.Email
		}
	}
	return fallback.String()
}

// UserDisplayWithRole appends the user's role when one is supplied.
func UserDisplayWithRole(u *api.UserInfoTinyDictSchema, fallback openapi_types.UUID) string {
	name := UserDisplay(u, fallback)
	if u != nil {
		name += RoleSuffix(u.Role)
	}
	return name
}

// RoleSuffix returns " (role)", or "" when role is nil or empty.
func RoleSuffix(role *api.RoleEnum) string {
	if role == nil || *role == "" {
		return ""
	}
	return " (" + string(*role) + ")"
}

// mentionLinkRE matches the backend's structured mention link
// "[@Label](mention:<uuid>)". Bare "@name" is NOT a mention on the wire.
var mentionLinkRE = regexp.MustCompile(`\[@([^\]]+)\]\(mention:[0-9a-fA-F-]{36}\)`)

// DeRenderMentions rewrites structured mention links to plain "@Label" for
// the describe payload.
func DeRenderMentions(s string) string {
	return mentionLinkRE.ReplaceAllString(s, "@$1")
}

// MentionLink renders the backend's structured mention form — the only form
// that notifies. Used by the comment command to append "cc" lines.
func MentionLink(label string, id openapi_types.UUID) string {
	return fmt.Sprintf("[@%s](mention:%s)", label, id)
}

// CountComments counts a comment tree including every nested reply.
func CountComments(comments []api.CommentResponseSchema) int {
	n := 0
	for i := range comments {
		n++
		if comments[i].Replies != nil {
			n += CountComments(*comments[i].Replies)
		}
	}
	return n
}

// RenderComment writes one comment and recurses into replies (backend max
// depth 3). Tombstones keep the UUID tag and timestamp but no author — the
// backend suppresses the author object and rewrites the body to
// "[comment deleted]", so the body prints as-is.
func RenderComment(b *strings.Builder, c *api.CommentResponseSchema, depth int) {
	indent := strings.Repeat("  ", depth)
	header := indent + "[c: " + c.ID.String() + "] "
	if depth > 0 {
		header += "↳ "
	}
	if c.IsDeleted != nil && *c.IsDeleted {
		header += fmt.Sprintf("(%s):", timeutil.FormatRelative(c.CreatedAt))
	} else {
		header += fmt.Sprintf("%s (%s)", UserDisplayWithRole(c.Author, c.AuthorID), timeutil.FormatRelative(c.CreatedAt))
		if c.IsEdited {
			header += " (edited)"
		}
		header += ":"
	}
	b.WriteString(header + "\n")

	bodyIndent := indent + "  "
	for _, line := range strings.Split(DeRenderMentions(c.Body), "\n") {
		b.WriteString(bodyIndent + line + "\n")
	}
	if c.LinkedDryRunJobID != nil {
		fmt.Fprintf(b, "%s(dry run: job %d)\n", bodyIndent, *c.LinkedDryRunJobID)
	}
	if c.Replies != nil {
		for i := range *c.Replies {
			RenderComment(b, &(*c.Replies)[i], depth+1)
		}
	}
}
