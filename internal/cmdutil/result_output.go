package cmdutil

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// ResultWriter routes a verb that returns what it created or changed.
//
// Under -o json, yaml or name, stdout carries one result document and Human is
// stderr. Under the table view, Human is stdout and Write prints nothing.
type ResultWriter struct {
	Human io.Writer

	cmd    *cobra.Command
	format output.Format
}

// NewResultWriter reads cmd's --output.
func NewResultWriter(cmd *cobra.Command) ResultWriter {
	format, _ := cmd.Flags().GetString("output")
	w := ResultWriter{Human: cmd.OutOrStdout(), cmd: cmd, format: output.Format(format)}
	if w.Machine() {
		w.Human = cmd.ErrOrStderr()
	}
	return w
}

// Machine reports whether stdout carries a result document rather than prose.
func (w ResultWriter) Machine() bool {
	switch w.format {
	case output.FormatJSON, output.FormatYAML, output.FormatName:
		return true
	}
	return false
}

// Write prints result through output.Printer under -o json or yaml, and each
// of names on its own line under -o name.
func (w ResultWriter) Write(result interface{}, names ...string) error {
	switch w.format {
	case output.FormatJSON, output.FormatYAML:
		printer, err := output.NewPrinterFromCmd(w.cmd)
		if err != nil {
			return err
		}
		return printer.Print(result)
	case output.FormatName:
		for _, name := range names {
			if _, err := fmt.Fprintln(w.cmd.OutOrStdout(), name); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteRun is Write for a run over total documents that returned docs: it
// writes an output.List of docs when total is more than one, docs[0] when docs
// holds exactly one, and nothing otherwise. Under -o name it prints names.
func (w ResultWriter) WriteRun(total int, docs []any, names ...string) error {
	if w.format == output.FormatName {
		return w.Write(nil, names...)
	}
	switch {
	case total > 1:
		return w.Write(output.NewList(docs, nil, nil, nil, nil), names...)
	case len(docs) == 1:
		return w.Write(docs[0], names...)
	}
	return nil
}
