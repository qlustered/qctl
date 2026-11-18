package deliverables

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/slug"
	"github.com/qlustered/qctl/internal/terminal"
)

const (
	createdDeliverableID = "<created-deliverable-id>"
	uploadedTemplateID   = "<uploaded-template-id>"

	keyCopyFromRevisionID = "copy_from_revision_id"
	keySkeletonID         = "skeleton_id"

	patchNote = "takes effect immediately: a deliverable's basic fields are not versioned"
)

// requestStep is the write a Request stands for.
type requestStep int

const (
	stepTemplateURL requestStep = iota + 1
	stepTemplateTransfer
	stepTemplateFinalize
	stepCreate
	stepPatch
	stepRevision
	stepSheetDelete
	stepSheetCreate
	stepSheetPatch
	stepColumnDelete
	stepColumnPatch
	stepColumnCreate
	stepCellDelete
	stepCellPatch
	stepCellCreate
)

// Request is one planned write, as diff prints it.
type Request struct {
	// Method is POST, PATCH, PUT, or DELETE.
	Method string
	// Path is the display path. Each <...> segment stands for an id apply
	// learns as it goes.
	Path string
	// Body is nil for the template transfer and for a DELETE.
	Body map[string]any
	// Note is one line printed under the request, "" for none.
	Note string
	// Sheet is the sheet name of a binding write; "" for any other request.
	Sheet string
	// Column is the 1-based target index of a column binding write; 0 for
	// any other request.
	Column int
	// SheetBindingID and ColumnBindingID are the binding ids the request
	// names; uuid.Nil for none, or until apply learns it.
	SheetBindingID  uuid.UUID
	ColumnBindingID uuid.UUID
	CellBindingID   uuid.UUID
	Cell            string
	mirrorSheet     string
	step            requestStep
	// key is the manifest key path a binding write makes the release match.
	key string
	// stored is the id, in the tree it was planned against, of the binding
	// the request deletes or patches.
	stored uuid.UUID
}

// Change is one basic field the PATCH request changes.
type Change struct {
	Field    string
	Old, New any
}

// Plan is every write applying a manifest makes, in order.
type Plan struct {
	Slug string
	// Current is the deliverable as read, nil when apply creates it.
	Current *Deliverable
	// Release is spec.release, "" when the manifest has none.
	Release string
	// Revision is the existing revision Release names, nil otherwise.
	Revision *Revision
	// CopyFrom is the enabled revision a created release copies, nil otherwise.
	CopyFrom *Revision
	// TemplatePath is the template file apply uploads, "" for none.
	TemplatePath     string
	AcknowledgeLossy bool
	// Requests are the writes in order; none means unchanged.
	Requests []Request
	// Changes are the basic fields the PATCH request changes.
	Changes []Change
	// Notes are printed after the requests.
	Notes []string
	// Tree is the binding tree used to plan binding writes.
	Tree *api.DeliverableRevisionResponse
	// Removals labels each planned binding DELETE, e.g. `sheet "Data"` or
	// `sheet "Data" column C`.
	Removals []string
	// MayRemove reports that the server may re-anchor the column bindings of
	// a sheet whose columns spec.sheets lists before apply writes them.
	MayRemove bool
	// MayRemoveCells reports that replacing a copied template may move cells
	// before apply reconciles their targets.
	MayRemoveCells bool
	// RowFilterCounts are the counts of the row filters the binding saves
	// send or keep, sheets by name and then cells by key.
	RowFilterCounts []RowFilterCount
	// RowFilterWarnings are the row filters that could not be counted or
	// checked, and the stored ones that read a column their table lacks.
	RowFilterWarnings []string
	// confirmations are the confirmation notes of Tree, printed after Notes.
	confirmations []string
	// sheets are spec.sheets resolved, sorted by name.
	sheets []plannedSheet
	// managesSheets reports that the manifest writes spec.sheets.
	managesSheets bool
	tableColumns  map[int]map[string]bool
	cells         []plannedCell
	managesCells  bool
}

// Prepare resolves references and plans m's writes. Relative template paths
// are resolved against manifestDir.
func (c *Client) Prepare(token string, m *Manifest, manifestDir string) (*Plan, error) {
	p := &Plan{Slug: m.Metadata.Slug, Release: m.Spec.Release}
	item, err := c.BySlug(token, m.Metadata.Slug)
	if err != nil {
		return nil, err
	}
	var deliverable *Request
	switch {
	case item == nil:
		generated := slug.Generate(m.Spec.Name, DeliverableSlugMax)
		if generated == "" {
			return nil, qerrors.NewBadUsagef("no deliverable has slug %q, and spec.name %q generates no slug to create it with; "+
				"give spec.name an ASCII letter or digit", terminal.Sanitize(m.Metadata.Slug), terminal.Sanitize(m.Spec.Name))
		}
		if generated != m.Metadata.Slug {
			return nil, qerrors.NewBadUsagef("no deliverable has slug %q, and a deliverable created from spec.name gets slug %q; "+
				"set metadata.slug to %q to create it", terminal.Sanitize(m.Metadata.Slug), generated, generated)
		}
		deliverable = createRequest(m)
	case item.ArchivedAt != nil:
		return nil, qerrors.NewBadUsagef("deliverable/%s is in the trash; restore it and apply again: qctl undelete deliverable %s",
			terminal.Sanitize(item.Slug), cmdutil.ShellArg(terminal.Sanitize(item.Slug)))
	default:
		if p.Current, err = c.Get(token, item.ID); err != nil {
			return nil, err
		}
		deliverable = p.patchRequest(m)
	}
	var template, bindings []Request
	var revision *Request
	if m.Has(keyRelease) {
		if template, revision, err = c.planRelease(token, p, m, manifestDir); err != nil {
			return nil, err
		}
	}
	if m.Has(keySheets) || m.Has(keyCells) {
		if bindings, err = c.planSheets(token, p, m); err != nil {
			return nil, err
		}
	}
	p.Requests = append(p.Requests, template...)
	for _, request := range []*Request{deliverable, revision} {
		if request != nil {
			p.Requests = append(p.Requests, *request)
		}
	}
	p.Requests = append(p.Requests, bindings...)
	return p, nil
}

// optional returns *s, or nil when s is nil.
func optional(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func createRequest(m *Manifest) *Request {
	body := map[string]any{keyName: m.Spec.Name, keyFilenamePattern: m.Spec.FilenamePattern}
	if m.Has(keyReceiverLabel) && m.Spec.ReceiverLabel != nil {
		body[keyReceiverLabel] = *m.Spec.ReceiverLabel
	}
	if m.Has(keyPeriodParam) {
		body[keyPeriodParam] = optional(m.Spec.PeriodParam)
	}
	return &Request{Method: http.MethodPost, Path: "/deliverables", Body: body, step: stepCreate}
}

// patchRequest records the basic fields m changes on p.Current and returns
// the PATCH that sends all four, or nil when none changes.
func (p *Plan) patchRequest(m *Manifest) *Request {
	current := p.Current
	label := current.ReceiverLabel
	if m.Has(keyReceiverLabel) && m.Spec.ReceiverLabel != nil {
		label = *m.Spec.ReceiverLabel
	}
	period := optional(current.PeriodParam)
	if m.Has(keyPeriodParam) {
		period = optional(m.Spec.PeriodParam)
	}
	body := map[string]any{}
	for _, change := range []Change{
		{Field: keyName, Old: current.Name, New: m.Spec.Name},
		{Field: keyReceiverLabel, Old: current.ReceiverLabel, New: label},
		{Field: keyFilenamePattern, Old: current.FilenamePattern, New: m.Spec.FilenamePattern},
		{Field: keyPeriodParam, Old: optional(current.PeriodParam), New: period},
	} {
		body[change.Field] = change.New
		if !reflect.DeepEqual(change.Old, change.New) {
			p.Changes = append(p.Changes, change)
		}
	}
	if len(p.Changes) == 0 {
		return nil
	}
	return &Request{Method: http.MethodPatch, Path: "/deliverables/" + current.ID.String(), Body: body, Note: patchNote, step: stepPatch}
}

// planRelease returns the writes spec.release needs: none for an existing
// release, which checkRevision checks, and otherwise the template upload when
// spec.template is set, and the revision POST.
func (c *Client) planRelease(token string, p *Plan, m *Manifest, manifestDir string) ([]Request, *Request, error) {
	var enabled *Revision
	if p.Current != nil {
		revisions, err := c.ListRevisions(token, p.Current.ID)
		if err != nil {
			return nil, nil, err
		}
		for i := range revisions {
			if revisions[i].Release == m.Spec.Release {
				p.Revision = &revisions[i]
			}
			if revisions[i].State == api.DeliverableRevisionStateEnabled {
				enabled = &revisions[i]
			}
		}
	}
	if p.Revision != nil {
		return nil, nil, p.checkRevision(m, manifestDir)
	}
	release := terminal.Sanitize(m.Spec.Release)
	body := map[string]any{keyRelease: m.Spec.Release}
	if m.Has(keyDescription) {
		body[keyDescription] = optional(m.Spec.Description)
	}
	note := fmt.Sprintf("creates draft release %q from scratch", release)
	if enabled != nil {
		p.CopyFrom = enabled
		body[keyCopyFromRevisionID] = enabled.ID
		note = fmt.Sprintf("creates draft release %q as a copy of enabled release %q", release, terminal.Sanitize(enabled.Release))
	}
	var template []Request
	if m.Has(keyTemplate) {
		var err error
		if template, err = p.templateRequests(m, manifestDir); err != nil {
			return nil, nil, err
		}
		body[keySkeletonID] = uploadedTemplateID
		body[keyAcknowledgeLossy] = p.AcknowledgeLossy
	}
	id := createdDeliverableID
	if p.Current != nil {
		id = p.Current.ID.String()
	}
	return template, &Request{Method: http.MethodPost, Path: "/deliverables/" + id + "/revisions", Body: body, Note: note, step: stepRevision}, nil
}

// checkRevision refuses a spec.description that p.Revision does not have, and
// notes that spec.template is not compared with it.
func (p *Plan) checkRevision(m *Manifest, manifestDir string) error {
	rev := p.Revision
	deliverable, release := terminal.Sanitize(p.Slug), terminal.Sanitize(rev.Release)
	state := terminal.Sanitize(string(rev.State))
	draft := rev.State == api.DeliverableRevisionStateDraft
	selector := cmdutil.ShellArg(deliverable) + " --release " + cmdutil.ShellArg(releaseSelector(rev))
	if m.Has(keyDescription) && !reflect.DeepEqual(optional(m.Spec.Description), optional(rev.Description)) {
		if draft {
			return qerrors.NewBadUsagef("spec.description differs from the description of deliverable/%s draft release %q, which apply does not change; "+
				"bump spec.release, or delete the draft and apply again: qctl delete deliverable-revision %s", deliverable, release, selector)
		}
		return qerrors.NewBadUsagef("spec.description differs from the description of deliverable/%s release %q, which is %s and cannot change; "+
			"bump spec.release", deliverable, release, state)
	}
	if !m.Has(keyTemplate) {
		return nil
	}
	if !draft {
		p.Notes = append(p.Notes, fmt.Sprintf("spec.template is not compared with the template of release %q, which is %s and cannot change; "+
			"bump spec.release to create a release with it", release, state))
		return nil
	}
	command := "qctl upload deliverable-template " + cmdutil.ShellArg(terminal.Sanitize(templatePath(m, manifestDir))) + " --deliverable " + selector
	if m.Spec.AcknowledgeLossy {
		command += " --acknowledge-lossy"
	}
	p.Notes = append(p.Notes, fmt.Sprintf("spec.template is not compared with the template of draft release %q; to replace it: %s", release, command))
	return nil
}

// releaseSelector returns rev's release when cellText leaves it unchanged,
// and rev's id otherwise.
func releaseSelector(rev *Revision) string {
	if cellText(rev.Release) == rev.Release {
		return rev.Release
	}
	return rev.ID.String()
}

// templatePath returns spec.template joined to manifestDir, or as written
// when it is absolute.
func templatePath(m *Manifest, manifestDir string) string {
	if filepath.IsAbs(m.Spec.Template) {
		return m.Spec.Template
	}
	return filepath.Join(manifestDir, m.Spec.Template)
}

// templateRequests checks the template file and returns the upload-URL mint,
// the transfer and the finalize. os.Stat's error is returned wrapped, and a
// directory is a bad-usage error.
func (p *Plan) templateRequests(m *Manifest, manifestDir string) ([]Request, error) {
	path := templatePath(m, manifestDir)
	if err := ValidateTemplatePath(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("spec.template: %w", err)
	}
	if info.IsDir() {
		return nil, qerrors.NewBadUsagef("%q is a directory, not a template file", path)
	}
	p.TemplatePath, p.AcknowledgeLossy = path, m.Spec.AcknowledgeLossy
	return []Request{
		{Method: http.MethodPut, Path: "/deliverable-skeletons/upload-url", step: stepTemplateURL,
			Body: map[string]any{"original_filename": filepath.Base(path), "file_size": info.Size()}},
		{Method: "<upload method>", Path: "<presigned upload URL>", step: stepTemplateTransfer,
			Note: "sends " + path + " to the URL, with the method, the upload-URL response names"},
		{Method: http.MethodPost, Path: "/deliverable-skeletons/finalize", step: stepTemplateFinalize,
			Body: map[string]any{keySkeletonID: uploadedTemplateID, keyAcknowledgeLossy: p.AcknowledgeLossy},
			Note: "the revision gets the template id this returns, an identical earlier upload's when there is one"},
	}, nil
}

// WriteRequests prints each request's method, path, note and JSON body, the
// changes before the PATCH request as field: old → new lines, then what
// WriteRowFilterCounts writes, and then p's notes and confirmation notes as
// "Note: " lines.
func (p *Plan) WriteRequests(out io.Writer) error {
	var b strings.Builder
	for _, request := range p.Requests {
		if request.step == stepPatch {
			for _, change := range p.Changes {
				fmt.Fprintf(&b, "%s: %s → %s\n", change.Field, cellText(jsonText(change.Old)), cellText(jsonText(change.New)))
			}
		}
		fmt.Fprintf(&b, "%s %s\n", request.Method, cellText(request.Path))
		if request.Note != "" {
			fmt.Fprintf(&b, "# %s\n", cellText(request.Note))
		}
		if request.Body != nil {
			var body bytes.Buffer
			encoder := json.NewEncoder(&body)
			encoder.SetEscapeHTML(false)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(request.Body); err != nil {
				return err
			}
			b.WriteString(terminal.Sanitize(body.String()))
		}
	}
	if err := p.WriteRowFilterCounts(&b); err != nil {
		return err
	}
	writeNotes(&b, slices.Concat(p.Notes, p.confirmations))
	_, err := io.WriteString(out, b.String())
	return err
}

// writeNotes writes each note as a "Note: " line.
func writeNotes(b *strings.Builder, notes []string) {
	for _, note := range notes {
		fmt.Fprintf(b, "Note: %s\n", cellText(note))
	}
}

// jsonText returns value as one line of JSON.
func jsonText(value any) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// ColumnBindingWarning is a warning returned by a column binding write.
type ColumnBindingWarning struct {
	Sheet  string
	Column int
	Issue  Issue
}

// ApplyResult is what Apply wrote.
type ApplyResult struct {
	// Deliverable is the create or PATCH response, or Plan.Current when
	// neither was sent.
	Deliverable *Deliverable
	Created     bool
	Updated     bool
	// Revision is the created release, nil when none was created.
	Revision *RevisionCreated
	// Template is the finalized template, nil when none was uploaded.
	Template *TemplateResult
	// AnchorReports are the anchor reports, in the order sent, of the sheet
	// PATCHes whose report lists a column binding.
	AnchorReports []*api.BindingAnchorReport
	// ColumnWarnings are column write warnings in request and response order.
	ColumnWarnings []ColumnBindingWarning
	// BindingWrites counts successful binding writes.
	BindingWrites int
	// Manifest is ExportManifest of Deliverable and the release Plan.Release
	// names, or FromAPI of them when Apply fails.
	Manifest *Manifest
	// writes counts the binding writes that succeeded, by step.
	writes map[requestStep]int
	// applied reports that Apply returned the result.
	applied bool
	// confirmations are the confirmation notes of the release as read after
	// the writes.
	confirmations []string
	sheetIDs      map[string]uuid.UUID
}

// Apply sends p's requests in order, replans from responses, and reads back
// the manifest. It returns a partial result when a failure follows a write.
func (c *Client) Apply(token string, p *Plan) (*ApplyResult, error) {
	r := &ApplyResult{Deliverable: p.Current, applied: true}
	r.sheetIDs = map[string]uuid.UUID{}
	if p.Tree != nil && p.isTarget(p.Tree) {
		r.recordSheets(p.Tree)
	}
	requests := slices.Clone(p.Requests)
	minted := 0
	for i := 0; i < len(requests); i++ {
		next, err := c.send(token, p, requests[i], r, &minted)
		if err != nil {
			return p.partial(r, p.stopped(requests, i, false, r, err))
		}
		if next == nil {
			continue
		}
		replanned, err := p.replanned(requests, i, next)
		if err != nil {
			return p.partial(r, p.stopped(requests, i, true, r, err))
		}
		requests = replanned
	}
	if r.Deliverable == nil {
		return r, nil
	}
	err := c.finish(token, p, r)
	return r, err
}

// partial returns r, with FromAPI's manifest, and err when the deliverable,
// the release or a binding was written, and nil and err otherwise.
func (p *Plan) partial(r *ApplyResult, err error) (*ApplyResult, error) {
	if !r.Created && !r.Updated && r.Revision == nil && r.BindingWrites == 0 {
		return nil, err
	}
	r.Manifest = FromAPI(r.Deliverable, p.resultRevision(r))
	return r, err
}

// send makes one request, records its response on r, and returns what the
// response re-plans, nil for nothing. The upload-URL step also sends the file,
// so the transfer step sends nothing.
func (c *Client) send(token string, p *Plan, request Request, r *ApplyResult, minted *int) (*replan, error) {
	if isBindingWrite(request) {
		return c.sendBinding(token, p, request, r)
	}
	var err error
	switch request.step {
	case stepTemplateURL:
		*minted, err = c.UploadTemplate(token, nil, p.TemplatePath)
	case stepTemplateFinalize:
		r.Template, err = c.FinalizeTemplate(token, nil, *minted, p.AcknowledgeLossy)
	case stepCreate:
		var created *Deliverable
		if created, err = c.Create(token, request.Body); err == nil {
			r.Deliverable, r.Created = created, true
		}
	case stepPatch:
		var patched *Deliverable
		if patched, err = c.Patch(token, p.Current.ID, request.Body); err == nil {
			r.Deliverable, r.Updated = patched, true
		}
	case stepRevision:
		body := maps.Clone(request.Body)
		if body[keySkeletonID] == uploadedTemplateID {
			if r.Template == nil {
				return nil, errors.New("no template was finalized for the revision's skeleton_id")
			}
			body[keySkeletonID] = r.Template.SkeletonID
		}
		if r.Revision, err = c.CreateRevision(token, r.Deliverable.ID, body); err == nil && (p.managesSheets || p.managesCells) {
			tree := createdTree(r.Revision.DeliverableRevisionCreateResponse)
			r.recordSheets(tree)
			return &replan{tree: tree}, nil
		}
	}
	return nil, err
}

// stopped adds the failed step and completed count to err after partial writes.
func (p *Plan) stopped(requests []Request, i int, sent bool, r *ApplyResult, err error) error {
	finish := "applying it again finishes it"
	var lossy *TemplateLossyError
	if errors.As(err, &lossy) {
		err = fmt.Errorf("%w\nspec.acknowledge_lossy: true accepts the listed losses", err)
		finish = "applying it again with spec.acknowledge_lossy: true finishes it"
	}
	at, completed := "at", i
	if sent {
		at, completed = "after", i+1
	}
	request := requests[i]
	if completed == 0 || request.step <= stepTemplateFinalize {
		return err
	}
	path := request.Path
	if r.Deliverable != nil {
		path = strings.ReplaceAll(path, createdDeliverableID, r.Deliverable.ID.String())
	}
	step := request.Method + " " + path
	if isBindingWrite(request) {
		label := sheetLabel(request.Sheet)
		if request.Column != 0 {
			label = columnLabel(request.Sheet, request.Column)
		} else if request.Cell != "" {
			label = cellLabel(request.Sheet, request.Cell)
		}
		step += " for " + label
	}
	return fmt.Errorf("deliverable/%s apply stopped %s step %d of %d (%s), %d completed: %w\n"+
		"qctl diff -f on the same manifest shows what is left, and %s",
		terminal.Sanitize(p.Slug), at, i+1, len(requests), step, completed, err, finish)
}

// resultRevision returns the revision r created, else p.Revision.
func (p *Plan) resultRevision(r *ApplyResult) *Revision {
	if r.Revision == nil || r.Revision.DeliverableRevisionCreateResponse == nil {
		return p.Revision
	}
	created := r.Revision
	return &Revision{
		ID: created.ID, Release: created.Release, State: created.State, Description: created.Description,
		SkeletonID: created.SkeletonID, EnabledAt: created.EnabledAt, EnabledByID: created.EnabledByID,
	}
}

// FormatApplyResult renders r's completed writes, binding warnings and notes.
func FormatApplyResult(p *Plan, r *ApplyResult) string {
	var b strings.Builder
	created := r.Revision != nil && r.Revision.DeliverableRevisionCreateResponse != nil
	if created {
		from := "from scratch"
		if p.CopyFrom != nil {
			from = fmt.Sprintf("copied from %q", terminal.Sanitize(p.CopyFrom.Release))
		}
		fmt.Fprintf(&b, "release %q created as a draft (%s)\n", terminal.Sanitize(r.Revision.Release), from)
	}
	if r.Template != nil {
		how := "new upload"
		if r.Template.ReusedExistingSkeleton != nil && *r.Template.ReusedExistingSkeleton {
			how = "reused: an identical workbook was uploaded earlier"
		}
		fmt.Fprintf(&b, "template attached: deliverable-template/%d (%s)\n", r.Template.SkeletonID, how)
	}
	if created && r.Revision.AnchorReport != nil {
		formatAnchorReport(&b, r.Revision.AnchorReport)
	}
	if counts := r.bindingCounts(); counts != "" {
		fmt.Fprintf(&b, "binding writes: %s\n", counts)
	}
	for _, report := range r.AnchorReports {
		formatAnchorReport(&b, report)
	}
	for _, warning := range r.ColumnWarnings {
		fmt.Fprintf(&b, "Warning: %s: %s\n", columnLabel(warning.Sheet, warning.Column), cellText(FormatIssue(warning.Issue)))
	}
	confirmations := p.confirmations
	if r.applied {
		confirmations = r.confirmations
	}
	writeNotes(&b, slices.Concat(p.Notes, confirmations))
	return b.String()
}
