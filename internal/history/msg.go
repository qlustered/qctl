package history

import "github.com/qlustered/qctl/internal/apierror"

// PlainMessage renders the backend's undo/redo message for a terminal.
//
// The message is Markdown: get_history_items_human_readable_msg's own docstring
// says "Returns a markdown message to be displayed for undo/redo screens"
// (atlas cettings/cettings/controllers/history_human_readable_mixin.py). It
// carries **bold** runs and markdown links to frontend routes.
//
// Several items are joined with NEWLINES and bulleted, not with a <br>. The
// function's last line is
//
//	return "\n".join([f"- {item}" for item in result])
//
// and the single-item case returns result[0] bare, with no bullet (its
// `if len(history_items) == 1:` branch). `grep -n "<br>"` over that whole file
// returns nothing. A literal <br> reaches this renderer only from the ERROR
// paths -- the concurrency refusal in history_controller.py
// HistoryController.undo and the partial-failure `"<br>".join(msgs)` in undo
// and redo -- which is why msg_test.go still covers it.
//
// A value too long to show whole arrives COMPLETE inside a ~*value*~ marker
// (atlas docs/message_value_markers.md, atlas-d702), in one of four shapes.
// The mixin marks an instance name longer than 43 characters
// (_get_human_readable_name_of_instance, max_length=40, and
// shorten_str_and_say_modified in common/common/misc.py only trims past
// max_length + 3) in its link text, `[~*name*~](url)`, or bare, `~*name*~`,
// when the model has no frontend route -- DatasetRule,
// ExtendedContextAwareModel and any model FrontendController.get_frontend_route
// does not list (cettings/cettings/controllers/frontend_controller.py);
// get_value_formatted_for_msg marks a value longer than 23 inside the
// quotes of its bold run, `**“~*value*~”**` -- curly quotes in production,
// straight ones only under settings.TESTING (common/common/exceptions.py,
// STYLE_TO_PREFIX_SUFFIX); and get_column_name_formatted_for_msg marks a
// column name longer than 19 inside a bold run with no quotes,
// `**~*column*~**` (max_length=16). ToPlainText prints the value whole and literal
// between the backend's own quotes; an undo/redo step is a detail view, so
// nothing is shortened. Before atlas-d702 the backend shortened the value
// itself into `[**“short...x”**](## "full")` with both halves
// markdown-escaped; qctl parses no such compatibility shape.
func PlainMessage(msg string) string {
	return apierror.ToPlainText(msg)
}
