package concepts

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/tags"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// Display is the table row for a concept.
type Display struct {
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	ExpectedFieldType string `json:"expected_field_type"`
	Tags              string `json:"tags"`
	UpdatedAt         string `json:"updated_at"`
	ShortID           string `json:"short_id"`
	ID                string `json:"id"`
}

// ToDisplayList formats concept list rows.
func ToDisplayList(items []Tiny) []Display {
	rows := make([]Display, len(items))
	for i, item := range items {
		fieldType := "-"
		if item.ExpectedFieldType != nil {
			fieldType = string(*item.ExpectedFieldType)
		}
		rows[i] = Display{
			Slug: item.Slug, Name: item.Name, ExpectedFieldType: fieldType,
			Tags:      tags.Build(tags.TagPair{Label: "Built-in", Active: item.IsBuiltin}, tags.TagPair{Label: "Customized", Active: item.IsBuiltin && item.IsCustomized}),
			UpdatedAt: timeutil.FormatRelative(item.UpdatedAt),
			ShortID:   strings.ReplaceAll(item.ID.String(), "-", "")[:8], ID: item.ID.String(),
		}
	}
	return rows
}

// FormatDetail renders a concept's full fields for a person.
func FormatDetail(item *Full) string {
	value := func(s *string) string {
		if s == nil || *s == "" {
			return "-"
		}
		return terminal.Sanitize(*s)
	}
	fieldType := "-"
	if item.ExpectedFieldType != nil {
		fieldType = string(*item.ExpectedFieldType)
	}
	aliases := "-"
	if item.Aliases != nil && len(*item.Aliases) > 0 {
		clean := make([]string, len(*item.Aliases))
		for i, alias := range *item.Aliases {
			clean[i] = terminal.Sanitize(alias)
		}
		aliases = strings.Join(clean, ", ")
	}
	return fmt.Sprintf("Name:                %s\nSlug:                %s\nID:                  %s\nDescription:         %s\nAliases:             %s\nExpected field type: %s\nBuilt-in:            %t\nCustomized:          %t\nCreated:             %s\nUpdated:             %s\n",
		terminal.Sanitize(item.Name), terminal.Sanitize(item.Slug), item.ID, value(item.Description), aliases, fieldType,
		item.IsBuiltin, item.IsCustomized, item.CreatedAt.Format("2006-01-02 15:04:05 MST"), item.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
}
