package releases

import (
	"strconv"
	"strings"
)

// Args is what ParseArgs reads from a raw command line.
type Args struct {
	// Config, Context and Server are the values of --config, --context and
	// --server; the last occurrence wins, as it does for the flags.
	Config, Context, Server string
	// Help is set by -h, --help or the help command.
	Help bool
	// Version is set by --version.
	Version bool
	// Words are the leading command words: the non-flag tokens before the
	// first flag that is not one of root's persistent flags.
	Words []string
}

// helpName is cobra's help flag and help command.
const helpName = "help"

// rootLongFlags maps each of root's persistent flags, plus cobra's help and
// version flags, to whether it takes a value.
var rootLongFlags = map[string]bool{
	"config":                  true,
	"context":                 true,
	"server":                  true,
	"user":                    true,
	"output":                  true,
	"columns":                 true,
	"max-column-width":        true,
	"request-timeout":         true,
	"retries":                 true,
	"org":                     true,
	"no-headers":              false,
	"allow-plaintext-secrets": false,
	"allow-insecure-http":     false,
	"verbose":                 false,
	helpName:                  false,
	"version":                 false,
}

// rootShortFlags is rootLongFlags for shorthands.
var rootShortFlags = map[byte]bool{
	'o': true,
	'O': true,
	'v': false,
	'h': false,
}

// ParseArgs reads args, the command line without the program name, the way
// root's flag parsing would, up to a bare "--".
func ParseArgs(args []string) Args {
	var a Args
	words := true
	for i := 0; i < len(args); i++ {
		tok := args[i]
		switch {
		case tok == "--":
			i = len(args)
		case tok == "-" || !strings.HasPrefix(tok, "-"):
			if words {
				a.Words = append(a.Words, tok)
			}
		case strings.HasPrefix(tok, "--"):
			i = a.parseLong(args, i, &words)
		default:
			i = a.parseShorthands(args, i, &words)
		}
	}
	if len(a.Words) > 0 && a.Words[0] == helpName {
		a.Help = true
	}
	return a
}

// parseLong reads the long flag at args[i] and returns the index of the last
// token it consumed.
func (a *Args) parseLong(args []string, i int, words *bool) int {
	name, val, hasVal := strings.Cut(args[i][2:], "=")
	takesValue, known := rootLongFlags[name]
	switch {
	case !known:
		*words = false
	case takesValue:
		if !hasVal {
			if i+1 >= len(args) {
				return i
			}
			i++
			val = args[i]
		}
		a.setValue(name, val)
	case name == helpName:
		a.Help = a.Help || !hasVal || isTrue(val)
	case name == "version":
		a.Version = a.Version || !hasVal || isTrue(val)
	}
	return i
}

// parseShorthands reads the shorthand cluster at args[i] and returns the index
// of the last token it consumed.
func (a *Args) parseShorthands(args []string, i int, words *bool) int {
	s := args[i][1:]
	for j := 0; j < len(s); j++ {
		takesValue, known := rootShortFlags[s[j]]
		switch {
		case !known:
			// The rest of the cluster may be this flag's value.
			*words = false
			return i
		case takesValue:
			if j+1 == len(s) && i+1 < len(args) {
				return i + 1
			}
			return i
		case s[j] == 'h':
			a.Help = true
		case j+1 < len(s) && s[j+1] == '=':
			// -v=7: the rest is the count.
			return i
		}
	}
	return i
}

func (a *Args) setValue(name, val string) {
	switch name {
	case "config":
		a.Config = val
	case "context":
		a.Context = val
	case "server":
		a.Server = val
	}
}

// isTrue reports whether s is a boolean flag value that parses as true.
func isTrue(s string) bool {
	b, err := strconv.ParseBool(s)
	return err == nil && b
}
