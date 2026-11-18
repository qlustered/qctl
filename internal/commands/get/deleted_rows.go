package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const (
	deletedRowColumns        = "row_id,cloud_source_id,file,delete_id,deleted,deleted_by,reason,undone"
	deletedRowVerboseColumns = "row_id,cloud_source_id,file,delete_id,deleted,deleted_by,reason,undone,signature,is_bad_data"

	// maxDeletedRowsLimit is the route's largest page.
	maxDeletedRowsLimit = 100
)

// NewDeletedRowsCommand creates the get deleted-rows command.
func NewDeletedRowsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deleted-rows [table-id-or-name]",
		Short: "List a table's deleted rows: who deleted them, when and why",
		Long: `List the rows deleted from a table, newest delete first: each row's cloud
source, the file and line it came from, the delete that removed it, who ran
that delete, when, and the reason given.

  qctl get deleted-rows orders
  qctl get deleted-rows orders --cloud-source s3-nightly
  qctl get deleted-rows 42 --file-id 311 --page 2 --limit 100
  qctl get deleted-rows orders --delete-id 17 -o yaml

A row appears once per delete that removed it. An undone delete stays listed,
with UNDONE true. FILE is name:line of the first file the row came from, with
+N when it came from N more; -o json|yaml lists every file.

--page and --limit select one page (default: page 1, limit 50); --limit is at
most 100. -v adds the SIGNATURE and IS-BAD-DATA columns.

Rows are deleted with 'qctl rows delete-quarantined'.`,
		Annotations: cmdutil.WithFormats(cmdutil.WithArgKinds(catalog.For("data"), "table"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			return runDeletedRows(cmd, positional)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().String("cloud-source", "", "Only rows of this cloud source, by NAME (exact, case-sensitive)")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Only rows of this cloud source, by ID")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Only rows that came from this file (stored item)")
	cmd.Flags().Int("delete-id", 0, "Only rows removed by this delete (the DELETE-ID column)")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 50, "Rows per page (1-100)")
	cmdutil.DeclareColumns(cmd, []deletedRowRow(nil))
	return cmd
}

// deletedRowsTerms is the request as far as the command line alone decides it.
type deletedRowsTerms struct {
	filter      datasets.DeletedRowsFilter
	cloudSource string
}

// deletedRowsFlags is every refusal decided from the flags, all of them before
// Bootstrap and all exit 2.
func deletedRowsFlags(cmd *cobra.Command) (deletedRowsTerms, error) {
	var t deletedRowsTerms
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"cloud-source-id", "cloud-source"}); err != nil {
		return t, err
	}
	var err error
	if t.filter.Page, t.filter.Limit, err = paginationFlags(cmd); err != nil {
		return t, err
	}
	if t.filter.Limit > maxDeletedRowsLimit {
		return t, qerrors.NewBadUsagef("--limit must be at most %d, got %d", maxDeletedRowsLimit, t.filter.Limit)
	}
	if t.filter.CloudSourceID, _, err = cmdutil.PositiveIDFlag(cmd, "cloud-source-id"); err != nil {
		return t, err
	}
	if t.filter.FileID, _, err = cmdutil.PositiveIDFlag(cmd, "file-id"); err != nil {
		return t, err
	}
	if t.filter.DeleteID, _, err = cmdutil.PositiveIDFlag(cmd, "delete-id"); err != nil {
		return t, err
	}
	t.cloudSource, _ = cmd.Flags().GetString("cloud-source")
	if cmd.Flags().Changed("cloud-source") && t.cloudSource == "" {
		return t, qerrors.NewBadUsage(
			"--cloud-source cannot be empty: name the cloud source, or use --cloud-source-id <id>")
	}
	return t, nil
}

func runDeletedRows(cmd *cobra.Command, tableArg string) error {
	terms, err := deletedRowsFlags(cmd)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, tableArg)
	if err != nil {
		return err
	}
	if terms.cloudSource != "" {
		// Only with a name: ResolveCloudSource auto-detects when given neither
		// an ID nor a name, and an unset source filter means every source.
		terms.filter.CloudSourceID, _, err = cmdutil.ResolveCloudSource(
			ctx, tableID, tableNameForMessage(cmd, tableArg), 0, terms.cloudSource)
		if err != nil {
			return err
		}
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	list, err := client.ListDeletedRows(ctx.Credential.AccessToken, tableID, terms.filter)
	if err != nil {
		return err
	}

	page, limit := terms.filter.Page, terms.filter.Limit
	lp := listPage{noun: "deleted rows", page: &page, limit: &limit, total: list.TotalRows, more: list.Next != nil}
	if len(list.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "deleted rows", []deletedRowRow{}, list.TotalRows)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	columns := deletedRowColumns
	if ctx.Verbosity >= 1 {
		columns = deletedRowVerboseColumns
	}
	return printListPage(cmd, list.Results, lp, func(data any) error {
		if l, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		if rows, ok := data.([]datasets.DeletedRow); ok {
			data = deletedRowRows(rows)
		}
		return tableui.PrintFromCmd(cmd, data, columns)
	})
}

// deletedRowRow is one deleted row as the table view shows it.
type deletedRowRow struct {
	RowID         int    `json:"row_id"`
	CloudSourceID int    `json:"cloud_source_id"`
	File          string `json:"file"`
	DeleteID      int    `json:"delete_id"`
	Deleted       string `json:"deleted"`
	DeletedBy     string `json:"deleted_by"`
	Reason        string `json:"reason"`
	Undone        bool   `json:"undone"`
	Signature     string `json:"signature"`
	IsBadData     bool   `json:"is_bad_data"`
}

func deletedRowRows(rows []datasets.DeletedRow) []deletedRowRow {
	out := make([]deletedRowRow, len(rows))
	for i := range rows {
		r := &rows[i]
		out[i] = deletedRowRow{
			RowID:         r.RowID,
			CloudSourceID: r.DataSourceModelID,
			File:          deletedRowFile(r.Files),
			DeleteID:      r.RowDeleteID,
			Deleted:       timeutil.FormatRelative(r.DeletedAt),
			DeletedBy:     deleterLabel(r.DeletedBy),
			Reason:        strings.Join(strings.Fields(r.Comment), " "),
			Undone:        r.Undone,
			Signature:     dashIfEmpty(r.InitialSignature),
			IsBadData:     r.IsBadData,
		}
	}
	return out
}

// deletedRowFile renders the first file as name:line, "file/<id>" standing in
// for a name the response left null, with "+N" for N more files.
func deletedRowFile(files []datasets.DeletedRowFile) string {
	if len(files) == 0 {
		return "-"
	}
	f := files[0]
	var s string
	switch {
	case f.FileName != nil && *f.FileName != "":
		s = *f.FileName
	case f.StoredItemID != nil:
		s = fmt.Sprintf("file/%d", *f.StoredItemID)
	default:
		s = "?"
	}
	if f.RowNumber != nil {
		s += fmt.Sprintf(":%d", *f.RowNumber)
	}
	if len(files) > 1 {
		s += fmt.Sprintf(" +%d", len(files)-1)
	}
	return s
}

// deleterLabel is the deleter's email, else name, else id; "-" for nil.
func deleterLabel(u *api.UserInfoTinyDictSchema) string {
	if u == nil {
		return "-"
	}
	if u.Email != "" {
		return u.Email
	}
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	return u.ID.String()
}

func dashIfEmpty(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}
