package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const emailSenderColumns = "email,first_name,last_name,delivery,since"

// emailSenderRow is the table-display projection of one sender.
type emailSenderRow struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Delivery  string `json:"delivery"`
	Since     string `json:"since"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
}

// NewEmailSendersCommand creates the get email-senders command.
func NewEmailSendersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email-senders",
		Short: "List the senders assigned to a cloud source's email intake",
		Long: `List the people assigned to send files to a cloud source's email intake, in
the order the server returns them (by email). Only active external users are
listed.

--cloud-source-id is required and takes an id or cloud-source/<id>. DELIVERY is
ok or suppressed, the state of mail to that address; SINCE is when it was
suppressed. Available columns: email, first_name, last_name, delivery, since,
user_id, reason (why delivery is suppressed: hard_bounce, complaint or
repeated_soft_bounce).

-o json and -o yaml print a List of the sender records as the server sent
them; -o name prints email-sender/<user-id> for each sender, which
email-sender remove and email-sender resend-welcome take.

Examples:
  qctl get email-senders --cloud-source-id 42
  qctl get email-senders --cloud-source-id cloud-source/42 --columns email,delivery,reason,user_id
  qctl get email-senders --cloud-source-id 42 -o json`,
		Annotations: catalog.For("email-sender"),
		Args:        cobra.NoArgs,
		RunE:        runEmailSenders,
	}
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Cloud source ID (required)")
	_ = cmd.MarkFlagRequired("cloud-source-id")
	cmdutil.DeclareColumns(cmd, []emailSenderRow(nil))
	return cmd
}

func runEmailSenders(cmd *cobra.Command, _ []string) error {
	sourceID, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	senders, err := email_intake.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		ListSenders(ctx.Credential.AccessToken, sourceID)
	if err != nil {
		return err
	}
	if len(senders) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "email senders", []emailSenderRow{}, nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, senders, listPage{noun: "email senders"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		if l, ok := data.(output.List); ok {
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		if format == string(output.FormatName) {
			for _, s := range senders {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s/%s\n", email_intake.SenderRefKind, s.UserID); err != nil {
					return err
				}
			}
			return nil
		}
		return tableui.PrintFromCmd(cmd, emailSenderRows(senders), emailSenderColumns)
	})
}

func emailSenderRows(senders []email_intake.Sender) []emailSenderRow {
	rows := make([]emailSenderRow, 0, len(senders))
	for _, s := range senders {
		row := emailSenderRow{
			Email:     s.Email,
			FirstName: s.FirstName,
			LastName:  s.LastName,
			Delivery:  string(s.EmailDeliveryStatus.Status),
			Since:     timeutil.FormatRelativePtr(s.EmailDeliveryStatus.Since),
			UserID:    s.UserID.String(),
		}
		if s.EmailDeliveryStatus.Reason != nil {
			row.Reason = string(*s.EmailDeliveryStatus.Reason)
		}
		rows = append(rows, row)
	}
	return rows
}
