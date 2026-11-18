package releases

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want Args
	}{
		{"empty", nil, Args{}},
		{"plain command", []string{"get", "tables"}, Args{Words: []string{"get", "tables"}}},
		{"space forms", []string{"--config", "/c", "--context", "prod", "--server", "https://s", "get", "tables"},
			Args{Config: "/c", Context: "prod", Server: "https://s", Words: []string{"get", "tables"}}},
		{"equals forms", []string{"get", "--config=/c", "--context=prod", "--server=https://s", "tables"},
			Args{Config: "/c", Context: "prod", Server: "https://s", Words: []string{"get", "tables"}}},
		{"empty equals value", []string{"--server=", "get"}, Args{Words: []string{"get"}}},
		{"last occurrence wins", []string{"--context", "a", "get", "--context=b"},
			Args{Context: "b", Words: []string{"get"}}},
		{"value that looks like a flag", []string{"--context", "--help", "get"},
			Args{Context: "--help", Words: []string{"get"}}},
		{"value-taking globals before the command", []string{"-o", "json", "--org", "acme", "--retries", "3", "--request-timeout", "5s", "--columns", "a,b", "--max-column-width", "9", "--user", "u", "get", "tables"},
			Args{Words: []string{"get", "tables"}}},
		{"attached shorthand values", []string{"-ojson", "-O=acme", "-o=yaml", "get", "tables"},
			Args{Words: []string{"get", "tables"}}},
		{"shorthand cluster ending in a value flag", []string{"-vo", "json", "get"}, Args{Words: []string{"get"}}},
		{"bool and count globals", []string{"--no-headers", "-vvv", "-v=7", "--verbose", "--allow-insecure-http", "--allow-plaintext-secrets", "get"},
			Args{Words: []string{"get"}}},
		{"flags after the command anywhere", []string{"get", "tables", "--sort", "name", "--server", "https://s"},
			Args{Server: "https://s", Words: []string{"get", "tables"}}},
		{"unknown flag ends the words", []string{"apply", "-f", "x.yaml", "--context", "c"},
			Args{Context: "c", Words: []string{"apply"}}},
		{"bare -- ends parsing", []string{"get", "--", "--server", "https://s", "--help"},
			Args{Words: []string{"get"}}},
		{"value flag at the end", []string{"get", "--server"}, Args{Words: []string{"get"}}},
		{"-h", []string{"get", "-h"}, Args{Help: true, Words: []string{"get"}}},
		{"-h in a cluster", []string{"-vh", "get"}, Args{Help: true, Words: []string{"get"}}},
		{"--help anywhere", []string{"apply", "-f", "x", "--help"}, Args{Help: true, Words: []string{"apply"}}},
		{"--help=false", []string{"get", "--help=false"}, Args{Words: []string{"get"}}},
		{"help command", []string{"help", "get"}, Args{Help: true, Words: []string{"help", "get"}}},
		{"help after flags", []string{"-o", "json", "help"}, Args{Help: true, Words: []string{"help"}}},
		{"help as a later word is not help", []string{"get", "help"}, Args{Words: []string{"get", "help"}}},
		{"help after -- is not help", []string{"get", "--", "-h"}, Args{Words: []string{"get"}}},
		{"--version", []string{"--version"}, Args{Version: true}},
		{"lone dash is a word", []string{"apply", "-"}, Args{Words: []string{"apply", "-"}}},
		{"use-context", []string{"config", "use-context", "prod"}, Args{Words: []string{"config", "use-context", "prod"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseArgs(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseArgs(%q)\n got %+v\nwant %+v", tt.args, got, tt.want)
			}
		})
	}
}
