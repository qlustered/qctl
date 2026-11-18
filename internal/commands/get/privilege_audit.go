package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/spf13/cobra"
)

const privilegeAuditColumns = "performed,request,action,target,approver,requestor,ip"

// privilegeAuditRow is the table-display projection of one audit entry. The
// wire names every person by UUID only; the table resolves them through one
// users-directory sweep.
type privilegeAuditRow struct {
	ID            string `json:"id"`
	Performed     string `json:"performed"`
	Request       string `json:"request"`
	Action        string `json:"action"`
	Target        string `json:"target"`
	Approver      string `json:"approver"`
	Requestor     string `json:"requestor"`
	IP            string `json:"ip"`
	PerformedBy   string `json:"performed_by"`
	UserAgent     string `json:"user_agent"`
	Justification string `json:"justification"`
}

// NewPrivilegeAuditCommand creates the get privilege-audit command.
func NewPrivilegeAuditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privilege-audit",
		Short: "List the immutable ledger of applied ops privilege changes (platform-wide)",
		Long: `List the audit ledger of ops privilege changes, newest first.

The ledger has one row per APPLIED change: an approval, or a change the server
applied without one (qctl explain privilege-request lists when). A rejected or
expired request leaves no row.

This is an operations surface: the route requires the ops role, so an admin or
editor gets a 403 (exit 4). The ledger is platform-wide, whichever
organization the current context points at.

-o json is the wire schema verbatim and the stable, machine-readable contract;
-o yaml carries the same keys. The table is presentation and may change: it
names people through the current org's user directory, and shows UUIDs (after
one warning) when that directory cannot be read.

--target-user takes an email, a "First Last" name, or a UUID; a name resolves
against the CURRENT org's members, so from a customer org pass the UUID.
--request-id takes the full UUID of one request.

Available columns: id, performed, request, action, target, approver,
requestor, ip, performed_by, user_agent, justification.

Examples:
  qctl get privilege-audit
  qctl get privilege-audit --target-user tom@example.com
  qctl get privilege-audit --request-id 0190bbbb-1111-2222-3333-444444444444 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("privilege-request"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runPrivilegeAudit,
	}
	cmd.Flags().String("target-user", "", `Only changes to this user (email, "First Last", or UUID)`)
	cmdutil.RefString(cmd.Flags(), "request-id", "privilege-request", "Only changes applied by this request (full UUID)")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, "Entries per page (max 100)")
	cmdutil.DeclareColumns(cmd, []privilegeAuditRow(nil))
	return cmd
}

func runPrivilegeAudit(cmd *cobra.Command, _ []string) error {
	opts, err := privilegeAuditOptions(cmd)
	if err != nil {
		return err
	}
	target, err := privilegeTargetUserFlag(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := privilege_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	var directory *privilege_requests.Directory
	if target != "" {
		id, dir, err := client.ResolveTargetUser(ctx.Credential.AccessToken, target)
		if err != nil {
			return err
		}
		opts.TargetUserID = &id
		directory = dir
	}
	page, err := client.Audit(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to list privilege audit: %w", err)
	}

	// The structured views are the compliance contract and never touch the
	// directory: they emit the wire object as the server sent it.
	outputFormat, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Changed("output") {
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, page)
		}
	}

	if len(page.AuditEntries) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "privilege audit entries",
			[]privilegeAuditRow{}, &page.Total, privilegePlatformWideHint)
		if err != nil || skip {
			return err
		}
	}

	// Reuse the directory --target-user already swept.
	if directory == nil {
		directory = cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, ctx.Credential.AccessToken)
	}
	printContextBanner(cmd, ctx)

	rows := privilegeAuditRows(page.AuditEntries, directory)
	if err := tableui.PrintFromCmd(cmd, rows, privilegeAuditColumns); err != nil {
		return err
	}
	printMorePrivilegeHint(cmd, "audit entries", page.Total, page.Page, page.Limit)
	return nil
}

func privilegeAuditOptions(cmd *cobra.Command) (privilege_requests.AuditOptions, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return privilege_requests.AuditOptions{}, err
	}
	opts := privilege_requests.AuditOptions{Page: page, Limit: limit}
	if cmd.Flags().Changed("request-id") {
		raw, _ := cmd.Flags().GetString("request-id")
		id, err := cmdutil.ParseUUIDArg(raw, "--request-id")
		if err != nil {
			return privilege_requests.AuditOptions{}, err
		}
		opts.RequestID = &id
	}
	return opts, nil
}

func privilegeAuditRows(
	entries []privilege_requests.AuditEntry,
	directory *privilege_requests.Directory,
) []privilegeAuditRow {
	rows := make([]privilegeAuditRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, privilegeAuditRow{
			ID:            e.ID.String(),
			Performed:     timeutil.FormatRelative(e.PerformedAt),
			Request:       e.RequestID.String(),
			Action:        privilege_requests.ActionOf(e.OldValue, e.NewValue),
			Target:        directory.DisplayName(e.TargetUserID),
			Approver:      directory.DisplayName(e.ApproverID),
			Requestor:     directory.DisplayName(e.RequestorID),
			IP:            e.PerformedByIP,
			PerformedBy:   directory.DisplayName(e.PerformedByID),
			UserAgent:     e.PerformedByUserAgent,
			Justification: e.Justification,
		})
	}
	return rows
}
