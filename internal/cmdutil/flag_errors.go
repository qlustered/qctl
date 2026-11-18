package cmdutil

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/fuzzy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagSuggestionDistance is cobra's default SuggestionsMinimumDistance.
const flagSuggestionDistance = 2

// maxFlagSuggestions caps the flags one "Did you mean" names.
const maxFlagSuggestions = 3

// FlagError is a cobra FlagErrorFunc. It returns err with exit code 2 and
// err's message unchanged at the start. For an unknown flag it names the list
// command that has the flag or, for a long flag, the closest flags cmd has.
// The message ends by pointing at cmd's --help.
func FlagError(cmd *cobra.Command, err error) error {
	var hints []string
	var unknown *pflag.NotExistError
	if errors.As(err, &unknown) {
		if hint := unknownFlagHint(cmd, unknown); hint != "" {
			hints = append(hints, hint)
		}
	}
	hints = append(hints, fmt.Sprintf("Run '%s --help' for its flags.", cmd.CommandPath()))
	return qerrors.Wrap(qerrors.ExitBadUsage, fmt.Errorf("%w\n\n%s", err, strings.Join(hints, "\n")), "")
}

// RequiredFlagsError returns err, cobra's required-flag failure, with exit
// code 2 and cmd's usage line appended.
func RequiredFlagsError(cmd *cobra.Command, err error) error {
	return qerrors.Wrap(qerrors.ExitBadUsage, fmt.Errorf("%w\nUsage: %s", err, catalog.UsageLine(cmd)), "")
}

// unknownFlagHint returns FlagError's hint line for an unknown flag, or "".
func unknownFlagHint(cmd *cobra.Command, unknown *pflag.NotExistError) string {
	name := unknown.GetSpecifiedName()
	shorthand := unknown.GetSpecifiedShortnames() != ""
	if list := catalog.ListCommand(cmd); list != nil {
		spelled := "--" + name
		var found *pflag.Flag
		switch {
		case !shorthand:
			found = list.LocalFlags().Lookup(name)
		case len(name) == 1:
			spelled = "-" + name
			found = list.LocalFlags().ShorthandLookup(name)
		}
		if found != nil {
			return fmt.Sprintf("%s is a flag of '%s', which lists them.", spelled, list.CommandPath())
		}
	}
	if shorthand {
		return ""
	}
	if near := nearFlags(cmd.Flags(), name); len(near) > 0 {
		return "Did you mean " + orList(near) + "?"
	}
	return ""
}

// nearFlags returns up to three flags in fs that are neither hidden nor
// deprecated and whose names start with name or are within
// flagSuggestionDistance of it, ignoring case, spelled with their dashes.
// Prefix matches come first, then the closest; ties are alphabetical.
func nearFlags(fs *pflag.FlagSet, name string) []string {
	typed := strings.ToLower(name)
	type match struct {
		name string
		rank int
	}
	var matches []match
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		candidate := strings.ToLower(f.Name)
		rank := fuzzy.EditDistance(typed, candidate)
		if strings.HasPrefix(candidate, typed) {
			rank = 0
		}
		if rank <= flagSuggestionDistance {
			matches = append(matches, match{name: f.Name, rank: rank})
		}
	})
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].name < matches[j].name
	})
	var out []string
	for _, m := range matches {
		if len(out) == maxFlagSuggestions {
			break
		}
		out = append(out, "--"+m.name)
	}
	return out
}

// orList joins items as "a", "a or b", or "a, b or c".
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
