// Package filedl streams a URL to a local file.
//
// It is the one implementation shared by `download file` (a presigned
// stored-item URL, and the presigned URL minted for a decrypted backup) and
// `download data` (the presigned URL minted for an exported CSV). All of them
// need the same four behaviours: refuse an existing path unless forced, honour
// a non-GET method, log the request at -v, and stream rather than buffer.
//
// Nothing here knows about notifications, alerts or stored items. The URL is
// short-lived and minted by the caller immediately before the transfer, so
// this package never holds or re-uses one.
package filedl

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// TransferClient is the HTTP client for a bulk object transfer.
//
// Timeout is deliberately zero. An export is the whole matching set of rows
// (capped at ten million), so no fixed overall deadline is defensible -- one
// would abort a legitimate large download and leave the user with nothing.
//
// ResponseHeaderTimeout bounds only the wait for the response headers: a
// server that accepts the connection and then says nothing is cut off after a
// minute. It does NOT bound the body. A peer that answers 200 and then stalls
// mid-stream hangs in io.Copy indefinitely -- Timeout is 0, no read deadline
// is set, and ToFile takes no context, so there is no cancellation path here.
// A stall guard belongs with the caller that owns the wait timeout.
//
// This is NOT the client the API calls use; those go through internal/client
// and honour --request-timeout.
func TransferClient() *http.Client {
	// Not an unchecked assertion: internal/testutil swaps http.DefaultTransport
	// for its own mock RoundTripper (and some suites for a func type), so under
	// test this is not a *http.Transport and asserting blind would panic.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: transport}
}

// ToFile downloads url into outputPath.
//
// An existing outputPath without force is a usage error (exit 2), checked
// before any request is made.
//
// Whenever ToFile returns an error it leaves no file behind: the status is
// checked before the file is opened, which would otherwise truncate the target
// on the way to failing, and a copy error removes what was written. That guarantee
// covers returns only. A process killed mid-copy -- Ctrl-C on a long export,
// SIGTERM, a panic, a lost session -- never reaches the cleanup, so it does
// leave a truncated prefix at outputPath under the real filename.
func ToFile(httpClient *http.Client, method, url, outputPath string, force bool, verbosity int) error {
	if _, err := os.Stat(outputPath); err == nil && !force {
		return qerrors.NewBadUsagef("file already exists: %s (use --force to overwrite)", outputPath)
	}

	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if verbosity > 0 {
		fmt.Fprintf(os.Stderr, "→ %s %s (file download)\n", method, url)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if verbosity > 0 {
		// resp.Status carries the reason phrase the peer put on the status
		// line, verbatim -- net/http does not filter it -- and this is a
		// presigned URL at a storage host we do not control. The request line
		// above needs no pass: http.NewRequest already refused a URL holding an
		// ASCII control character.
		fmt.Fprintf(os.Stderr, "← %d %s\n", resp.StatusCode, terminal.Sanitize(resp.Status))
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("download failed with status %d (could not read response body)", resp.StatusCode)
		}
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// 0600, not os.Create's 0666 & umask (typically 0644). What lands here is
	// a full table export or a decrypted backup -- the most sensitive artifact
	// this CLI writes -- and the repo already holds its own state owner-only
	// through internal/pkg/privatefile. The mode applies on CREATE only: an
	// existing destination reached through --force keeps whatever mode it
	// already had, because O_TRUNC does not change one, and that file is the
	// user's rather than ours to re-permission.
	out, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		// O_TRUNC emptied the destination, so what is on disk now is a
		// prefix of the real file. Remove it rather than leave a short CSV
		// that looks complete.
		_ = os.Remove(outputPath)
		return fmt.Errorf("failed to write file: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}
