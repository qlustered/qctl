package rule_versions

import (
	"testing"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
)

func TestFormatAuthor(t *testing.T) {
	tests := []struct {
		name string
		user *api.UserInfoTinyDictSchema
		want string
	}{
		{"nil user", nil, "-"},
		{"no names", &api.UserInfoTinyDictSchema{FirstName: "  ", LastName: ""}, "-"},
		{"first and last", &api.UserInfoTinyDictSchema{FirstName: "John", LastName: "Doe"}, "John D."},
		{"first only", &api.UserInfoTinyDictSchema{FirstName: "Ana"}, "Ana"},
		{"last only", &api.UserInfoTinyDictSchema{FirstName: " ", LastName: "Doe"}, "D."},
		{"long first name cut to 10", &api.UserInfoTinyDictSchema{FirstName: "Bartholomew", LastName: "Smith"}, "Bartholome S."},
		// 12 characters, 14 bytes: the cut lands after the tenth character,
		// and the initial is the whole first character of the last name.
		{"multi-byte names cut by character", &api.UserInfoTinyDictSchema{FirstName: "Søren-Åsmund", LastName: "Ørsted"}, "Søren-Åsmu Ø."},
		// 10 characters, 12 bytes: within the limit, so nothing is cut. A
		// 10-byte cut would end inside the second "ø".
		{"multi-byte name of 10 characters kept whole", &api.UserInfoTinyDictSchema{FirstName: "Ana-Sølvør", LastName: "Ødegård"}, "Ana-Sølvør Ø."},
		{"single-character CJK names", &api.UserInfoTinyDictSchema{FirstName: "伟", LastName: "李"}, "伟 李."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAuthor(tt.user)
			if got != tt.want {
				t.Errorf("FormatAuthor() = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("FormatAuthor() = %q is not valid UTF-8", got)
			}
		})
	}
}

// describe rule's "Created by:" initial is the whole first character of a
// multi-byte last name.
func TestFormatUserNameMultiByteInitial(t *testing.T) {
	if got := formatUserName(&api.UserInfoTinyDictSchema{FirstName: "Søren", LastName: "Ørsted"}); got != "Søren Ø." {
		t.Errorf("formatUserName() = %q, want %q", got, "Søren Ø.")
	}
}
