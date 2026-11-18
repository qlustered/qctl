package upload

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableTemplateCommand creates the 'upload deliverable-template' command.
func NewDeliverableTemplateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-template [<file>]",
		Short: "Upload a workbook template for deliverables, or attach one to a draft revision",
		Long: `Upload a workbook as a deliverable template. Its name must end in .xlsx or
.xlsm, in any case: a .xlsx template keeps no macros, and a .xlsm template
keeps its VBA.

Without --deliverable the template is uploaded to the organization and
attached to nothing. With --deliverable (a slug, name or UUID) it is attached
to a draft revision of that deliverable: the only draft, or the one --release
(an exact release label or a full revision UUID) or --revision (a full UUID)
names. A revision that is not a draft is refused before anything is uploaded.
Replacing a draft's template re-anchors its bindings, and the command prints
each binding that moved, broke or needs confirmation.

A template the server reports as losing parts of the workbook is refused
unless --acknowledge-lossy is given. The refusal lists the losses and the
command that accepts them without uploading again; it attaches nothing.
Attaching a lossy template needs --acknowledge-lossy again.

--template-id finalizes an uploaded template again without sending a file:
to accept its losses, or to attach it to a draft with --deliverable.

A workbook identical to a template the organization already has reuses that
template, its id and its VBA setting. The deliverable's filename_pattern is
not changed; downloads take their extension from the template.

-o json and -o yaml print the server's response, and -o name prints
deliverable-template/<id>. Under all three the prose goes to stderr.

Examples:
  qctl upload deliverable-template bordereau.xlsx
  qctl upload deliverable-template bordereau.xlsm --deliverable monthly-bordereau
  qctl upload deliverable-template bordereau.xlsx --deliverable monthly-bordereau --release 2026.2
  qctl upload deliverable-template --template-id 42 --acknowledge-lossy
  qctl upload deliverable-template --template-id 42 --deliverable monthly-bordereau`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-template"), "table", "json", "yaml", "name"), "-"), // <file> is a local file
		Args:        cobra.MaximumNArgs(1),
		RunE:        runUploadDeliverableTemplate,
	}
	cmdutil.RefInt(cmd.Flags(), "template-id", "deliverable-template", "Finalize this uploaded template again instead of uploading a file")
	cmd.Flags().Bool("acknowledge-lossy", false, "Accept the parts of the workbook the template loses")
	cmdutil.RefString(cmd.Flags(), "deliverable", "deliverable", "Deliverable slug, name or UUID whose draft revision gets the template")
	cmd.Flags().String("release", "", "Draft revision release label or full UUID (requires --deliverable; default: the only draft)")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Draft revision full UUID (requires --deliverable)")
	return cmd
}

// templateRequest is what upload deliverable-template was asked to do. file
// is "" when templateID names an uploaded template, and deliverable is ""
// when nothing is attached.
type templateRequest struct {
	file        string
	templateID  int
	ack         bool
	deliverable string
	release     string
	revision    string
}

// templateDraft is the draft revision a template is attached to. release is
// its label for display, and selector the --release value that names it in a
// printed command line.
type templateDraft struct {
	id       uuid.UUID
	slug     string
	release  string
	selector string
}

func runUploadDeliverableTemplate(cmd *cobra.Command, args []string) error {
	req, err := readTemplateRequest(cmd, args)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	var draft *templateDraft
	if req.deliverable != "" {
		if draft, err = resolveTemplateDraft(c, token, req); err != nil {
			return err
		}
	}
	var revisionID *uuid.UUID
	if draft != nil {
		revisionID = &draft.id
	}
	id := req.templateID
	if req.file != "" {
		if id, err = c.UploadTemplate(token, revisionID, req.file); err != nil {
			return err
		}
	}
	result, err := c.FinalizeTemplate(token, revisionID, id, req.ack)
	if err != nil {
		return finalizeTemplateError(err, req, draft, id)
	}
	return writeTemplateResult(cmd, req, draft, result)
}

// readTemplateRequest reads the positional and flags and checks the template
// file with checkTemplateFile. Every other refusal is a bad-usage error.
func readTemplateRequest(cmd *cobra.Command, args []string) (templateRequest, error) {
	templateID, haveID, err := cmdutil.PositiveIDFlag(cmd, "template-id")
	if err != nil {
		return templateRequest{}, err
	}
	switch {
	case len(args) == 1 && haveID:
		return templateRequest{}, qerrors.NewBadUsage("give a template file or --template-id, not both")
	case len(args) == 0 && !haveID:
		return templateRequest{}, qerrors.NewBadUsage("give a template file (.xlsx or .xlsm) or --template-id")
	}
	req := templateRequest{templateID: templateID}
	req.ack, _ = cmd.Flags().GetBool("acknowledge-lossy")
	req.deliverable, _ = cmd.Flags().GetString("deliverable")
	if cmd.Flags().Changed("deliverable") {
		if err := deliverables.ValidateReference(req.deliverable); err != nil {
			return templateRequest{}, err
		}
	}
	for _, flag := range []string{"release", "revision"} {
		value, _ := cmd.Flags().GetString(flag)
		if !cmd.Flags().Changed(flag) {
			continue
		}
		if value == "" {
			return templateRequest{}, qerrors.NewBadUsagef("--%s cannot be empty", flag)
		}
		if req.deliverable == "" {
			return templateRequest{}, qerrors.NewBadUsagef("--%s chooses a revision of --deliverable; add --deliverable", flag)
		}
	}
	req.release, _ = cmd.Flags().GetString("release")
	req.revision, _ = cmd.Flags().GetString("revision")
	if err := deliverables.ValidateRevisionSelector(req.release, req.revision); err != nil {
		return templateRequest{}, err
	}
	if len(args) == 1 {
		req.file = args[0]
		if err := checkTemplateFile(req.file); err != nil {
			return templateRequest{}, err
		}
	}
	return req, nil
}

// checkTemplateFile refuses, with exit 2, a path whose extension is not a
// template's or that is a directory, and returns an exit-1 error for a path
// os.Stat cannot read.
func checkTemplateFile(path string) error {
	if err := deliverables.ValidateTemplatePath(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to access template file: %w", err)
	}
	if info.IsDir() {
		return qerrors.NewBadUsagef("%q is a directory, not a template file", path)
	}
	return nil
}

// resolveTemplateDraft resolves req's deliverable, including one in the
// trash, and selects its revision with ResolveTemplateTarget. A selected
// revision that is not a draft is a bad-usage error. The draft's selector is
// its release label, or its UUID when terminal.Sanitize changes the label.
func resolveTemplateDraft(c *deliverables.Client, token string, req templateRequest) (*templateDraft, error) {
	id, err := c.ResolveID(token, req.deliverable, true)
	if err != nil {
		return nil, err
	}
	item, err := c.Get(token, id)
	if err != nil {
		return nil, err
	}
	target, _, err := c.ResolveTemplateTarget(token, id, req.release, req.revision)
	if err != nil {
		return nil, err
	}
	draft := &templateDraft{id: target.ID, slug: terminal.Sanitize(item.Slug), release: terminal.Sanitize(target.Release)}
	draft.selector = target.Release
	if draft.release != target.Release {
		draft.selector = target.ID.String()
	}
	if target.State != api.DeliverableRevisionStateDraft {
		return nil, qerrors.NewBadUsagef("deliverable/%s release %q is %s; only a draft revision's template can change",
			draft.slug, draft.release, terminal.Sanitize(string(target.State)))
	}
	return draft, nil
}

// finalizeTemplateError wraps err, the failure to finalize template id, with
// the command line that finalizes a template again: for a
// *TemplateLossyError, its template with --acknowledge-lossy; for an exit-1
// or exit-4 failure when req has a file, id with req's --acknowledge-lossy.
// Any other err is returned unchanged.
func finalizeTemplateError(err error, req templateRequest, draft *templateDraft, id int) error {
	var lossy *deliverables.TemplateLossyError
	if errors.As(err, &lossy) {
		return fmt.Errorf("%w\nTo accept the loss without uploading again:\n  %s", err, templateRerun(lossy.TemplateID, true, draft))
	}
	if code := qerrors.GetExitCode(err); req.file != "" && (code == qerrors.ExitGenericError || code == qerrors.ExitUnauthorized) {
		return fmt.Errorf("%w\nThe file is uploaded as deliverable-template/%d. To finalize it again without uploading:\n  %s",
			err, id, templateRerun(id, req.ack, draft))
	}
	return err
}

// templateRerun is the upload deliverable-template command line that
// finalizes template id, on draft when it is not nil.
func templateRerun(id int, ack bool, draft *templateDraft) string {
	line := "qctl upload deliverable-template --template-id " + strconv.Itoa(id)
	if ack {
		line += " --acknowledge-lossy"
	}
	if draft != nil {
		line += " --deliverable " + cmdutil.ShellArg(draft.slug) + " --release " + cmdutil.ShellArg(draft.selector)
	}
	return line
}

// writeTemplateResult prints the finalized template: prose through the
// result writer's Human, and under -o json, yaml or name the response body or
// deliverable-template/<id> on stdout.
func writeTemplateResult(cmd *cobra.Command, req templateRequest, draft *templateDraft, result *deliverables.TemplateResult) error {
	w := cmdutil.NewResultWriter(cmd)
	name := fmt.Sprintf("deliverable-template/%d", result.SkeletonID)
	switch {
	case draft != nil && result.Attached:
		fmt.Fprintf(w.Human, "%s attached to deliverable/%s release %q\n", name, draft.slug, draft.release)
	case req.file != "":
		fmt.Fprintf(w.Human, "%s finalized from %s, not attached\n", name, terminal.Sanitize(filepath.Base(req.file)))
	default:
		fmt.Fprintf(w.Human, "%s finalized, not attached\n", name)
	}
	fmt.Fprint(w.Human, deliverables.FormatTemplate(result))
	writeReuseNote(w.Human, req, result)
	if draft != nil {
		fmt.Fprintf(w.Human, "Next: qctl describe deliverable-revision %s --release %s\n",
			cmdutil.ShellArg(draft.slug), cmdutil.ShellArg(draft.selector))
	} else {
		next := fmt.Sprintf("Next: qctl upload deliverable-template --template-id %d --deliverable <deliverable>", result.SkeletonID)
		if result.FidelityReport.IsLossy {
			next += " --acknowledge-lossy"
		}
		fmt.Fprintln(w.Human, next)
	}
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return w.Write(nil, name)
}

// writeReuseNote prints a note when req uploaded a file, the server reused an
// existing template, and that template's VBA setting is not the one the
// file's extension sets.
func writeReuseNote(out io.Writer, req templateRequest, result *deliverables.TemplateResult) {
	if req.file == "" || result.ReusedExistingSkeleton == nil || !*result.ReusedExistingSkeleton {
		return
	}
	keepsVBA := deliverables.TemplateKeepsVBA(req.file)
	if result.KeepVba == keepsVBA {
		return
	}
	ext := ".xlsx"
	if keepsVBA {
		ext = ".xlsm"
	}
	fmt.Fprintf(out, "Note: an identical workbook was uploaded earlier, and this template keeps that upload's VBA setting, so VBA kept is %t although the file ends in %s.\n",
		result.KeepVba, ext)
}
