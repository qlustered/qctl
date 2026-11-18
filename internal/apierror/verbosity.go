package apierror

import "sync/atomic"

// verbosity is the process-wide -v count. Error rendering is reached from
// domain clients that carry no cobra command and no config, so the level
// cannot be threaded down as a parameter without touching every call site;
// this mirrors config.SetPathOverride, which the root command feeds the same
// way (internal/commands/root/root.go).
//
// atomic because it is process-global state written once from the root
// command and read later from an error path, on whatever goroutine that
// error is built on. No test in this package runs in parallel today; the
// atomic is not there for the tests.
var verbosity atomic.Int64

// SetVerbosity records the -v count. Called once per process from the root
// command's PersistentPreRunE, the single hook that runs for every command.
func SetVerbosity(v int) { verbosity.Store(int64(v)) }

// Verbosity reports the recorded -v count. Zero when nothing set it, which is
// the right default for a library used outside a command (tests, tools).
func Verbosity() int { return int(verbosity.Load()) }
