package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/fileul"
)

// templateTransferTimeout is the deadline UploadTemplate gives the file
// transfer and FinalizeTemplate gives the finalize request, before
// client.ResolveTimeout applies --request-timeout.
const templateTransferTimeout = 300 * time.Second

const (
	templateUploadURLFailed = "create deliverable template upload URL failed"
	templateFinalizeFailed  = "finalize deliverable template failed"
)

// TemplateResult is a finalized template and its original response body.
type TemplateResult struct {
	*api.SkeletonFinalizeResponse
	Body json.RawMessage `json:"-"`
}

// templateExtension returns the lowercased text from the last dot of the
// text of filepath.Base(path) before its first "?": "" when that text has no
// dot, or only dots before its last one.
func templateExtension(path string) string {
	name, _, _ := strings.Cut(filepath.Base(path), "?")
	dot := strings.LastIndex(name, ".")
	if strings.Trim(name[:max(dot, 0)], ".") == "" {
		return ""
	}
	return strings.ToLower(name[dot:])
}

// ValidateTemplatePath returns a bad-usage error unless the extension of the
// text of path's base name before its first "?" is .xlsx or .xlsm, in any case.
func ValidateTemplatePath(path string) error {
	switch templateExtension(path) {
	case ".xlsx", ".xlsm":
		return nil
	}
	return qerrors.NewBadUsagef("%q is not a workbook template: its name must end in .xlsx or .xlsm", path)
}

// TemplateKeepsVBA reports whether the extension ValidateTemplatePath reads
// from path is .xlsm.
func TemplateKeepsVBA(path string) bool {
	return templateExtension(path) == ".xlsm"
}

// UploadTemplate mints a template upload URL, on the organization when
// revisionID is nil and on that revision otherwise, with filepath.Base(path)
// as original_filename and the file's size as file_size; sends the file to
// it; and returns the minted template id. A path that is a directory is a
// bad-usage error.
func (c *Client) UploadTemplate(token string, revisionID *uuid.UUID, path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("read template file: %w", err)
	}
	if info.IsDir() {
		return 0, qerrors.NewBadUsagef("%q is a directory, not a template file", path)
	}
	size := int(info.Size())
	request := api.SkeletonUploadURLRequest{OriginalFilename: filepath.Base(path), FileSize: &size}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return 0, err
	}
	var (
		status int
		body   []byte
		minted *api.SkeletonUploadURLResponse
	)
	operationID := "create_org_deliverable_skeleton_upload_url"
	if revisionID != nil {
		operationID = "create_deliverable_skeleton_upload_url"
	}
	if revisionID == nil {
		resp, err := apiClient.API.CreateOrgDeliverableSkeletonUploadURLWithResponse(context.Background(), c.orgID,
			&api.CreateOrgDeliverableSkeletonUploadURLParams{}, request)
		if err != nil {
			return 0, wrapAPIError(apiClient, err, templateUploadURLFailed, operationID)
		}
		status, body, minted = resp.StatusCode(), resp.Body, resp.JSON200
	} else {
		resp, err := apiClient.API.CreateDeliverableSkeletonUploadURLWithResponse(context.Background(), c.orgID, *revisionID,
			&api.CreateDeliverableSkeletonUploadURLParams{}, request)
		if err != nil {
			return 0, wrapAPIError(apiClient, err, templateUploadURLFailed, operationID)
		}
		status, body, minted = resp.StatusCode(), resp.Body, resp.JSON200
	}
	if status != http.StatusOK {
		return 0, handleError(status, body, templateUploadURLFailed, operationID)
	}
	if minted == nil {
		return 0, fmt.Errorf("unexpected empty deliverable template upload URL response")
	}
	target, err := uploadTarget(minted, body)
	if err != nil {
		return 0, err
	}
	httpClient := &http.Client{Timeout: client.ResolveTimeout(templateTransferTimeout)}
	if err := fileul.Upload(httpClient, target, path, c.verbosity); err != nil {
		return 0, fmt.Errorf("upload deliverable template %d: %w", minted.SkeletonID, err)
	}
	return minted.SkeletonID, nil
}

// uploadTarget returns the presigned request minted describes, with the
// fields of body's "fields" object as uploadFields converts them.
func uploadTarget(minted *api.SkeletonUploadURLResponse, body []byte) (fileul.Target, error) {
	target := fileul.Target{URL: minted.URL}
	if minted.HTTPMethod != nil {
		target.Method = *minted.HTTPMethod
	}
	var raw struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fileul.Target{}, fmt.Errorf("decode deliverable template upload URL: %w", err)
	}
	fields, err := uploadFields(raw.Fields)
	if err != nil {
		return fileul.Target{}, err
	}
	target.Fields = fields
	return target, nil
}

// uploadFields returns each field as text: a string's value, a number's JSON
// literal, or "true" or "false". A null field is left out, and an object or
// array is an error.
func uploadFields(raw map[string]json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	fields := make(map[string]string, len(raw))
	for key, value := range raw {
		value = bytes.TrimSpace(value)
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		switch value[0] {
		case '"':
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return nil, fmt.Errorf("decode upload URL field %q: %w", key, err)
			}
			fields[key] = text
		case '{', '[':
			return nil, fmt.Errorf("upload URL field %q is %s, not a string, number or boolean", key, value)
		default:
			fields[key] = string(value)
		}
	}
	return fields, nil
}

// FinalizeTemplate finalizes template id on the organization when revisionID
// is nil and on that revision otherwise, sending acknowledge_lossy as given.
// A 409 SKELETON_LOSSY refusal whose context names the template and its
// fidelity report is a *TemplateLossyError, which unwraps to the *APIError.
func (c *Client) FinalizeTemplate(token string, revisionID *uuid.UUID, id int, acknowledgeLossy bool) (*TemplateResult, error) {
	apiClient, err := c.newAPIClientWithin(token, templateTransferTimeout)
	if err != nil {
		return nil, err
	}
	request := api.SkeletonFinalizeRequest{SkeletonID: id, AcknowledgeLossy: &acknowledgeLossy}
	var (
		status    int
		body      []byte
		finalized *api.SkeletonFinalizeResponse
	)
	operationID := "finalize_org_deliverable_skeleton"
	if revisionID != nil {
		operationID = "finalize_deliverable_skeleton"
	}
	if revisionID == nil {
		resp, err := apiClient.API.FinalizeOrgDeliverableSkeletonWithResponse(context.Background(), c.orgID,
			&api.FinalizeOrgDeliverableSkeletonParams{}, request)
		if err != nil {
			return nil, templateLossyError(wrapAPIError(apiClient, err, templateFinalizeFailed, operationID))
		}
		status, body, finalized = resp.StatusCode(), resp.Body, resp.JSON200
	} else {
		resp, err := apiClient.API.FinalizeDeliverableSkeletonWithResponse(context.Background(), c.orgID, *revisionID,
			&api.FinalizeDeliverableSkeletonParams{}, request)
		if err != nil {
			return nil, templateLossyError(wrapAPIError(apiClient, err, templateFinalizeFailed, operationID))
		}
		status, body, finalized = resp.StatusCode(), resp.Body, resp.JSON200
	}
	if status != http.StatusOK {
		return nil, templateLossyError(handleError(status, body, templateFinalizeFailed, operationID))
	}
	if finalized == nil {
		return nil, fmt.Errorf("unexpected empty finalize deliverable template response")
	}
	return &TemplateResult{SkeletonFinalizeResponse: finalized, Body: body}, nil
}

// TemplateLossyError is a template finalize refused with SKELETON_LOSSY. Its
// text is the refusal's, one "  - " line per FidelityLossLines entry, and a
// line naming the template.
type TemplateLossyError struct {
	// TemplateID is the template the refusal's context names.
	TemplateID int
	// Report is the refusal's fidelity report.
	Report api.FidelityReport
	err    error
}

func (e *TemplateLossyError) Error() string {
	var b strings.Builder
	b.WriteString(e.err.Error())
	for _, line := range FidelityLossLines(e.Report) {
		b.WriteString("\n  - " + cellText(line))
	}
	fmt.Fprintf(&b, "\nThe file is uploaded as deliverable-template/%d, and this request attached nothing.", e.TemplateID)
	return b.String()
}

// Unwrap returns the refusal.
func (e *TemplateLossyError) Unwrap() error { return e.err }

// templateLossyError returns err as a *TemplateLossyError when its *APIError
// is a 409 SKELETON_LOSSY whose context decodes to a skeleton_id and a
// fidelity_report, and unchanged otherwise.
func templateLossyError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "SKELETON_LOSSY" {
		return err
	}
	var id int
	var report api.FidelityReport
	if !decodeContext(apiErr.Context["skeleton_id"], &id) || !decodeContext(apiErr.Context["fidelity_report"], &report) {
		return err
	}
	return &TemplateLossyError{TemplateID: id, Report: report, err: err}
}

// itemizedFidelity reports whether report is version 2 or later.
func itemizedFidelity(report api.FidelityReport) bool {
	return report.Version != nil && *report.Version >= 2
}

// FidelityLossLines returns the non-empty label of each of report's losses
// when it is version 2 or later, and its non-empty lost parts otherwise.
func FidelityLossLines(report api.FidelityReport) []string {
	var lines []string
	if itemizedFidelity(report) {
		for _, loss := range copied(report.Losses) {
			if loss.Label != "" {
				lines = append(lines, loss.Label)
			}
		}
		return lines
	}
	for _, part := range copied(report.LostParts) {
		if part != "" {
			lines = append(lines, part)
		}
	}
	return lines
}

// FormatTemplate renders a finalized template's signature, VBA and reuse
// flags, inventory, fidelity report and, when it has one, anchor report.
func FormatTemplate(r *TemplateResult) string {
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-25s %s\n", label+":", cellText(value)) }
	line("Signature", r.Signature)
	line("VBA kept", strconv.FormatBool(r.KeepVba))
	line("Reused existing template", strconv.FormatBool(r.ReusedExistingSkeleton != nil && *r.ReusedExistingSkeleton))
	formatInventory(&b, &r.WorkbookInventory)
	formatFidelity(&b, &r.FidelityReport)
	if r.AnchorReport != nil {
		formatAnchorReport(&b, r.AnchorReport)
	}
	return b.String()
}

func formatAnchorReport(b *strings.Builder, report *api.BindingAnchorReport) {
	fmt.Fprintf(b, "\nRe-anchored bindings (%d):\n", len(report.ColumnBindings)+len(report.CellBindings))
	for _, change := range report.ColumnBindings {
		formatAnchorChange(b, "Column", change.SheetName, ColumnLetter(change.FromColumnIndex), ColumnLetter(change.ToColumnIndex),
			change.Outcome, change.Reason, change.TargetLabel, change.ColumnBindingID)
	}
	for _, change := range report.CellBindings {
		formatAnchorChange(b, "Cell", change.SheetName, change.FromCellRef, change.ToCellRef,
			change.Outcome, change.Reason, change.TargetLabel, change.CellBindingID)
	}
}

func formatAnchorChange(b *strings.Builder, kind, sheet, from, to string, outcome api.BindingAnchorOutcome,
	reason *api.BindingAnchorBreak, label *string, id uuid.UUID) {
	position := sheet + "!" + from
	if to != from {
		position += " -> " + to
	}
	text := string(outcome)
	if reason != nil {
		text += " (" + string(*reason) + ")"
	}
	if label != nil && *label != "" {
		text += "; label: " + *label
	}
	fmt.Fprintf(b, "  %s %s: %s [%s]\n", kind, cellText(position), cellText(text), id)
}
