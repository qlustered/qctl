package upload

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// warnDroppedFixes reports the upload's manual-fix count or an unavailable check.
func warnDroppedFixes(out io.Writer, ctx *cmdutil.CommandContext, uc *uploadContext) {
	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	var count int
	if err == nil {
		count, err = client.GetCorrectedRowCount(ctx.Credential.AccessToken, uc.cloudSourceID, uc.fileName, uc.periodKey)
	}
	if err != nil {
		fmt.Fprintf(out, "Warning: could not check whether this upload may drop manual fixes: %s; continuing with upload.\n", terminal.Sanitize(err.Error()))
		return
	}
	if count == 1 {
		fmt.Fprintln(out, "Warning: this upload may drop manual fixes on 1 row. If the file changes that row, its corrections cannot be carried without key columns.")
	} else if count > 1 {
		fmt.Fprintf(out, "Warning: this upload may drop manual fixes on %d rows. Corrections to rows changed in the file cannot be carried without key columns.\n", count)
	}
}
