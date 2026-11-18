package download

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/filedl"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// alreadyQueuedFragment identifies the code-less refusal from older servers.
const alreadyQueuedFragment = "already queued for downloading"

func isAlreadyQueued(err error) bool {
	var opErr *apierror.OperationalError
	return err != nil && ((errors.As(err, &opErr) && opErr.ErrorCode == "ERR_QUEUE_KEY_CONFLICT") ||
		strings.Contains(err.Error(), alreadyQueuedFragment))
}

// exportOptions is the delivery half of a queued download's flag surface:
// where the file goes and whether to wait for it. `download data` and
// `download alert-report` share it, because both queue a server-side job and
// collect its file the same way.
type exportOptions struct {
	output string
	force  bool
	noWait bool
}

// NewDataCommand creates the 'download data' command.
func NewDataCommand() *cobra.Command {
	opts := &exportOptions{}

	cmd := &cobra.Command{
		Use:   "data <table-id-or-name>",
		Short: "Export a table's rows and download the zipped CSV",
		Long: `Export a table's rows and download the result.

The export runs on the server. By default qctl waits for it to finish and
writes the file; --no-wait queues it and returns.

--rule <slug> exports quarantine rows flagged by any instance of that rule family,
across releases and lifecycle states. It requires --is-bad-data and cannot be
combined with --dataset-rule-ids. Find family slugs with qctl get rules.

What lands on disk is a ZIP ARCHIVE holding one CSV: the server appends .zip
to the name it generates, and qctl writes the bytes as it receives them.
"download file" does not unzip a decrypted backup either.

Examples:
  # every clean row of a table, into the working directory
  qctl download data orders

  # quarantined rows only, to a path you pick -- the file is a zip
  qctl download data 42 --is-bad-data --output ./bad_rows.csv.zip

  # only the rows one alert holds
  qctl download data 42 --is-bad-data --alert-item-id 8817

  # rows flagged by any instance of a rule family
  qctl download data orders --is-bad-data --rule email-check

  # rows held for approval as possible duplicates; the CSV has no reason column
  qctl download data 42 --is-bad-data --held-for-approval --approval-hold-reason possible_duplicate

  # queue the export and return immediately
  qctl download data orders --no-wait`,
		Annotations: cmdutil.WithArgKinds(catalog.For("data"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runExport(cmd, args[0])
		},
	}

	cmd.Long += "\n\nUse --shared for a table shared from another organization.\n--rule is not supported for shared tables; use --dataset-rule-ids instead."
	registerExportFlags(cmd)
	cmdutil.AddDataRuleFlag(cmd)
	cmdutil.AddSharedDataFlag(cmd)
	opts.registerFlags(cmd, "the zipped CSV", "export")
	return cmd
}

// registerFlags declares the five delivery flags. file names what lands on
// disk and job what is queued, both for the help text.
//
// A LOCAL --output shadows root's persistent -o/--output. That is what
// exempts it from format validation -- ownsRootOutputFlag compares flag
// pointers, so there is no list to add a command to.
//
// `download file` does not use this on purpose: its --no-wait governs only its
// encrypted branch, so `--no-wait --output` is an ordinary working command
// line there and exit 2 here. One shared helper would hand each command the
// other's rule.
func (o *exportOptions) registerFlags(cmd *cobra.Command, file, job string) {
	cmd.Flags().StringVar(&o.output, "output", "", "Destination file path for "+file+" (default: the server's filename, in the working directory)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Overwrite an existing file")
	cmd.Flags().BoolVar(&o.noWait, "no-wait", false, "Queue the "+job+" and return without waiting for the file")
	cmd.Flags().Int("watch-interval", 5, "Seconds between polls while waiting")
	cmd.Flags().Int("wait-timeout", 900, "Seconds to wait for the file before giving up")
}

func (o *exportOptions) runExport(cmd *cobra.Command, target string) error {
	thirdParty, err := cmdutil.SharedData(cmd, "rule")
	if err != nil {
		return err
	}
	pollEvery, waitBudget, err := o.validate(cmd)
	if err != nil {
		return err
	}
	ruleSlug, err := cmdutil.DataRuleFlag(cmd)
	if err != nil {
		return err
	}

	// Also client-side: a malformed filter value or a quarantine-only option
	// without --is-bad-data is exit 2 with nothing enqueued. Atlas now guards
	// the generated options too; validating here names CLI flags and avoids
	// enqueuing a request the server can only refuse. --alert-item-id remains
	// the load-bearing exception: Atlas silently drops it on clean rows.
	if err := paramflags.ValidateDataFilterFlags(cmd, exportFlags()); err != nil {
		return err
	}
	params, err := buildExportParams(cmd)
	if err != nil {
		return err
	}
	queryOpts, err := exportQueryOptions(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(thirdParty)

	datasetID, err := dsClient.ResolveID(token, target)
	if err != nil {
		return err
	}
	if ruleSlug != "" {
		params.DatasetRuleIds, err = cmdutil.ResolveDataRuleIDs(ctx, datasetID, ruleSlug)
		if err != nil {
			return err
		}
	}

	return o.queueAndCollect(cmd, ctx, queuedJob{
		label:        "export",
		otherRequest: "change the filters to request a different one",
		send: func() (string, error) {
			return dsClient.PutDataForDownload(token, datasetID, params, queryOpts...)
		},
	}, notifications.WaitOptions{
		DatasetID: datasetID,
		// No FileNameHint: the export's file_name does not identify this
		// request -- from atlas b072ae28b two filtered exports of one kind on
		// one UTC day share it, and before that it hashed parameters qctl
		// never fully sees. What identifies it is the download_handle the POST
		// returns, which queueAndCollect fills in; without one the wait falls
		// back to which producer a row came from, and a `download file`
		// decrypt or an alert report on the same table announces into the same
		// inbox with the same dataset_id.
		Producer: notifications.ProducerExport,
		Interval: pollEvery,
		Timeout:  waitBudget,
	})
}

// validate refuses a delivery flag combination that cannot work and returns
// the wait's cadence. It makes no request.
func (o *exportOptions) validate(cmd *cobra.Command) (pollEvery, waitBudget time.Duration, err error) {
	// --no-wait downloads nothing and polls nothing, so a destination or a
	// cadence alongside it would be a lie. Refuse, never ignore -- the same
	// rule get data applies to --include-comment-threads under -o json.
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"no-wait", "output"},
		[]string{"no-wait", "force"},
		[]string{"no-wait", "watch-interval"},
		[]string{"no-wait", "wait-timeout"},
	); err != nil {
		return 0, 0, err
	}
	pollEvery, waitBudget, err = waitTimings(cmd)
	if err != nil {
		return 0, 0, err
	}

	// Pre-flight the destination BEFORE anything reaches the network.
	// Enqueuing a job we would then refuse to deliver wastes it server-side,
	// and resolving a table by NAME is itself a request -- so callers run this
	// above the bootstrap rather than merely above the POST.
	if err := preflightDestination(o.output, o.force); err != nil {
		return 0, 0, err
	}
	return pollEvery, waitBudget, nil
}

// waitTimings validates the two cadence flags and converts them.
//
// PositiveSecondsFlag returns an int of SECONDS; WaitOptions takes a
// time.Duration. time.Duration(interval) alone is NANOSECONDS -- a 5-second
// poll becomes 5ns and hammers the server. Multiply.
func waitTimings(cmd *cobra.Command) (pollEvery, waitBudget time.Duration, err error) {
	interval, err := cmdutil.PositiveSecondsFlag(cmd, "watch-interval")
	if err != nil {
		return 0, 0, err
	}
	timeout, err := cmdutil.PositiveSecondsFlag(cmd, "wait-timeout")
	if err != nil {
		return 0, 0, err
	}
	return time.Duration(interval) * time.Second, time.Duration(timeout) * time.Second, nil
}

// queuedJob is what differs between the commands that queue a server-side
// job and collect the file it announces.
type queuedJob struct {
	// label names the product in messages: "export", "alert report".
	label string
	// otherRequest is appended when an identical request is already queued
	// under --no-wait: how to ask for a different one.
	otherRequest string
	// queuedAdvice, when set, makes an already-queued answer fail with it
	// appended, waiting or not, instead of waiting without a download_handle.
	queuedAdvice string
	// send is the request that queues the job. It returns that job's
	// download_handle, or "" on a server before atlas d117e8429.
	send func() (string, error)
}

// queueAndCollect snapshots the inbox, queues the job, and -- unless --no-wait
// -- waits for its announcement and writes the file. wait.Snapshot is filled
// in here.
func (o *exportOptions) queueAndCollect(
	cmd *cobra.Command, ctx *cmdutil.CommandContext, job queuedJob, wait notifications.WaitOptions,
) error {
	token := ctx.Credential.AccessToken
	notifClient := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	// The snapshot must be taken BEFORE the request that produces the file.
	// Correlation is this snapshot against later polls; a rule that waits for a
	// greater id hangs forever, because a repeat of an identical request bumps
	// the existing row rather than inserting one.
	if !o.noWait {
		snapshot, err := notifClient.SnapshotFileReady(token, wait.Producer)
		if err != nil {
			return err
		}
		wait.Snapshot = snapshot
	}

	handle, err := o.enqueue(cmd.ErrOrStderr(), job)
	if err != nil {
		return err
	}
	// Set from the POST's answer rather than by the caller: only this side of
	// the request knows it, and it is what tells this request's file from
	// another one for the same table and producer. Empty leaves the wait on
	// its older correlation, which is the right answer in both cases that
	// produce one -- a server before atlas d117e8429, and the already-queued
	// refusal below, whose error body carries no handle.
	wait.DownloadHandle = handle
	if o.noWait {
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s queued. Re-running without --no-wait requests the %s again and waits for the file.\n",
			strings.ToUpper(job.label[:1]), job.label[1:], job.label)
		return nil
	}

	return o.collect(cmd, notifClient, token, ctx.Verbosity, job.label, wait)
}

// enqueue sends the job and decides what "already queued" means here, saying so
// on stderr when it waits anyway. It returns the queued job's download_handle,
// which is "" on every path that did not get a 200 back.
func (o *exportOptions) enqueue(stderr io.Writer, job queuedJob) (string, error) {
	handle, err := job.send()
	switch {
	case isAlreadyQueued(err) && job.queuedAdvice != "":
		return "", fmt.Errorf("%w\n\n%s", err, job.queuedAdvice)
	case isAlreadyQueued(err) && o.noWait:
		// Nothing to wait for, so it stays a failure -- with the one thing the
		// user can act on appended, and %w so the chain survives.
		return "", fmt.Errorf("%w\n\nwait for that %s to finish, or %s", err, job.label, job.otherRequest)
	case isAlreadyQueued(err):
		// The queue key is the object key, so "identical view already queued"
		// means a job writing this request's object is already QUEUED -- not
		// necessarily running yet -- and waiting collects that object. It is
		// the file asked for unless two different requests share a key, as two
		// long digest-named ones could before atlas d063ae87a (#1040) when
		// truncation cut off the digest. Note how short that window is: the
		// scheduler acknowledges the message as soon as it has deployed the job
		// (scheduler/queues/queue_run.py), and an acknowledged row no longer
		// blocks the partial unique index, so a repeat issued once the job is
		// actually running is not refused at all -- it enqueues a second
		// identical job, which is harmless for the same reason.
		fmt.Fprintf(stderr, "This %s was already queued; waiting for it to finish...\n", job.label)
		return "", nil
	}
	return handle, err
}

// collect waits for the announcement, mints its URL, streams the bytes to disk
// and marks the announcement read. label names the job, as awaitFile takes it.
func (o *exportOptions) collect(
	cmd *cobra.Command, client *notifications.Client, token string, verbosity int,
	label string, opts notifications.WaitOptions,
) error {
	n, err := awaitFile(cmd, client, token, label, opts)
	if err != nil {
		return err
	}

	mint, err := client.DownloadURL(token, n.ID)
	if err != nil {
		return err
	}

	outputPath, err := mintDestination(o.output, mint.Filename)
	if err != nil {
		return err
	}

	// filedl.ToFile maps "" to GET, so passing an absent method through
	// unchanged is correct.
	method := ""
	if mint.HTTPMethod != nil {
		method = *mint.HTTPMethod
	}
	if err := filedl.ToFile(filedl.TransferClient(), method, mint.URL, outputPath, o.force, verbosity); err != nil {
		return err
	}

	// A failure here must NOT fail the command: the file is already on disk and
	// is the deliverable. Nothing is dismissed -- read and dismissed are
	// independent axes.
	if err := client.MarkRead(token, n.ID.String()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not mark the notification read: %s\n", terminal.Sanitize(err.Error()))
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully downloaded file to: %s\n", terminal.Sanitize(outputPath))
	return nil
}

// awaitFile waits for a file under cmdutil.StartWait and appends request advice
// on timeout.
func awaitFile(
	cmd *cobra.Command, client *notifications.Client, token, job string, opts notifications.WaitOptions,
) (*notifications.Notification, error) {
	// SelfTimed because the loop enforces opts.Timeout itself, and its timeout
	// already exits 124. No Next: nothing collects a queued job's file by its
	// handle, so the only way back is a new request.
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    opts.Timeout,
		SelfTimed: true,
		Doing:     job + " queued; waiting for the file",
		Note:      "re-running this command requests the " + job + " again",
	})
	defer w.Stop()

	n, err := client.WaitForReadyFile(ctx, token, opts)
	if err = w.Ended(err); err == nil {
		return n, nil
	}
	if errors.Is(err, notifications.ErrWaitTimeout) {
		// One known way to time out while the file is in fact ready: the poll
		// reads a single page of at most notifications' snapshotLimit (100)
		// rows of the types the wait lists -- file_ready_to_download, and for
		// the broker issues report file_download_failed with them -- so in an
		// org where more than 100 NEWER such rows land between the snapshot
		// and ours, our row is pushed off page 1 and can never be seen. The ceiling is recorded here for the
		// next editor, who would otherwise go looking for the bug in the
		// matcher.
		return nil, fmt.Errorf("%w\n\nre-running this command requests the %s again",
			err, job)
	}
	return nil, err
}
