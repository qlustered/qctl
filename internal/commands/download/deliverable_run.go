package download

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/filedl"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

type runDownloadOptions struct {
	output string
	force  bool
}

// NewDeliverableRunCommand creates the download deliverable-run command.
func NewDeliverableRunCommand() *cobra.Command {
	opts := &runDownloadOptions{}
	cmd := &cobra.Command{
		Use:   "deliverable-run <id>",
		Short: "Download and verify a rendered deliverable run",
		Long: `Download a run's rendered spreadsheet and verify its SHA-256 signature.

The destination defaults to the server's filename in the current directory.
Use --output to choose a path and --force to overwrite an existing file.
A failed verification removes the downloaded file and exits 1.

Runs that are not signed off or delivered download as DRAFT, with a note on
stderr. A cancelled run can still have a rendered file to download.

Examples:
  qctl download deliverable-run 42
  qctl download deliverable-run deliverable-run/42 --output reports/bordereau.xlsx
  qctl download deliverable-run 42 --output reports/bordereau.xlsx --force`,
		Annotations: catalog.For("deliverable-run"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, err := deliverables.ParseRunID(args[0])
			if err != nil {
				return err
			}
			return opts.run(cmd, runID)
		},
	}
	cmd.Flags().StringVar(&opts.output, "output", "", "Output file path (default: use server filename)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite existing file")
	return cmd
}

func (o *runDownloadOptions) run(cmd *cobra.Command, runID int) error {
	if err := preflightDestination(o.output, o.force); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	run, err := c.GetRun(token, runID)
	if err != nil {
		return err
	}
	mint, err := c.GetRunDownloadURL(token, runID)
	if err != nil {
		return err
	}
	signature, err := expectedRunSignature(run)
	if err != nil {
		return err
	}
	return o.collect(cmd, mint, signature, ctx.Verbosity)
}

func expectedRunSignature(run *deliverables.Run) ([]byte, error) {
	if run.RenderedFileSignature == nil {
		return nil, fmt.Errorf("run %d has no rendered file signature to verify; retry the download after a successful render", run.ID)
	}
	signature, err := hex.DecodeString(*run.RenderedFileSignature)
	if err != nil || len(signature) != sha256.Size {
		return nil, fmt.Errorf("run %d has an invalid rendered file signature: expected 64 hexadecimal characters", run.ID)
	}
	return signature, nil
}

func (o *runDownloadOptions) collect(cmd *cobra.Command, mint *api.DownloadURLResponse, signature []byte, verbosity int) error {
	path, err := mintDestination(o.output, mint.Filename)
	if err != nil {
		return err
	}
	if mint.IsDraft {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: run %d downloads as DRAFT (state: %s).\n", mint.RunID, terminal.Sanitize(string(mint.RunState)))
	}
	method := ""
	if mint.HTTPMethod != nil {
		method = *mint.HTTPMethod
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Downloading to %s...\n", terminal.Sanitize(path))
	if err := filedl.ToFile(filedl.TransferClient(), method, mint.URL, path, o.force, verbosity); err != nil {
		return err
	}
	if err := verifyRunDownload(path, signature); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("%w; could not remove unverified file %s: %s", err, terminal.Sanitize(path), terminal.Sanitize(removeErr.Error()))
		}
		return fmt.Errorf("%w; downloaded file removed", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Successfully downloaded file to: %s\n", terminal.Sanitize(path))
	return nil
}

// verifyRunDownload checks the saved file against its expected SHA-256 digest.
func verifyRunDownload(path string, signature []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open downloaded file for verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("read downloaded file for verification: %w", err)
	}
	if !bytes.Equal(hash.Sum(nil), signature) {
		return fmt.Errorf("downloaded file SHA-256 does not match the run's rendered file signature; a concurrent render may have replaced the file, retry the download")
	}
	return nil
}
