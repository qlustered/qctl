package cmdutil

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TypeArgsErrors decorates Args validators in c's tree to reject empty
// arguments and wrap untyped errors with exit 2, the command path and usage.
// Nil validators and errors already carrying a *qerrors.Error are preserved.
func TypeArgsErrors(c *cobra.Command) {
	if inner := c.Args; inner != nil {
		c.Args = func(cmd *cobra.Command, args []string) error {
			err := inner(cmd, args)
			if err == nil {
				for i, arg := range args {
					if arg == "" {
						err = fmt.Errorf("argument %d cannot be empty", i+1)
						break
					}
				}
			}
			var typed *qerrors.Error
			if err == nil || errors.As(err, &typed) {
				return err
			}
			msg := argsProblem(cmd, args, err) +
				"\nUsage: " + catalog.UsageLine(cmd) +
				argsHint(cmd, inner, args)
			return qerrors.Wrap(qerrors.ExitBadUsage, &argsError{msg: msg, err: err}, "")
		}
	}
	for _, sub := range c.Commands() {
		TypeArgsErrors(sub)
	}
}

// argsError is a validator's error with the message TypeArgsErrors renders.
type argsError struct {
	msg string
	err error
}

func (e *argsError) Error() string { return e.msg }
func (e *argsError) Unwrap() error { return e.err }

// argsProblem is the first line of the message: what was wrong, naming cmd.
func argsProblem(cmd *cobra.Command, args []string, err error) string {
	path := cmd.CommandPath()
	if len(args) > 0 && err.Error() == fmt.Sprintf("unknown command %q for %q", args[0], path) {
		return fmt.Sprintf("%s takes no arguments, got %q", path, args[0])
	}
	return path + ": " + err.Error()
}

// argsHint is the message's hint line with its leading newline, or "" when
// none applies.
func argsHint(cmd *cobra.Command, inner cobra.PositionalArgs, args []string) string {
	if len(args) == 0 {
		if list := catalog.ListCommand(cmd); list != nil {
			return "\nTo list them, run: " + catalog.UsageLine(list)
		}
		return ""
	}
	if hint := boolValueHint(cmd, args); hint != "" {
		return hint
	}
	if takesNone := inner(cmd, nil) == nil && inner(cmd, args[:1]) != nil; !takesNone {
		return ""
	}
	if show := catalog.ShowCommand(cmd); show != nil && catalog.ListCommand(show) == cmd {
		return "\nTo show one, run: " + show.CommandPath() + " " + args[0]
	}
	return ""
}

// boolValueHint explains a stray "true" or "false" in args: a boolean flag
// takes its value only after "=". It names the flag when exactly one boolean
// flag is set to true.
func boolValueHint(cmd *cobra.Command, args []string) string {
	for _, a := range args {
		if a != "true" && a != "false" {
			continue
		}
		var set []string
		cmd.Flags().Visit(func(f *pflag.Flag) {
			if f.Value.Type() == "bool" && f.Value.String() == "true" {
				set = append(set, f.Name)
			}
		})
		name := "flag"
		if len(set) == 1 {
			name = set[0]
		}
		return "\nBoolean flags take a value only as --" + name + "=" + a
	}
	return ""
}
