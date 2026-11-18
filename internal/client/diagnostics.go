package client

import (
	"context"
	"io"
)

type diagnosticWriterKey struct{}

// WithDiagnosticWriter returns a context that directs client diagnostics to w.
func WithDiagnosticWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, diagnosticWriterKey{}, w)
}

// DiagnosticWriter returns ctx's diagnostic writer, or fallback when unset.
func DiagnosticWriter(ctx context.Context, fallback io.Writer) io.Writer {
	if w, ok := ctx.Value(diagnosticWriterKey{}).(io.Writer); ok && w != nil {
		return w
	}
	return fallback
}
