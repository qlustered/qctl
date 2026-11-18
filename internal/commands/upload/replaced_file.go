package upload

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
)

// codeReplacedFileChanged is the publish 409 for a replaces_stored_item_id
// that is not the loaded file the upload would replace.
const codeReplacedFileChanged = "REPLACED_FILE_CHANGED"

// maxReplacementPublishes bounds the publishes one upload sends after a
// REPLACED_FILE_CHANGED refusal.
const maxReplacementPublishes = 3

// confirmReplacementFunc asks whether the upload may replace replaced; changed
// reports that the server refused the previous answer because the loaded file
// changed.
type confirmReplacementFunc func(replaced *stored_items.ReplacedFile, changed bool) (bool, error)

// published is the outcome of publishUpload. replaces is the
// replaces_stored_item_id the accepted publish sent, nil when none; replaced
// is what the server reported for it, nil when its read failed.
type published struct {
	ingestionJobID *int
	replaces       *int
	replaced       *stored_items.ReplacedFile
}

// publishUpload publishes uploaded file storedItemID. When the server reports
// a loaded file it would replace, uc.replaces or uc.confirmReplacement decides
// whether the publish confirms that replacement; with neither, it publishes
// without confirming. A declined or mismatched replacement removes the
// unpublished file record and returns an error; a REPLACED_FILE_CHANGED
// refusal reads the replaced file again and decides again.
func publishUpload(out io.Writer, client *stored_items.Client, token string, storedItemID int, uc *uploadContext) (*published, error) {
	changed := false
	for attempt := 1; ; attempt++ {
		replaced, readErr := client.GetReplacedFile(token, storedItemID)
		replaces, err := decideReplacement(out, client, token, storedItemID, uc, replaced, readErr, changed)
		if err != nil {
			return nil, err
		}
		jobID, err := client.PublishStoredItemForIngestion(token, storedItemID, replaces)
		if err == nil {
			p := &published{ingestionJobID: jobID, replaces: replaces}
			if replaces != nil {
				p.replaced = replaced
			}
			return p, nil
		}
		if replaces == nil || apierror.ErrorCodeOf(err) != codeReplacedFileChanged || attempt >= maxReplacementPublishes {
			return nil, explainPublishFailure(client, token, storedItemID, err)
		}
		changed = true
	}
}

// decideReplacement returns the replaces_stored_item_id to publish with, nil
// for none, from the replaced-file read (replaced, readErr) and the upload's
// --replaces or prompt.
func decideReplacement(out io.Writer, client *stored_items.Client, token string, storedItemID int, uc *uploadContext,
	replaced *stored_items.ReplacedFile, readErr error, changed bool) (*int, error) {
	switch {
	case readErr != nil:
		fmt.Fprintf(out, "Warning: could not read which loaded file this upload replaces: %s\n", terminal.Sanitize(readErr.Error()))
		if uc.replaces != 0 {
			id := uc.replaces
			return &id, nil
		}
		return nil, nil
	case replaced == nil:
		if uc.replaces != 0 {
			fmt.Fprintf(out, "Note: this upload replaces no loaded file, so --replaces %d is not sent.\n", uc.replaces)
		}
		return nil, nil
	}
	writeWouldReplace(out, replaced, changed)
	id := replaced.ReplacedStoredItemID
	switch {
	case uc.replaces != 0:
		if id != uc.replaces {
			return nil, withRecordRemoved(client, token, storedItemID, qerrors.NewBadUsagef(
				"--replaces %d is not the loaded file this upload would replace in %s; it would replace %s (file/%d). "+
					"Inspect it with qctl describe file %d, then upload again with --replaces %d",
				uc.replaces, terminal.Sanitize(replaced.PeriodLabel), terminal.Sanitize(replaced.ReplacedFileName), id, id, id))
		}
		return &id, nil
	case uc.confirmReplacement == nil:
		fmt.Fprintf(out, "Note: publishing without confirming the replacement, so ingestion holds the file on a "+
			"preload_check_held alert. To confirm it without a prompt, upload with --replaces %d.\n", id)
		return nil, nil
	}
	yes, err := uc.confirmReplacement(replaced, changed)
	if err != nil {
		return nil, withRecordRemoved(client, token, storedItemID, err)
	}
	if !yes {
		return nil, withRecordRemoved(client, token, storedItemID, fmt.Errorf("upload cancelled: the file was not published"))
	}
	return &id, nil
}

// writeWouldReplace prints the loaded file an upload would replace.
func writeWouldReplace(out io.Writer, r *stored_items.ReplacedFile, changed bool) {
	label := terminal.Sanitize(r.PeriodLabel)
	if changed {
		fmt.Fprintf(out, "%s has a different loaded file now.\n", label)
	}
	fmt.Fprintf(out, "This file would replace %s in %s.\n", replacedFileText(r), label)
}

// replacedFileText renders a replaced file: its name, ID, load time and rows.
func replacedFileText(r *stored_items.ReplacedFile) string {
	about := fmt.Sprintf("file/%d", r.ReplacedStoredItemID)
	if r.ReplacedLoadedAt != nil {
		about += ", loaded " + timeutil.FormatRelative(*r.ReplacedLoadedAt)
	}
	if r.ReplacedRowCount != nil {
		about += fmt.Sprintf(", %d rows", *r.ReplacedRowCount)
	}
	return fmt.Sprintf("%s (%s)", displayName(r.ReplacedFileName), about)
}

// replacementPrompt is the question confirmReplacement asks.
func replacementPrompt(r *stored_items.ReplacedFile) string {
	return fmt.Sprintf("Replace %s's data with this file?", r.PeriodLabel)
}

// withRecordRemoved removes unpublished file record storedItemID and returns
// cause with the outcome of that removal appended.
func withRecordRemoved(client *stored_items.Client, token string, storedItemID int, cause error) error {
	return fmt.Errorf("%w\n\n%s", cause, removeUnpublishedRecord(client, token, storedItemID))
}

// removeUnpublishedRecord removes unpublished file record storedItemID and
// describes the outcome.
func removeUnpublishedRecord(client *stored_items.Client, token string, storedItemID int) string {
	if err := client.DeleteZombieStoredItem(token, storedItemID); err != nil {
		return fmt.Sprintf("Failed to remove unpublished file record %d: %s\nInspect it with: qctl describe file %d", storedItemID, err, storedItemID)
	}
	return fmt.Sprintf("Unpublished file record %d was removed; uploaded bytes remain in backup storage", storedItemID)
}
