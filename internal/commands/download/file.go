package download

import (
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/filedl"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// fileOptions is `download file`'s flag surface.
type fileOptions struct {
	output string
	force  bool
	noWait bool
}

// NewFileCommand creates the 'download file' command
func NewFileCommand() *cobra.Command {
	opts := &fileOptions{}

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Download the original raw file",
		Long: `Download the original raw file from backup storage.

The file will be downloaded to the current directory using its original filename,
or to a custom path if --output is specified.

A GPG-encrypted backup cannot be served directly: the server queues a decrypt
and qctl waits for the prepared file, which arrives as a .zip under a name of
the server's choosing. --no-wait prints the server's note and returns instead.

Examples:
  # Download to current directory
  qctl download file 123

  # Download to specific path
  qctl download file 123 --output /path/to/file.csv

  # Overwrite existing file
  qctl download file 123 --force

  # Queue an encrypted backup's decrypt and return without waiting for it
  qctl download file 123 --no-wait`,
		Annotations: catalog.For("file"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runDownload(cmd, args[0])
		},
	}

	addDownloadFlags(cmd, opts)
	return cmd
}

func (o *fileOptions) runDownload(cmd *cobra.Command, arg string) error {
	// Parse stored item ID
	storedItemID, err := strconv.Atoi(arg)
	if err != nil {
		// Exit 2, not 1: every client-side argument validation failure in qctl
		// is a usage error. This line used to be a bare fmt.Errorf.
		return qerrors.NewBadUsagef("invalid file ID '%s': must be a number", arg)
	}

	pollEvery, waitBudget, err := o.validateWaitFlags(cmd)
	if err != nil {
		return err
	}

	// Refuse a destination we could not write BEFORE anything reaches the
	// network. On the encrypted branch GetDownloadURL queues a server-side
	// decrypt, so discovering the collision inside filedl.ToFile would waste
	// that whole job AND the entire --wait-timeout first -- the same argument
	// `download data` makes above its own POST. On the synchronous branch this
	// is the check filedl.ToFile already makes, just earlier and cheaper.
	if err := preflightDestination(o.output, o.force); err != nil {
		return err
	}

	// Bootstrap auth context
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken

	// Create client
	storedItemsClient, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	notifClient := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	// Snapshot BEFORE the call that queues the decrypt: for a GPG-encrypted
	// backup GetDownloadURL is itself what publishes that job (atlas
	// get_download_link_for_original_stored_item,
	// capi/capi/views/storage_view.py), so a snapshot taken afterwards
	// can already carry the job's own announcement, which the wait would then
	// never accept as new. On an unencrypted file this costs one listing we
	// did not need; that is the price of not being able to know which branch
	// we are on until the answer comes back.
	//
	// Its FAILURE is deliberately held rather than returned. Before this
	// change `download file` touched no notification endpoint at all, and an
	// ordinary unencrypted download must not start failing because the inbox
	// listing is unavailable or forbidden -- an external-role user reaches
	// this command through check_external_user (atlas
	// get_download_link_for_original_stored_item,
	// capi/capi/views/storage_view.py) and need not be able to list
	// notifications. Only the encrypted branch needs the snapshot, so only the
	// encrypted branch reports the error.
	var snapshot notifications.FileReadySnapshot
	var snapErr error
	if !o.noWait {
		snapshot, snapErr = notifClient.SnapshotFileReady(token, notifications.ProducerDecrypt)
	}

	// Get download URL
	fmt.Fprintln(cmd.ErrOrStderr(), "Getting download URL...")
	urlResp, err := storedItemsClient.GetDownloadURL(token, storedItemID)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	// Display any message from backend
	if urlResp.Msg != nil && *urlResp.Msg != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s\n", terminal.Sanitize(*urlResp.Msg))
	}

	// An empty URL is deliberate, not a failure: for a GPG-encrypted backup
	// the server queues an async decrypt and answers with no URL and that note
	// (printed just above). Under --no-wait the note is the whole outcome, so
	// stop there rather than resolving an output path and handing "" to the
	// transport. Otherwise the decrypt announces the prepared file through the
	// same file_ready_to_download notification `download data` waits on, and
	// we collect it.
	if urlResp.URL == "" {
		if urlResp.Msg != nil && *urlResp.Msg != "" {
			if o.noWait {
				return nil
			}
			// Only here does a failed snapshot matter -- and here it is fatal:
			// WaitOptions.Snapshot documents that a snapshot which was never
			// TAKEN makes the FIRST row already in the inbox satisfy the
			// wait, which would hand back somebody else's file. (A snapshot
			// that was taken and came back EMPTY is fine: the inbox really
			// was empty, so every later row really is new.)
			if snapErr != nil {
				return fmt.Errorf("could not read the notification inbox, so the decrypted file cannot be collected: %w", snapErr)
			}
			return (&decryptedBackup{
				stored:    storedItemsClient,
				notif:     notifClient,
				token:     token,
				itemID:    storedItemID,
				snapshot:  snapshot,
				interval:  pollEvery,
				timeout:   waitBudget,
				output:    o.output,
				force:     o.force,
				verbosity: ctx.Verbosity,
			}).collect(cmd)
		}
		return fmt.Errorf("server returned no download URL for file %d", storedItemID)
	}

	return o.downloadDirect(cmd, storedItemsClient, token, storedItemID, urlResp)
}

// validateWaitFlags refuses the flag combinations that describe a wait which
// will not happen.
//
// --no-wait governs ONLY the encrypted branch here, so the two cadence flags
// are the only ones it contradicts: an unencrypted file still downloads
// synchronously, which makes `--no-wait --output ./x.csv` an ordinary working
// command line. This is deliberately NOT `download data`'s rule, where
// --no-wait downloads nothing at all and --output/--force are exit 2 --
// "making the two commands consistent" would break one of them.
func (o *fileOptions) validateWaitFlags(cmd *cobra.Command) (pollEvery, waitBudget time.Duration, err error) {
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"no-wait", "watch-interval"},
		[]string{"no-wait", "wait-timeout"},
	); err != nil {
		return 0, 0, err
	}
	return waitTimings(cmd)
}

// downloadDirect writes the file the server handed us a presigned URL for.
func (o *fileOptions) downloadDirect(cmd *cobra.Command, client *stored_items.Client, token string, storedItemID int, urlResp *stored_items.StoredItemURLResponse) error {
	// Resolve output path
	outputPath, err := resolveOutputPath(client, token, storedItemID, o.output)
	if err != nil {
		return err
	}

	// Download file. The path is echoed through terminal.Sanitize because with
	// no --output it IS the server's file_name: the bytes still go to the real
	// path, only the echo is made visible.
	fmt.Fprintf(cmd.ErrOrStderr(), "Downloading to %s...\n", terminal.Sanitize(outputPath))
	if err := client.DownloadFile(urlResp, outputPath, o.force); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully downloaded file to: %s\n", terminal.Sanitize(outputPath))
	return nil
}

// decryptedBackup is one wait for the file an async GPG decrypt prepares: the
// arguments collect needs, gathered so the encrypted branch reads as one call.
type decryptedBackup struct {
	stored *stored_items.Client
	notif  *notifications.Client
	token  string
	itemID int

	snapshot notifications.FileReadySnapshot
	interval time.Duration
	timeout  time.Duration

	output    string
	force     bool
	verbosity int
}

// collect waits for the decrypt's announcement, mints its URL, streams the
// bytes to disk and marks the announcement read.
func (d *decryptedBackup) collect(cmd *cobra.Command) error {
	// Only this branch needs the stored item, and only for its dataset_id --
	// the sole identity the announcement carries. Fetching it here rather than
	// earlier keeps the synchronous path's request count where it was.
	item, err := d.stored.GetStoredItem(d.token, d.itemID)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	n, err := awaitFile(cmd, d.notif, d.token, "decrypt", notifications.WaitOptions{
		DatasetID: item.DatasetID,
		// Advisory only: atlas names the prepared object
		// f"file_{stored_item.id}_{name}.zip"
		// (cmapper/cmapper/storage.py get_key_ready_for_direct_download),
		// which tells apart two decrypts of two files in one table.
		// matchReadyFile never REJECTS a candidate for failing the hint, so a
		// backend rename degrades this to the dataset test instead of hanging
		// the command. It is still the only thing that does tell two decrypts
		// apart: the handle below is null for this producer on every server.
		FileNameHint: fmt.Sprintf("file_%d_", d.itemID),
		// Rejects a same-table row from another producer: by
		// file_operation_action from atlas d117e8429 on, where anything but
		// put_original_qfile_for_download is refused outright, and on an older
		// server by the file name -- one named like an export or alert report
		// and not like a decrypt. A decrypt of orders.csv.gz,
		// file_<id>_orders.csv.zip, is named like both, so the older rule
		// collects it too.
		//
		// No DownloadHandle: the decrypt route computes no download key, so
		// atlas passes download_handle=None for this action
		// (cmapper/cmapper/file_operator.py) and the POST that queues it
		// answers with a StoredItemURLSchema, which has no such field.
		Producer: notifications.ProducerDecrypt,
		Snapshot: d.snapshot,
		Interval: d.interval,
		Timeout:  d.timeout,
	})
	if err != nil {
		return err
	}

	mint, err := d.notif.DownloadURL(d.token, n.ID)
	if err != nil {
		return err
	}

	outputPath, err := mintDestination(d.output, mint.Filename)
	if err != nil {
		return err
	}

	// filedl.ToFile maps "" to GET, so passing an absent method through
	// unchanged is correct.
	method := ""
	if mint.HTTPMethod != nil {
		method = *mint.HTTPMethod
	}
	// Sanitized echo only -- mint.Filename is the server's, and outputPath is
	// built from it whenever --output was not given.
	fmt.Fprintf(cmd.ErrOrStderr(), "Downloading to %s...\n", terminal.Sanitize(outputPath))
	if err := filedl.ToFile(filedl.TransferClient(), method, mint.URL, outputPath, d.force, d.verbosity); err != nil {
		return err
	}

	// A failure here must NOT fail the command: the file is already on disk
	// and is the deliverable. Nothing is dismissed -- read and dismissed are
	// independent axes.
	if err := d.notif.MarkRead(d.token, n.ID.String()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not mark the notification read: %s\n", terminal.Sanitize(err.Error()))
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully downloaded file to: %s\n", terminal.Sanitize(outputPath))
	return nil
}

// resolveOutputPath determines the output file path.
//
// With no --output that is the stored item's own file_name, which is a value
// the SERVER supplies: serverFileName reduces it to a single path element so
// the name cannot decide which directory the bytes land in. A --output is the
// user's own path and is used as typed -- the same split mintDestination
// makes, for the same reason.
func resolveOutputPath(client *stored_items.Client, token string, storedItemID int, output string) (string, error) {
	outputPath := output
	if outputPath == "" {
		// Fetch file info to get filename
		file, err := client.GetStoredItem(token, storedItemID)
		if err != nil {
			return "", fmt.Errorf("failed to get file info: %w", err)
		}
		if outputPath, err = serverFileName(file.FileName); err != nil {
			return "", err
		}
	}

	// Make sure path is absolute or relative to current directory
	return absoluteOutputPath(outputPath)
}

// addDownloadFlags adds flags for the download command.
func addDownloadFlags(cmd *cobra.Command, opts *fileOptions) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")
	cmd.Flags().StringVar(&opts.output, "output", "", "Output file path (default: use original filename)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite existing file")
	cmd.Flags().BoolVar(&opts.noWait, "no-wait", false, "For a GPG-encrypted backup, print the server's queued-decrypt note and return instead of waiting for the decrypted file")
	cmd.Flags().Int("watch-interval", 5, "Seconds between polls while waiting for a decrypted backup")
	cmd.Flags().Int("wait-timeout", 900, "Seconds to wait for a decrypted backup before giving up")
}
