package download

import (
	"fmt"
	"os"
	"path/filepath"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// absoluteOutputPath anchors a destination to the working directory.
//
// Both download commands resolve a name they were handed -- `download file`
// the stored item's own file_name, `download data` the mint's filename -- and
// both must write it relative to where the user ran qctl, not to whatever
// directory the process happens to be in later. It lives here rather than in
// either command's file because both call it and neither owns it.
func absoluteOutputPath(outputPath string) (string, error) {
	if filepath.IsAbs(outputPath) {
		return outputPath, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return filepath.Join(cwd, outputPath), nil
}

// preflightDestination refuses a --output that already exists, before anything
// reaches the network.
//
// Both download commands ask the server to PRODUCE a file: `download data`
// enqueues an export, `download file` on a GPG-encrypted backup queues a
// decrypt. Starting a job we would then refuse to deliver wastes the whole
// job server-side -- and on the decrypt path it also wastes up to the full
// --wait-timeout before the user learns the destination was never writable.
// filedl.ToFile makes the same check -- before any request of its own, but
// only at the transfer step, by which point the server-side job is paid for --
// and its message is reproduced here VERBATIM: which of the two a user
// sees depends only on whether they passed --output, so they must not
// read as two different conditions.
//
// --force means the collision is not one, so there is nothing to pre-flight.
func preflightDestination(output string, force bool) error {
	if output == "" || force {
		return nil
	}
	if _, err := os.Stat(output); err == nil {
		return qerrors.NewBadUsagef("file already exists: %s (use --force to overwrite)", output)
	}
	return nil
}

// mintDestination resolves --output or the mint's filename in the working directory.
func mintDestination(output, minted string) (string, error) {
	outputPath := output
	if outputPath == "" {
		base, err := serverFileName(minted)
		if err != nil {
			return "", err
		}
		outputPath = base
	}
	return absoluteOutputPath(outputPath)
}

// serverFileName reduces a name the SERVER chose to a single path element.
//
// With no --output the destination IS the server's name, joined against the
// working directory, so the name decides where the bytes land: a name holding
// `../` would climb out of that directory and an absolute one would ignore it
// entirely. Nothing about the protocol makes that impossible -- atlas builds
// both names from values it controls, but qctl reaches whatever server the
// context points at, and a download is the one place a compromised or merely
// confused backend gets to name a local path.
//
// A --output is deliberately NOT passed through here. That path is the user's
// own, `qctl download data orders --output ../archive/orders.zip` is a
// legitimate thing to type, and collapsing it would refuse work the user asked
// for in order to defend them from themselves.
//
// The refusal covers the names filepath.Base answers with a non-name: "" and
// "." both yield ".", ".." yields "..", and each of those would resolve to a
// DIRECTORY and fail inside the transfer with an error about the path rather
// than about the server.
func serverFileName(name string) (string, error) {
	base := filepath.Base(name)
	switch base {
	case ".", "..", string(filepath.Separator):
		return "", fmt.Errorf("the server supplied an unusable file name %q; pass --output to choose the destination", name)
	}
	return base, nil
}
