package get

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// watchList used to hand its raw int to time.NewTicker, which panics on any
// non-positive duration. Every caller validates its own flag first, so this
// guard only fires for a caller that skipped that step — but a helper that
// panics on a value any caller can pass is a trap for the next --watch command.
func TestWatchListRejectsNonPositiveInterval(t *testing.T) {
	for _, seconds := range []int{0, -1} {
		called := false
		printer := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: io.Discard})
		err := watchList(&cobra.Command{}, printer, "jobs", lwWatch{intervalSeconds: seconds}, func(context.Context) (any, error) {
			called = true
			return nil, nil
		})
		if err == nil {
			t.Fatalf("watchList(%d) = nil, want an error", seconds)
		}
		if !strings.Contains(err.Error(), "watch interval must be at least 1 second") {
			t.Errorf("error should state the rule, got: %v", err)
		}
		if called {
			t.Errorf("watchList(%d) fetched before rejecting the interval", seconds)
		}
	}
}

func TestWatchPollRejectsNonPositiveInterval(t *testing.T) {
	stream := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: io.Discard}).NewStream(false, true)
	called := false
	err := watchPoll(context.Background(), stream, io.Discard, 0, func(context.Context) (any, error) {
		called = true
		return nil, nil
	})
	if err == nil || called {
		t.Fatalf("watchPoll(0) = %v, fetched = %t; want an error and no fetch", err, called)
	}
}

// The first fetch failing ends the watch with that error, even a transient
// one, before anything is written or retried.
func TestWatchPollReturnsTheFirstFetchError(t *testing.T) {
	var out, errOut bytes.Buffer
	stream := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: &out}).NewStream(false, true)
	fetches := 0
	failed := errors.New("server went away")
	err := watchPoll(context.Background(), stream, &errOut, time.Millisecond, func(context.Context) (any, error) {
		fetches++
		return nil, failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the fetch error", err)
	}
	if fetches != 1 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("fetches = %d, out = %q, errOut = %q; want one fetch and nothing written", fetches, out.String(), errOut.String())
	}
}

// lwFetchScript answers each fetch with the next of steps: a step with an err
// fails the fetch, any other returns a page holding one job in its state. The
// last step cancels the watch and answers every fetch from then on.
type lwFetchScript struct {
	steps  []lwFetchStep
	cancel context.CancelFunc
	calls  int
}

type lwFetchStep struct {
	state string
	err   error
}

func (s *lwFetchScript) fetch(context.Context) (any, error) {
	i := s.calls
	s.calls++
	if i >= len(s.steps) {
		s.cancel()
		i = len(s.steps) - 1
	}
	if err := s.steps[i].err; err != nil {
		return nil, err
	}
	return output.NewList([]watchJob{{ID: 1, State: s.steps[i].state}}, nil, nil, nil, nil), nil
}

// lwRunScript runs watchPoll at a 1ms interval over steps and returns stdout,
// stderr and the error.
func lwRunScript(t *testing.T, steps ...lwFetchStep) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut bytes.Buffer
	stream := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: &out}).NewStream(false, true)
	script := &lwFetchScript{steps: steps, cancel: cancel}
	err := watchPoll(ctx, stream, &errOut, time.Millisecond, script.fetch)
	return out.String(), errOut.String(), err
}

// A transient failure after the first poll is reported on stderr and retried
// with a growing wait, and the stream carries on once a poll succeeds; that
// success puts the wait back to the interval.
func TestWatchPollRetriesATransientFailure(t *testing.T) {
	failed := errors.New("server went away")
	out, errOut, err := lwRunScript(t,
		lwFetchStep{state: "queued"},
		lwFetchStep{err: failed},
		lwFetchStep{err: failed},
		lwFetchStep{state: "running"},
		lwFetchStep{err: failed},
		lwFetchStep{state: "finished"},
	)
	if err != nil {
		t.Fatalf("watchPoll = %v, want nil once the watch is cancelled", err)
	}
	want := "poll failed (server went away); retrying in 2ms\n" +
		"poll failed (server went away); retrying in 4ms\n" +
		"poll failed (server went away); retrying in 2ms\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"queued"`) ||
		!strings.Contains(lines[1], `"running"`) || !strings.Contains(lines[2], `"finished"`) {
		t.Errorf("stdout = %q, want the queued, running and finished pages", out)
	}
}

func TestWatchPollKeepsNoticesAboveTheNextTable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	stream := output.NewPrinter(output.Options{Format: output.FormatTable, Writer: &out, Columns: []string{"id", "state"}}).NewStream(true, true)
	script := &lwFetchScript{cancel: cancel, steps: []lwFetchStep{
		{state: "queued"},
		{err: errors.New("server went away")},
		{state: "queued"},
		{state: "running"},
		{state: "finished"},
	}}
	if err := watchPoll(ctx, stream, &out, time.Millisecond, script.fetch); err != nil {
		t.Fatal(err)
	}
	want := "ID  STATE\n1   queued\npoll failed (server went away); retrying in 2ms\n" +
		"ID  STATE\n1   running\n\033[2A\033[JID  STATE\n1   finished\n"
	if out.String() != want {
		t.Errorf("terminal = %q, want %q", out.String(), want)
	}
}

// A failure qerrors.IsPermanent reports, such as a 404, ends the watch with
// that error and its exit code instead of being retried.
func TestWatchPollEndsOnAPermanentFailure(t *testing.T) {
	gone := qerrors.FromHTTPStatusWithCause(http.StatusNotFound, errors.New("table 5 not found"), "failed to list dry-run jobs")
	out, errOut, err := lwRunScript(t,
		lwFetchStep{state: "queued"},
		lwFetchStep{err: gone},
		lwFetchStep{state: "running"},
	)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitNotFound)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want no retry line", errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `"queued"`) {
		t.Errorf("stdout = %q, want only the first page", out)
	}
}

// A watch that runs out of budget ends at exit 0, after the banner and a
// "timed out" line, both naming the command that resumes it.
func TestWatchListTimeoutEndsCleanWithTheResumeLine(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{Use: "qctl"}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	printer := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: &out})
	fetches := 0
	watch := lwWatch{intervalSeconds: 1, budget: 20 * time.Millisecond, next: "qctl get ingestion-jobs --watch"}
	err := watchList(cmd, printer, "ingestion jobs", watch, func(context.Context) (any, error) {
		fetches++
		return output.NewList([]watchJob{{ID: 1, State: "queued"}}, nil, nil, nil, nil), nil
	})
	if err != nil {
		t.Fatalf("watchList = %v, want nil", err)
	}
	want := "watching ingestion jobs every 1s, for up to 20ms; resume with: qctl get ingestion-jobs --watch\n" +
		"timed out after 20ms; resume with: qctl get ingestion-jobs --watch\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
	if fetches != 1 || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("fetches = %d, stdout = %q; want the first page alone", fetches, out.String())
	}
}

type watchJob struct {
	ID        int       `json:"id"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updated_at"`
}

// runScriptedWatch runs watchPoll over snapshots, each wrapped in the List a
// get list prints, cancelling the watch once the last one has been fetched,
// and returns what it wrote.
func runScriptedWatch(t *testing.T, format output.Format, columns []string, snapshots ...[]watchJob) string {
	t.Helper()
	var out bytes.Buffer
	printer := output.NewPrinter(output.Options{Format: format, Writer: &out, Columns: columns, NameKind: "ingestion-job"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetches := 0
	page, limit := 1, 10
	err := watchPoll(ctx, printer.NewStream(false, true), io.Discard, time.Millisecond, func(context.Context) (any, error) {
		i := min(fetches, len(snapshots)-1)
		fetches++
		if fetches > len(snapshots) {
			cancel()
		}
		total := len(snapshots[i])
		return listPage{page: &page, limit: &limit, total: &total}.list(snapshots[i]), nil
	})
	if err != nil {
		t.Fatalf("watchPoll: %v", err)
	}
	return out.String()
}

// watchScript is a baseline of two queued jobs, the same page again, job 1
// running, and job 1 gone.
func watchScript() [][]watchJob {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	queued1 := watchJob{ID: 1, State: "queued", UpdatedAt: at}
	queued2 := watchJob{ID: 2, State: "queued", UpdatedAt: at}
	running1 := watchJob{ID: 1, State: "running", UpdatedAt: at.Add(time.Minute)}
	return [][]watchJob{
		{queued1, queued2},
		{queued1, queued2},
		{running1, queued2},
		{queued2},
	}
}

// -o json writes one compact List per changed page, one per line.
func TestWatchPollJSONIsOneListPerChangedLine(t *testing.T) {
	out := runScriptedWatch(t, output.FormatJSON, nil, watchScript()...)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (baseline, running, gone):\n%s", len(lines), out)
	}
	wantItems := []int{2, 2, 1}
	for i, line := range lines {
		var list struct {
			Kind  string           `json:"kind"`
			Items []map[string]any `json:"items"`
			Total *int             `json:"total"`
		}
		if err := json.Unmarshal([]byte(line), &list); err != nil {
			t.Fatalf("line %d is not one JSON document: %v\n%s", i+1, err, line)
		}
		if list.Kind != "List" || len(list.Items) != wantItems[i] || list.Total == nil || *list.Total != wantItems[i] {
			t.Errorf("line %d = %s, want a List of %d", i+1, line, wantItems[i])
		}
	}
	if !strings.Contains(lines[1], `"running"`) {
		t.Errorf("line 2 = %s, want job 1 running", lines[1])
	}
}

// -o yaml writes each changed page as its own document.
func TestWatchPollYAMLIsOneDocumentPerChange(t *testing.T) {
	out := runScriptedWatch(t, output.FormatYAML, nil, watchScript()...)
	dec := yaml.NewDecoder(strings.NewReader(out))
	var docs []map[string]any
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decoding document %d: %v\n%s", len(docs)+1, err, out)
		}
		docs = append(docs, doc)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d documents, want 3:\n%s", len(docs), out)
	}
	for i, doc := range docs {
		if doc["kind"] != "List" {
			t.Errorf("document %d kind = %v, want List", i+1, doc["kind"])
		}
	}
}

// -o name prints every item of the baseline, then each item that changed or
// left the page.
func TestWatchPollNamePrintsChangedItems(t *testing.T) {
	out := runScriptedWatch(t, output.FormatName, nil, watchScript()...)
	if want := "ingestion-job/1\ningestion-job/2\ningestion-job/1\ningestion-job/1\n"; out != want {
		t.Errorf("-o name wrote %q, want %q (baseline 1 2, job 1 changed, job 1 gone)", out, want)
	}
}

// tickingCell renders as a new table cell each time it is formatted, as a
// relative time does when the clock moves, while its data stays the same.
type tickingCell struct{ renders *int }

func (c tickingCell) MarshalText() ([]byte, error) {
	*c.renders++
	return []byte(strconv.Itoa(*c.renders)), nil
}

func (c tickingCell) MarshalJSON() ([]byte, error) { return []byte(`"same"`), nil }

// Off a terminal a table is appended only when the page's data changes, so a
// cell whose text changes on its own does not reprint it.
func TestWatchPollTableAppendsOnlyChangedData(t *testing.T) {
	type row struct {
		ID   int         `json:"id"`
		Cell tickingCell `json:"cell"`
	}
	var out bytes.Buffer
	printer := output.NewPrinter(output.Options{Format: output.FormatTable, Writer: &out, Columns: []string{"id", "cell"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	renders, fetches := 0, 0
	err := watchPoll(ctx, printer.NewStream(false, true), io.Discard, time.Millisecond, func(context.Context) (any, error) {
		if fetches++; fetches == 3 {
			cancel()
		}
		return output.NewList([]row{{ID: 1, Cell: tickingCell{&renders}}}, nil, nil, nil, nil), nil
	})
	if err != nil {
		t.Fatalf("watchPoll: %v", err)
	}
	if n := strings.Count(out.String(), "CELL"); n != 1 {
		t.Errorf("table printed %d times, want 1:\n%s", n, out.String())
	}
}

// An empty baseline still prints: the header alone in a table, an empty List
// in json.
func TestWatchPollEmptyBaseline(t *testing.T) {
	table := runScriptedWatch(t, output.FormatTable, []string{"id", "state"}, []watchJob{})
	if strings.TrimSpace(table) != "ID  STATE" {
		t.Errorf("empty table = %q, want the header alone", table)
	}
	js := runScriptedWatch(t, output.FormatJSON, nil, []watchJob{})
	if !strings.Contains(js, `"items":[]`) {
		t.Errorf("empty json = %q, want an empty List", js)
	}
}

// lwListCommand is one `get` list command that takes --watch.
type lwListCommand struct {
	name     string
	build    func() *cobra.Command
	args     []string // what the command needs besides the flags under test
	endpoint string   // the listing request path
}

func lwListCommands() []lwListCommand {
	return []lwListCommand{
		{"ingestion-jobs", setupTestCommandForIngestion, nil, "/api/orgs/" + testOrgID + "/ingestion-jobs"},
		{"profiling-jobs", setupTestCommandForProfiling, nil, "/api/orgs/" + testOrgID + "/training-jobs"},
		{"queue-messages", setupTestCommandForQueueMessages, nil, "/api/orgs/" + testOrgID + "/queue-messages"},
		{"dry-run-jobs", setupTestCommandForDryRunJobs, []string{"--table-id", "5"}, "/api/orgs/" + testOrgID + "/datasets/5/dry-runs"},
	}
}

// lwRunListCommand runs `qctl get <c.name> <c.args> <args>` against a mock
// whose listing endpoint answers status, and returns stdout, stderr, every
// request the mock saw, and the command's error.
func lwRunListCommand(t *testing.T, c lwListCommand, status int, args ...string) (string, string, []string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/5", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 5, "name": "orders"})
	})
	mock.RegisterHandler("GET", c.endpoint, func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			testutil.RespondError(w, status, "not here")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	cmd := c.build()
	cmd.SetArgs(append(append([]string{"get", c.name}, c.args...), args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	return out.String(), errOut.String(), mock.Requests(), err
}

// --timeout bounds a watch, so without --watch it is refused before any
// request, as is a negative one.
func TestListWatchTimeoutUsageErrors(t *testing.T) {
	for _, c := range lwListCommands() {
		for _, args := range [][]string{
			{"--timeout", "5s"},
			{"--timeout", "0"},
			{"--watch", "--timeout", "-1s"},
		} {
			t.Run(c.name+" "+strings.Join(args, " "), func(t *testing.T) {
				_, _, requests, err := lwRunListCommand(t, c, http.StatusOK, args...)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
				}
				if len(requests) != 0 {
					t.Errorf("requests = %v, want none before the usage error", requests)
				}
			})
		}
	}
}

// The watch flags are settled before Bootstrap, so they exit 2 even with no
// config to load.
func TestListWatchUsageErrorsNeedNoLogin(t *testing.T) {
	for _, c := range lwListCommands() {
		for _, args := range [][]string{
			{"--timeout", "5s"},
			{"--watch", "--timeout", "-1s"},
		} {
			t.Run(c.name+" "+strings.Join(args, " "), func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()
				cmd := c.build()
				cmd.SetArgs(append(append([]string{"get", c.name}, c.args...), args...))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				err := cmd.Execute()
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
				}
			})
		}
	}
}

// A list watch announces itself on stderr, naming what it watches, how often,
// its budget and the command that resumes it, then ends at exit 0 with a
// "timed out" line when the budget is spent. The resume command carries the
// flags the user set and not the --columns the table view sets on its own.
func TestListWatchCommandTimesOutWithTheResumeLine(t *testing.T) {
	c := lwListCommands()[0]
	out, errOut, _, err := lwRunListCommand(t, c, http.StatusOK, "--watch", "--watch-interval", "1", "--timeout", "100ms")
	if err != nil {
		t.Fatalf("err = %v (exit %d), want nil", err, qerrors.GetExitCode(err))
	}
	resume := "qctl get ingestion-jobs --timeout 100ms --watch --watch-interval 1"
	want := "watching ingestion jobs every 1s, for up to 100ms; resume with: " + resume + "\n" +
		"timed out after 100ms; resume with: " + resume + "\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	if !strings.Contains(out, "DATASET-ID") {
		t.Errorf("stdout = %q, want the table header", out)
	}
}

// The first poll failing ends the watch with that poll's exit code: a 404 is
// exit 3, with no retry line.
func TestListWatchFirstPollNotFoundExitsThree(t *testing.T) {
	c := lwListCommands()[3]
	_, errOut, _, err := lwRunListCommand(t, c, http.StatusNotFound, "--watch", "--timeout", "5s")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitNotFound)
	}
	banner := "watching dry-run jobs every 2s, for up to 5s; resume with: qctl get dry-run-jobs --table-id 5 --timeout 5s --watch\n"
	if !strings.HasPrefix(errOut, banner) || strings.Contains(errOut, "poll failed") || strings.Contains(errOut, "timed out") {
		t.Errorf("stderr = %q, want the banner %q and no retry or timeout line", errOut, banner)
	}
}
