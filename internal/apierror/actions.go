package apierror

import (
	"encoding/json"
	"maps"
)

// Action is one entry of an error's actions list: a machine-readable code and
// the server's label for it.
type Action struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// hintByAction maps an action code to the line Format() prints for it: the
// qctl command that does what the action does.
var hintByAction = map[string]string{
	// atlas common/common/types.py CompositeSuggestionActionCode. cosmosview
	// answers it with POST .../retry-build (useRetryCompositeBuild), the
	// request `run composite-run-job` sends.
	"RETRY_BUILD": "Run `qctl run composite-run-job <table>` to rebuild the table.",
}

// ActionHints returns a copy of the action-code-to-hint table.
func ActionHints() map[string]string { return maps.Clone(hintByAction) }

// decodeActions returns raw as a list of actions, or nil when it is absent,
// null, or not an array of objects.
func decodeActions(raw json.RawMessage) []Action {
	var actions []Action
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil
	}
	return actions
}

// actionHintLines returns the hint for each action whose code has one, in the
// order the server sent them, each hint once.
func (e *OperationalError) actionHintLines() []string {
	var lines []string
	seen := map[string]bool{}
	for _, a := range e.Actions {
		hint, ok := hintByAction[a.Code]
		if !ok || seen[hint] {
			continue
		}
		seen[hint] = true
		lines = append(lines, hint)
	}
	return lines
}
