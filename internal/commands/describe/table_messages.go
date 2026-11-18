package describe

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/markdown"
	pkglogs "github.com/qlustered/qctl/internal/pkg/logs"
	"github.com/qlustered/qctl/internal/profiling"
)

// inlineTableMessage renders one backend message before its containing struct
// is JSON-marshaled into a table cell. The printer still applies the cell's
// --max-column-width cap; keeping the renderer uncapped here makes that outer
// cap the only place content is shortened.
func inlineTableMessage(message string) string {
	return markdown.Inline(message, markdown.InlineOptions{})
}

func inlineTableMessagePtr(message *string) *string {
	if message == nil {
		return nil
	}
	rendered := inlineTableMessage(*message)
	return &rendered
}

func inlineTableLogMessages(entries []pkglogs.Entry) []pkglogs.Entry {
	rendered := append([]pkglogs.Entry(nil), entries...)
	for i := range rendered {
		rendered[i].Message = inlineTableMessage(rendered[i].Message)
	}
	return rendered
}

func inlineTableAnalysisTaskMessages(tasks []profiling.AnalysisTask) []profiling.AnalysisTask {
	rendered := append([]profiling.AnalysisTask(nil), tasks...)
	for i := range rendered {
		rendered[i].Msg = inlineTableMessage(rendered[i].Msg)
	}
	return rendered
}

// writeTableMessageSection keeps a describe table's complete message outside
// the JSON cell. PlainText is the detail-view form: it preserves line
// breaks and never shortens a marked value.
func writeTableMessageSection(out io.Writer, heading, message string) error {
	rendered := markdown.PlainText(message)
	if rendered == "" {
		return nil
	}
	_, err := fmt.Fprintf(out, "\n%s:\n%s\n", heading, rendered)
	return err
}
