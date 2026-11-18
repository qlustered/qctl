package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewBackupSettingsCommand creates the get backup-settings command.
func NewBackupSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "backup-settings",
		Short:       "List backup settings",
		Long:        `List all backup settings in the current organization.`,
		Annotations: catalog.For("backup-settings"),
		Args:        cobra.NoArgs,
		RunE:        runBackupSettings,
	}

	cmd.Flags().Int("limit", 100, "Maximum number of backup settings to return")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("order-by", "id", "Order by field (id, bucket, backup_type, created_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")
	cmd.Flags().String("search", "", "Search backup settings by bucket or backup type")
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []backup_settings.BackupSettingsTiny(nil)), ", "))

	return cmd
}

func runBackupSettings(cmd *cobra.Command, _ []string) error {
	params, err := backupSettingsParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := backup_settings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := client.List(ctx.Credential.AccessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get backup settings: %w", err)
	}
	return printBackupSettings(cmd, ctx, resp, params.Page, params.Limit)
}

func backupSettingsParams(cmd *cobra.Command) (backup_settings.ListParams, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return backup_settings.ListParams{}, err
	}
	orderBy, _ := cmd.Flags().GetString("order-by")
	if err := validateSortField(orderBy, []string{"id", "bucket", "backup_type", "created_at"}); err != nil {
		return backup_settings.ListParams{}, err
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return backup_settings.ListParams{}, err
	}
	reverse, _ := cmd.Flags().GetBool("reverse")
	params := backup_settings.ListParams{OrderBy: orderBy, Reverse: reverse, Page: page, Limit: limit}
	if search != "" {
		params.SearchQuery = &search
	}
	return params, nil
}

func printBackupSettings(cmd *cobra.Command, ctx *cmdutil.CommandContext, resp *backup_settings.ListPage, page, limit int) error {
	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "backup settings", []backup_settings.BackupSettingsTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	columns, _ := cmd.Flags().GetString("columns")
	if isTableFormat(cmd) && columns == "" {
		if err := cmd.Flags().Set("columns", "name,backup_type,bucket,id"); err != nil {
			return err
		}
	}
	p, err := printer.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	lp := listPage{noun: "backup settings", page: &page, limit: &limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if err := p.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}
