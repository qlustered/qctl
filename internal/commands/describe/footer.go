package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/spf13/cobra"
)

// writeYAMLWithFooter encodes doc as a YAML document and appends footer to the
// same stream.
//
// `describe alert` and `describe warning` both print a NEXT ACTIONS block after
// their manifest, and the alert command does it twice -- once for the default
// view and once for the -vv raw dump. Open-coding it a third time is what
// pushed NewAlertCommand past the gocyclo limit.
//
// The sequence is carried over verbatim from those three sites: close the
// encoder, check that error, then write the footer -- and close it on the
// encode-error path too, rather than leaving it open. No claim is made here
// about what Close flushes; a probe that skipped it entirely left the rendered
// output byte-for-byte identical, so the reason to keep it is that it is what
// the shipped code did, not a flushing behaviour anyone has observed.
//
// footer is written verbatim, so it carries its own leading newline and its own
// comment markers. An empty footer writes nothing, which is how both renderers
// say there is no block worth printing.
func writeYAMLWithFooter(cmd *cobra.Command, doc interface{}, footer string) error {
	encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), footer)
	return nil
}
