package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/queue"
	"github.com/spf13/cobra"
)

// NewQueueMessageCommand creates the describe queue-message command.
func NewQueueMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue-message <id>",
		Short: "Show one queue message, including its payload",
		Long: `Show one message on the platform's internal job queue, including the payload
the producer enqueued.

This is an operations view: the route requires the ops role, and an admin or
editor gets a 403.

The payload is the 'spec.data' object. It is whatever the producer put there --
there is no schema for it, and it differs per queue.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: QueueMessage
  metadata:
    name: files/77/orders.csv
    labels:
      q_name: CUEUE_INGESTION
      dataset_id: "5"
      data_source_model_id: "22"
    annotations:
      queue_message_id: "42"
  spec:
    id: 42
    q_name: CUEUE_INGESTION
    key: files/77/orders.csv
    dataset_id: 5
    data_source_model_id: 22
    data:
      ingestion_job_id: 101
  status:
    acknowledged: false
    is_dead: true
    put_count: 1
    receive_count: 4
    max_receive_count: 4
    reschedule_count: 0
    enqueued_at: 2025-01-18T09:00:00Z
    dequeued_at: 2025-01-18T10:00:00Z`,
		Annotations: cmdutil.WithFormats(catalog.For("queue-message"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Settled before Bootstrap: a non-numeric id is decidable locally
			// and must cost no credential read and no request.
			ids, err := cmdutil.ParseIntArgs(args, "queue message ID")
			if err != nil {
				return err
			}
			msgID := ids[0]

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := queue.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}

			msg, err := client.GetMessage(ctx.Credential.AccessToken, msgID)
			if err != nil {
				return fmt.Errorf("failed to get queue message: %w", err)
			}
			manifest := queue.ToManifest(msg)

			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = outputFormatYAML
			}

			switch outputFormat {
			case outputFormatJSON:
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(manifest)
			case outputFormatTable:
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				if err := p.Print(manifest); err != nil {
					return fmt.Errorf("failed to print output: %w", err)
				}
				// The payload is the reason to run this command, and the table
				// renderer caps a JSON cell at --max-column-width, so it is
				// printed again in full underneath rather than left truncated.
				// The same shape `describe ingestion-job -o table` uses for its
				// status message.
				return writeQueueMessageData(cmd, manifest)
			default:
				encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(manifest)
			}
		},
	}

	return cmd
}

// writeQueueMessageData prints the untruncated payload under the table view.
//
// It goes out as JSON rather than through terminal.Sanitize'd prose because the
// payload is structured data, not a backend message: it is what a reader will
// copy into a bug report, and re-encoding it keeps control characters escaped
// by the encoder rather than rendered.
func writeQueueMessageData(cmd *cobra.Command, manifest *queue.MessageManifest) error {
	if len(manifest.Spec.Data) == 0 {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nData:")
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest.Spec.Data)
}
