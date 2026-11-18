package root

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// runRoot executes the real tree and restores the global command's state.
// api-resources and explain are offline: they need no server and no credential,
// and validation runs in PersistentPreRunE, before any RunE could reach out.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out bytes.Buffer
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	// The command cobra actually ran. It is not always root: cobra calls
	// InitDefaultHelpFlag and ParseFlags on the executed command, so that is
	// where --help and --version land.
	var executed *cobra.Command
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		_ = cmd.Flags().Set("output", "table")
		// pflag's Set always leaves Changed == true; without this, the first
		// test that passes -o pins the persistent flag Changed for the rest
		// of the binary, defeating ValidateOutputFormat's unpassed-flag guard.
		if f := cmd.Flags().Lookup("output"); f != nil {
			f.Changed = false
		}
		// Same hazard, two flags over, and it is why this helper uses
		// executeC, which returns the executed command like cobra's ExecuteC
		// and the help-flag rejection like this package's Execute. cobra's
		// execute() returns flag.ErrHelp whenever the help flag reads true
		// (cobra@v1.10.2/command.go) and prints the version and stops when
		// --version does, both before ValidateArgs and PersistentPreRunE.
		// A value left on the shared tree
		// therefore makes every later test in the binary succeed doing
		// nothing -- and the failures that produces name neither flag, they
		// read as product bugs. Resetting on root would not be enough:
		// after `get --help` it is get's help flag that is stuck true, and
		// root's is not even initialized.
		if executed == nil {
			return
		}
		for _, name := range []string{"help", "version"} {
			// Lookup can miss either one: cobra installs help only on the
			// command it runs, and --version only where Version is set.
			if f := executed.Flags().Lookup(name); f != nil {
				_ = f.Value.Set("false")
				f.Changed = false
			}
		}
	})
	executed, err := executeC()
	return out.String(), err
}

// What this helper does NOT cover, measured rather than assumed. It resets
// root's persistent output flag and the executed command's help/version, and
// nothing else. Still unguarded: verbose; a subcommand's *shadowing local*
// --output (download file binds its own StringVar, a different *Flag object
// than the persistent one reset above, so a future runRoot(t, "download",
// "file", "--output", ...) would leak it); and any test that drives the tree
// without going through here -- TestDownloadFileOutputPathIsNotValidated
// hand-rolls its own cleanup, while TestConfigFlagRedirectsConfigPath and
// TestExecute (root_test.go) leave state behind. Two measured leaks today,
// both inert: --config Changed=true, which nothing reads; and apply's local
// --filename, a StringP whose value and Changed both survive
// org_scope_test.go's runRootScoped(t, "apply", "-f", manifest) -- inert only
// because that is the one place in this package that runs apply. The real
// cure is a per-test command tree instead of this package singleton.

// The guard above, from both ends. Each half is two subtests rather than two
// statements because runRoot's cleanup runs when its *test* ends: a single
// function would still see the flag its own first call left behind. That also
// means the subtests must stay sequential -- do not add t.Parallel here, it
// would turn both halves into no-ops silently rather than failing.
func TestRunRootDoesNotLeakHelpOrVersionToTheNextTest(t *testing.T) {
	// The subcommand half. Root-level tests cannot see this one -- `qctl -o
	// bogus` still fails correctly after `get --help` -- so nothing else in
	// the package would catch a regression to a root-only reset.
	t.Run("help, on a subcommand", func(t *testing.T) {
		t.Run("get --help", func(t *testing.T) {
			if _, err := runRoot(t, "get", "--help"); err != nil {
				t.Fatalf("qctl get --help: %v", err)
			}
		})
		t.Run("get bogusresource", func(t *testing.T) {
			_, err := runRoot(t, "get", "bogusresource")
			if err == nil {
				t.Fatal(`qctl get bogusresource exited 0; get's help flag survived "get --help"`)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	})
	// The version half. Only root has a --version flag, so this one does
	// stick to root.
	t.Run("version, on root", func(t *testing.T) {
		t.Run("--version", func(t *testing.T) {
			if _, err := runRoot(t, "--version"); err != nil {
				t.Fatalf("qctl --version: %v", err)
			}
		})
		t.Run("bogustop", func(t *testing.T) {
			_, err := runRoot(t, "bogustop")
			if err == nil {
				t.Fatal(`qctl bogustop exited 0; root's version flag survived "--version"`)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	})
}

func TestUnknownOutputFormatExitsBadUsage(t *testing.T) {
	_, err := runRoot(t, "api-resources", "-o", "bogus")
	if err == nil {
		t.Fatal("qctl api-resources -o bogus exited 0")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `unknown output format "bogus"`) {
		t.Errorf("message = %q", err.Error())
	}
}

func TestKnownOutputFormatStillRenders(t *testing.T) {
	out, err := runRoot(t, "api-resources", "-o", "json")
	if err != nil {
		t.Fatalf("qctl api-resources -o json failed: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("expected JSON, got %.60q", out)
	}
}

// A subcommand that ever grows its own PersistentPreRunE must not silently
// disable validation for its whole subtree -- cobra runs only the closest hook
// unless this is set.
func TestTraverseRunHooksIsEnabled(t *testing.T) {
	if !cobra.EnableTraverseRunHooks {
		t.Error("cobra.EnableTraverseRunHooks is false; a subcommand hook would shadow the root's validation")
	}
}

// download file's --output is a file path, not a format.
func TestDownloadFileOutputPathIsNotValidated(t *testing.T) {
	cmd, _, err := GetRootCmd().Find([]string{"download", "file"})
	if err != nil {
		t.Fatalf("Find(download file): %v", err)
	}
	// download file's --output is bound by StringVar to the *string its RunE
	// reads; restore it so a later test executing download file doesn't pick
	// up this test's path.
	t.Cleanup(func() {
		if f := cmd.Flags().Lookup("output"); f != nil {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	if err := cmd.ParseFlags([]string{"--output", "/tmp/report.csv"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
		t.Errorf("download file --output was validated as a format: %v", err)
	}
}

// download data's --output is a destination path too. Its conflict rules
// diverge from download file's on purpose (--no-wait --output is exit 2 here),
// which is why it declares its own flags rather than sharing the helper -- but
// the shadowing of root's format flag is identical.
func TestDownloadDataOutputPathIsNotValidated(t *testing.T) {
	cmd, _, err := GetRootCmd().Find([]string{"download", "data"})
	if err != nil {
		t.Fatalf("Find(download data): %v", err)
	}
	// download data's --output is bound by StringVar to the *string its RunE
	// reads; restore it so a later test executing download data doesn't pick
	// up this test's path.
	t.Cleanup(func() {
		if f := cmd.Flags().Lookup("output"); f != nil {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	if err := cmd.ParseFlags([]string{"--output", "/tmp/rows.csv"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
		t.Errorf("download data --output was validated as a format: %v", err)
	}
}

func TestDownloadDeliverableRunOutputPathIsNotValidated(t *testing.T) {
	resetRunDownloadFlags(t)
	cmd, _, err := GetRootCmd().Find([]string{"download", "deliverable-run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--output", "/tmp/report.xlsx"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
		t.Errorf("download deliverable-run --output was validated as a format: %v", err)
	}
}

func TestOutputFlagCompletesTheValidValues(t *testing.T) {
	complete, ok := GetRootCmd().GetFlagCompletionFunc("output")
	if !ok {
		t.Fatal("--output has no completion function; the shell offers filenames")
	}
	values, _ := complete(GetRootCmd(), nil, "")
	if strings.Join(values, ",") != "table,json,yaml,name" {
		t.Errorf("completions = %v, want table,json,yaml,name", values)
	}
}

// The complete inventory of commands that declare their --output vocabulary.
// Every entry was measured by reading the command's
// fallback path: a fallback that ends in a printer supports all four, a
// bespoke one does not. Adding a line here is a claim about rendering, so the
// diff is the review.
//
// Note what this cannot catch: a command that should declare a set and does
// not. The test below compares declarations to this map in both directions,
// but a command with no annotation at all is invisible to both halves -- it
// simply inherits the four.
var declaredOutputFormats = map[string]string{
	"qctl get table-members":           "table,json,yaml",
	"qctl table-member set-permission": "table,json,yaml",
	"qctl table-member remove":         "table,json,yaml",
	"qctl table-member leave":          "table,json,yaml",
	"qctl api-resources":               "table,json,yaml",
	"qctl data-review approve":         "table,json,yaml",
	// reapply names no single group and comment prints a comment, so neither has a -o name.
	"qctl duplicate-group reapply":        "table,json,yaml",
	"qctl duplicate-group comment":        "table,json,yaml",
	"qctl data-review progress":           "table,json,yaml",
	"qctl data-review withdraw":           "table,json,yaml",
	"qctl apply":                          "table,json,yaml,name",
	"qctl apply backup-settings":          "table,json,yaml,name",
	"qctl apply cloud-source":             "table,json,yaml,name",
	"qctl apply destination":              "table,json,yaml,name",
	"qctl apply linked-table":             "table,json,yaml,name",
	"qctl apply table":                    "table,json,yaml,name",
	"qctl email-sender add":               "table,json,yaml,name",
	"qctl describe alert":                 "table,json,yaml",
	"qctl describe backup-settings":       "table,json,yaml",
	"qctl describe cloud-source":          "table,json,yaml",
	"qctl describe comment-thread":        "table,json,yaml",
	"qctl describe duplicate-group":       "table,json,yaml",
	"qctl describe composite-run-job":     "table,json,yaml",
	"qctl describe change-proposal":       "table,json,yaml", // an agent text payload; no name-only renderer
	"qctl describe composite-dry-run-job": "table,json,yaml",
	"qctl describe standard-field":        "table,json,yaml", // standard field detail has no name-only renderer
	"qctl describe data-review":           "table,json,yaml",
	"qctl describe deliverable":           "table,json,yaml",
	"qctl describe deliverable-revision":  "table,json,yaml",
	"qctl describe deliverable-run":       "table,json,yaml",
	"qctl describe destination":           "table,json,yaml",
	"qctl describe dry-run-job":           "table,json,yaml",
	"qctl describe error-incident":        "table,json,yaml",
	"qctl describe file":                  "table,json,yaml",
	"qctl describe ingestion-job":         "table,json,yaml",
	"qctl describe profiling-job":         "table,json,yaml",
	"qctl describe privilege-request":     "table,json,yaml",
	"qctl describe queue-message":         "table,json,yaml",
	"qctl describe row":                   "table,json,yaml",
	"qctl describe rule-merge":            "table,json,yaml",
	"qctl describe rule":                  "table,json,yaml",
	"qctl describe rule-review":           "table,json,yaml",
	"qctl describe rule-run-replay":       "table,json,yaml",
	"qctl describe rule-suggestion":       "table,json,yaml",
	"qctl describe table":                 "table,json,yaml",
	"qctl describe table-kind":            "table,json,yaml",
	"qctl describe table-rule":            "table,json,yaml",
	"qctl describe table-schema":          "table,json,yaml",
	"qctl describe table-series":          "table,json,yaml,name", // series detail also renders its bare reference
	"qctl describe warning":               "table,json,yaml",
	"qctl explain":                        "table,json,yaml",
	"qctl get data":                       "table,json,yaml",
	"qctl get standard-field":             "table,json,yaml", // standard field detail has no name-only renderer
	"qctl get table-series-suggestion":    "table,json,yaml", // a suggestion names no resource
	"qctl get data-review-changes":        "table,json,yaml",
	// -o name prints kind/<id>, and these list objects no command takes: a
	// table's review policy, audit entries, rule ignores.
	"qctl get data-review-policy":                "table,json,yaml",
	"qctl get notification-preferences":          "table,json,yaml",
	"qctl get slack-config":                      "table,json,yaml", // one organization-wide config, no id
	"qctl get privilege-audit":                   "table,json,yaml",
	"qctl get rule-ignores":                      "table,json,yaml",
	"qctl get deleted-rows":                      "table,json,yaml",
	"qctl get corrected-lines":                   "table,json,yaml",
	"qctl get dropped-lines":                     "table,json,yaml",
	"qctl get rule-review-policy":                "table,json,yaml",
	"qctl get change-review-policy":              "table,json,yaml", // manifest or rows; no name view, as for the other policies
	"qctl get rule":                              "table,json,yaml,code,name",
	"qctl get table-config":                      "table,json,yaml",
	"qctl get table-kind":                        "table,json,yaml",
	"qctl inspect composite-dry-run-job":         "json,yaml",
	"qctl inspect dry-run-job":                   "json,yaml",
	"qctl inspect deliverable":                   "table,json,yaml",
	"qctl deliverable accept-column-suggestions": "table,json,yaml",
	"qctl deliverable confirm-columns":           "table,json,yaml",
	"qctl deliverable confirm-cells":             "table,json,yaml",
	"qctl deliverable completeness":              "table,json,yaml",
	"qctl deliverable test-filter":               "table,json,yaml",
	"qctl deliverable-run drift":                 "table,json,yaml",
	"qctl deliverable-run readiness":             "table,json,yaml",
	"qctl redo cloud-source":                     "table,json,yaml",
	"qctl redo destination":                      "table,json,yaml",
	"qctl redo table":                            "table,json,yaml",
	// The one -o-aware verb in the rows group: a bespoke verdict renderer with
	// no name view, because the response carries no id or name to print and an
	// empty line at exit 0 would read as "unique".
	"qctl rows check-unique": "table,json,yaml",
	"qctl share table":       "table,json,yaml",
	// A local five-state report with no id or name in it, so -o name has
	// nothing to print; the other three are what makes it scriptable.
	"qctl skill status":      "table,json,yaml",
	"qctl undo cloud-source": "table,json,yaml",
	"qctl undo destination":  "table,json,yaml",
	"qctl undo table":        "table,json,yaml",
	// The issue and review verbs that create something addressed next print it:
	// a thread, a review, a submission, the alerts or warnings a submit made, a
	// suggestion. accept has no name view because a batch names no one object.
	"qctl alert submit":          "table,json,yaml,name",
	"qctl comment-thread create": "table,json,yaml,name",
	"qctl data-review request":   "table,json,yaml,name",
	"qctl review request":        "table,json,yaml,name",
	"qctl suggestion accept":     "table,json,yaml",
	"qctl suggestion edit":       "table,json,yaml,name",
	"qctl warning submit":        "table,json,yaml,name",
	// An audit entry has no name, email or id field, so -o name printed nothing.
	"qctl review audit": "table,json,yaml",
	// All four, declared so every leaf of these groups names its set.
	"qctl privilege approve": "table,json,yaml,name",
	"qctl privilege reject":  "table,json,yaml,name",
	"qctl privilege request": "table,json,yaml,name",
	// A reveal answers {value, event_id}: the event is no resource any
	// command takes, so there is no name to print.
	"qctl reveal cell":    "table,json,yaml",
	"qctl reveal alert":   "table,json,yaml",
	"qctl reveal warning": "table,json,yaml",
	// A proposal verb prints the proposal it created or changed, and its
	// name is the change-proposal/<uuid> every other proposal verb takes;
	// the decision verbs print the decided proposal the same way.
	"qctl alert propose":                   "table,json,yaml,name",
	"qctl change-proposal approve":         "table,json,yaml,name",
	"qctl change-proposal edit":            "table,json,yaml,name",
	"qctl change-proposal reject":          "table,json,yaml,name",
	"qctl change-proposal request-changes": "table,json,yaml,name",
	"qctl change-proposal rerun-dry-run":   "table,json,yaml,name",
	"qctl change-proposal submit":          "table,json,yaml,name",
	"qctl change-proposal withdraw":        "table,json,yaml,name",
	"qctl table-rule propose":              "table,json,yaml,name",
	"qctl warning propose":                 "table,json,yaml,name",
	// The issue and review verbs that report what they changed print the
	// server's answer, or one qctl builds where that answer is not safe to
	// print (alert and warning resolve). A result naming one object has a name
	// view; a cell verdict, a comment or a row batch names nothing a command
	// takes.
	"qctl alert resolve":                 "table,json,yaml,name",
	"qctl alert retract":                 "table,json,yaml,name",
	"qctl comment-thread edit-comment":   "table,json,yaml",
	"qctl comment-thread reply":          "table,json,yaml",
	"qctl comment-thread resolve":        "table,json,yaml,name",
	"qctl comment-thread unresolve":      "table,json,yaml,name",
	"qctl data-review accept-cell":       "table,json,yaml",
	"qctl data-review assign":            "table,json,yaml,name",
	"qctl data-review clear-cell":        "table,json,yaml",
	"qctl data-review close":             "table,json,yaml,name",
	"qctl data-review comment":           "table,json,yaml",
	"qctl data-review re-request":        "table,json,yaml,name",
	"qctl data-review unassign":          "table,json,yaml,name",
	"qctl data-review withdraw-cell":     "table,json,yaml",
	"qctl data-review-group assign":      "table,json,yaml,name",
	"qctl data-review-group cancel":      "table,json,yaml,name",
	"qctl data-review-group re-request":  "table,json,yaml,name",
	"qctl notification dismiss":          "table,json,yaml",
	"qctl notification read":             "table,json,yaml",
	"qctl notification set-preference":   "table,json,yaml",
	"qctl slack set-alert-type":          "table,json,yaml", // names alert types, not a resource
	"qctl queue-message resurrect-dead":  "table,json,yaml,name",
	"qctl queue-message retire":          "table,json,yaml,name",
	"qctl review approve":                "table,json,yaml,name",
	"qctl review assign":                 "table,json,yaml,name",
	"qctl review close":                  "table,json,yaml,name",
	"qctl review comment":                "table,json,yaml",
	"qctl review edit-comment":           "table,json,yaml",
	"qctl review re-request":             "table,json,yaml,name",
	"qctl review reject":                 "table,json,yaml,name",
	"qctl review reopen":                 "table,json,yaml,name",
	"qctl review request-changes":        "table,json,yaml,name",
	"qctl review unassign":               "table,json,yaml,name",
	"qctl review withdraw":               "table,json,yaml,name",
	"qctl rows delete-quarantined":       "table,json,yaml",
	"qctl rows hold":                     "table,json,yaml",
	"qctl rows ignore-rule":              "table,json,yaml",
	"qctl rows release":                  "table,json,yaml",
	"qctl rows reprocess-clean":          "table,json,yaml",
	"qctl rows reprocess-quarantined":    "table,json,yaml",
	"qctl rows reset-hold":               "table,json,yaml",
	"qctl rows search-replace":           "table,json,yaml",
	"qctl rows send-to-quarantine":       "table,json,yaml",
	"qctl rule-upgrade notify":           "table,json,yaml",
	"qctl table-rule recheck-clean-rows": "table,json,yaml",
	"qctl table-rule test-filter":        "table,json,yaml",
	"qctl table-rule move":               "table,json,yaml,name",
	"qctl warning resolve":               "table,json,yaml,name",
	"qctl warning retract":               "table,json,yaml,name",
	// The rest print prose alone, so table is the whole vocabulary.
	"qctl comment-thread delete-comment": "table",
	"qctl queue-message resurrect":       "table",
	"qctl queue-message retry":           "table",
	"qctl review delete-comment":         "table",
	"qctl review link-dry-run":           "table",
	"qctl rows set-cell":                 "table",
	"qctl rows unignore-rule":            "table",
	"qctl suggestion dismiss":            "table",
	// The current context as a document: the stored context plus the scope
	// --server/--org and QCTL_* select. There is no name view.
	"qctl config current-context": "table,json,yaml",
	"qctl info":                   "table,json,yaml",
	// All four, declared so every leaf of the local groups names its set.
	"qctl auth sessions": "table,json,yaml,name",
	// Rows of every context; -o name prints context/<name>, which the
	// commands that name a context take back.
	"qctl config list-contexts": "table,json,yaml,name",
	// Local documents with no name view: an alias and the client are no
	// kind any command takes.
	"qctl cache aliases list": "table,json,yaml",
	"qctl version":            "table,json,yaml",
	// A release and gc's report are no kind any command takes either.
	"qctl cache releases list": "table,json,yaml",
	"qctl cache releases gc":   "table,json,yaml",
	// The user is no kind any command takes, so there is no name view.
	"qctl auth me": "table,json,yaml",
	// The local and session commands that print prose, a script, a page or a
	// diff, so table is the whole vocabulary.
	"qctl auth login":            "table",
	"qctl auth logout":           "table",
	"qctl auth sessions revoke":  "table",
	"qctl auth switch-org":       "table",
	"qctl cache aliases clear":   "table",
	"qctl cache aliases resolve": "table",
	"qctl cache releases pin":    "table",
	"qctl cache releases unpin":  "table",
	"qctl completion bash":       "table",
	"qctl completion fish":       "table",
	"qctl completion install":    "table",
	"qctl completion powershell": "table",
	"qctl completion zsh":        "table",
	"qctl config delete-context": "table",
	"qctl config use-context":    "table",
	"qctl diff":                  "table",
	"qctl docs":                  "table",
	"qctl guide":                 "table",
	"qctl help":                  "table",
	"qctl skill install":         "table",
	"qctl skill uninstall":       "table",
	// The mutating verbs that render a document they already had: a plan, a
	// merge, a rekey or rebuild answer, a launched dry run.
	"qctl copy table":                   "table,json,yaml,name",
	"qctl copy cloud-source":            "table,json,yaml,name", // raw response, created source reference, or result line
	"qctl create composite-dry-run-job": "table,json,yaml,name",
	"qctl enable linked-table":          "table,json,yaml,name",
	"qctl enable deliverable":           "table,json,yaml,name",
	"qctl merge table-rules":            "table,json,yaml,name",
	"qctl rekey linked-table":           "table,json,yaml,name",
	"qctl run composite-run-job":        "table,json,yaml,name",
	// The mutating verbs that print what they created or changed
	// (qctl-3z8v.9.16). create dry-run-job's default with no -o is JSON.
	"qctl delete deliverable":     "table,json,yaml,name",
	"qctl create deliverable-run": "table,json,yaml,name",
	// The render request's answer, or deliverable-run/<id>; a wait's outcome goes to stderr under them.
	"qctl deliverable-run render": "table,json,yaml,name",
	// Waive prints the created record or its numeric id; unwaive's 204 has only a text acknowledgement.
	"qctl deliverable-run waive":   "table,json,yaml,name",
	"qctl deliverable-run unwaive": "table",
	// The overridden run or deliverable-run/<id>; the source's warning and the stale line go to stderr under them.
	"qctl deliverable-run override-source": "table,json,yaml,name",
	"qctl deliverable-run sign-off":        "table,json,yaml,name",
	"qctl deliverable-run mark-delivered":  "table,json,yaml,name",
	"qctl deliverable-run cancel":          "table,json,yaml,name",
	"qctl create dry-run-job":              "json,yaml,name",
	"qctl delete rules":                    "table,json,yaml,name",
	"qctl run rule-run-replay":             "table,json,yaml,name",
	"qctl submit rules":                    "table,json,yaml",
	"qctl submit table-kinds":              "table,json,yaml,name",
	"qctl undelete table":                  "table,json,yaml,name",
	"qctl undelete deliverable":            "table,json,yaml,name",
	"qctl upload file":                     "table,json,yaml,name",
	// The finalize response the server returned, or deliverable-template/<id>.
	"qctl upload deliverable-template": "table,json,yaml,name",
	// The server's answer plus its table's name, or file/<id>.
	"qctl file set-period": "table,json,yaml,name",
	// The answered replacement question, or file/<id>.
	"qctl file replace-older": "table,json,yaml,name",
	"qctl file keep-both":     "table,json,yaml,name",
	// The queued send's {result, send_id}, or file/<id>.
	"qctl file send-issues-report": "table,json,yaml,name",
	// Period locks print the changed member or its table/<id> reference.
	"qctl table-series lock":   "table,json,yaml,name",
	"qctl table-series unlock": "table,json,yaml,name",
	// The server's rule-set approval record, or table-series/<uuid>.
	"qctl table-series approve-rules": "table,json,yaml,name",
	// The server's period delete or recreate answer, or table/<id> of the
	// deleted or the new table.
	"qctl table-series delete-period":   "table,json,yaml,name",
	"qctl table-series recreate-period": "table,json,yaml,name",
	// The period's status after the waiver change; there is no reference to name.
	"qctl submission-status waive":   "table,json,yaml",
	"qctl submission-status unwaive": "table,json,yaml",
	// The mutating verbs that print prose, so table is the whole vocabulary.
	"qctl delete cloud-source":         "table",
	"qctl delete standard-field":       "table",
	"qctl delete data-review-policy":   "table",
	"qctl delete error-incident":       "table",
	"qctl delete file":                 "table",
	"qctl delete rule":                 "table",
	"qctl delete rule-review-policy":   "table",
	"qctl delete change-review-policy": "table",
	"qctl delete table-kind":           "table",
	"qctl delete table-series":         "table",
	"qctl delete deliverable-revision": "table",
	"qctl disable cloud-source":        "table",
	"qctl disable email-intake":        "table,json,yaml,name",
	"qctl disable rule":                "table",
	"qctl enable cloud-source":         "table",
	"qctl enable email-intake":         "table,json,yaml,name",
	"qctl enable rule":                 "table",
	"qctl kill ingestion-job":          "table",
	"qctl kill profiling-job":          "table",
	"qctl run ingestion-job":           "table",
	"qctl run profiling-job":           "table",
	"qctl run sensor-job":              "table",
	"qctl set-default rule":            "table",
	"qctl undelete cloud-source":       "table",
	"qctl undelete file":               "table",
}

func TestDeclaredOutputFormatsMatchTheInventory(t *testing.T) {
	got := map[string]string{}
	for _, n := range catalog.Walk(GetRootCmd()) {
		if raw := n.Cmd.Annotations[cmdutil.AnnotationOutputFormats]; raw != "" {
			got[n.Path] = raw
		}
	}
	for path, want := range declaredOutputFormats {
		if got[path] != want {
			t.Errorf("%s declares %q, want %q", path, got[path], want)
		}
	}
	for path, formats := range got {
		if _, expected := declaredOutputFormats[path]; !expected {
			t.Errorf("%s declares %q but is not in the inventory; add it (and say why in the diff)", path, formats)
		}
	}
}

// A verb with no declaration accepts all four formats, and the issue and review
// verbs that do not render one printed prose under -o json at exit 0. Every
// leaf of those groups must therefore name its set, so a new verb cannot
// silently join them.
func TestIssueAndReviewVerbsDeclareTheirFormats(t *testing.T) {
	requireDeclaredFormats(t, 68, []string{
		"alert", "warning", "comment-thread", "data-review", "review", "rows", "suggestion",
		"notification", "privilege", "queue-message", "rule-upgrade", "table-rule", "share",
	})
}

// The same for the local and session commands, which printed their prose,
// script or page under -o json at exit 0. config set-context is exempt: its
// own --output is the value it stores, not a format.
func TestLocalCommandsDeclareTheirFormats(t *testing.T) {
	requireDeclaredFormats(t, 31, []string{
		"auth", "cache", "config", "completion", "skill", "inspect",
		"info", "version", "api-resources", "docs", "explain", "guide", "diff", "help",
	})
}

// The same for the mutating verbs, which printed their prose under -o json at
// exit 0. download is exempt: its own --output is the file it writes.
func TestMutatingVerbsDeclareTheirFormats(t *testing.T) {
	requireDeclaredFormats(t, 51, []string{
		"apply", "copy", "create", "delete", "disable", "enable", "set-default", "download",
		"kill", "run", "merge", "redo", "undo", "undelete", "rekey", "submit", "upload",
		"deliverable", "deliverable-run", "file",
	})
}

// Each of these refuses before its RunE could print prose or change anything.
func TestMutatingVerbsNarrowedToTableRefuseJSON(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "file", "7", "--yes", "-o", "json"},
		{"enable", "rule", "check_email", "-o", "yaml"},
		{"kill", "ingestion-job", "7", "--yes", "-o", "name"},
		{"run", "sensor-job", "7", "-o", "json"},
		{"undelete", "file", "7", "--yes", "-o", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "valid: table") {
				t.Errorf("error = %q, want it to name table as the only format", err)
			}
			if out != "" {
				t.Errorf("stdout = %.80q, want nothing", out)
			}
		})
	}
}

// The narrowing reaches the real tree: each of these refuses before its RunE
// could print prose, a script or a page.
func TestLocalCommandsNarrowedToTableRefuseJSON(t *testing.T) {
	for _, args := range [][]string{
		{"cache", "aliases", "resolve", "r-1", "-o", "json"},
		{"help", "get", "-o", "json"},
		{"guide", "-o", "yaml"},
		{"docs", "-o", "name"},
		{"completion", "bash", "-o", "json"},
		{"auth", "login", "-o", "json"},
		{"config", "use-context", "dev", "-o", "yaml"},
		{"skill", "install", "claude-code", "-o", "json"},
		{"diff", "-f", "manifest.yaml", "-o", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "valid: table") {
				t.Errorf("error = %q, want it to name table as the only format", err)
			}
			if out != "" {
				t.Errorf("stdout = %.80q, want nothing", out)
			}
		})
	}
}

// requireDeclaredFormats fails for every runnable command under groups that
// declares no output formats, unless it registers its own --output. It also
// fails when the walk reaches fewer than want commands, so a renamed group
// cannot pass vacuously.
func requireDeclaredFormats(t *testing.T, want int, groups []string) {
	t.Helper()
	root := GetRootCmd()
	rootOutput := root.PersistentFlags().Lookup("output")
	reached := 0
	for _, n := range catalog.Walk(root) {
		if n.Resource == catalog.Dispatch {
			continue
		}
		for _, g := range groups {
			if n.Path != "qctl "+g && !strings.HasPrefix(n.Path, "qctl "+g+" ") {
				continue
			}
			if f := n.Cmd.Flags().Lookup("output"); f != nil && f != rootOutput {
				continue
			}
			reached++
			if n.Cmd.Annotations[cmdutil.AnnotationOutputFormats] == "" {
				t.Errorf("%s declares no output formats; add cmdutil.WithFormats and a declaredOutputFormats line", n.Path)
			}
		}
	}
	if reached < want {
		t.Errorf("reached %d commands in %v, want at least %d", reached, groups, want)
	}
}

// A typo in a declaration is invisible at runtime -- it just makes a valid
// value un-passable. Every declared value must be one a renderer implements.
func TestDeclaredOutputFormatsAreRenderable(t *testing.T) {
	renderable := map[string]bool{"table": true, "json": true, "yaml": true, "name": true, "code": true}
	for _, n := range catalog.Walk(GetRootCmd()) {
		for _, f := range cmdutil.SupportedOutputFormats(n.Cmd) {
			if !renderable[f] {
				t.Errorf("%s declares %q, which no renderer implements", n.Path, f)
			}
		}
	}
}

// The -v gate is only useful if something sets it. This asserts the root hook
// does, because every unit test in internal/apierror passes with the hook
// missing entirely.
func TestPersistentPreRunSetsApierrorVerbosity(t *testing.T) {
	t.Cleanup(func() {
		apierror.SetVerbosity(0)
		// verbose is a persistent Count on the global command; without this
		// it accumulates into every later test in the binary.
		_ = GetRootCmd().Flags().Set("verbose", "0")
		if f := GetRootCmd().Flags().Lookup("verbose"); f != nil {
			f.Changed = false
		}
	})
	apierror.SetVerbosity(0)

	if _, err := runRoot(t, "api-resources", "-vv"); err != nil {
		t.Fatalf("qctl api-resources -vv: %v", err)
	}
	if got := apierror.Verbosity(); got != 2 {
		t.Errorf("apierror.Verbosity() = %d, want 2", got)
	}
}
