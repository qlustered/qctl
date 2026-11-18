package launcher

import "github.com/spf13/cobra"

// localCommands classifies every top-level command name and alias: true runs
// in this process with no release check, false is checked against the target
// server's release. A name missing from the table is checked.
var localCommands = map[string]bool{
	// Local: no request to atlas, or (config) only to endpoints every atlas
	// release serves alike. config use-context runs its own release check.
	"api-resources":                 true,
	"cache":                         true,
	"completion":                    true,
	"config":                        true,
	"docs":                          true,
	"guide":                         true,
	"help":                          true,
	"info":                          true,
	"skill":                         true,
	"version":                       true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,

	// Checked: talks to atlas. explain fetches the server's OpenAPI spec.
	"alert":              false,
	"apply":              false,
	"auth":               false,
	"change-proposal":    false,
	"comment-thread":     false,
	"copy":               false,
	"create":             false,
	"data-review":        false,
	"data-review-group":  false,
	"delete":             false,
	"deliverable":        false,
	"deliverable-run":    false,
	"describe":           false,
	"diff":               false,
	"disable":            false,
	"download":           false,
	"duplicate-group":    false,
	"email-intake":       false,
	"email-sender":       false,
	"enable":             false,
	"explain":            false,
	"file":               false,
	"files":              false,
	"get":                false,
	"inspect":            false,
	"kill":               false,
	"merge":              false,
	"notification":       false,
	"pii-access-request": false,
	"pii-grant":          false,
	"privilege":          false,
	"queue-message":      false,
	"redo":               false,
	"rekey":              false,
	"reveal":             false,
	"review":             false,
	"rows":               false,
	"rule-upgrade":       false,
	"run":                false,
	"set-default":        false,
	"share":              false,
	"slack":              false,
	"submission-status":  false,
	"submit":             false,
	"suggestion":         false,
	"table-member":       false,
	"table-rule":         false,
	"table-series":       false,
	"undelete":           false,
	"undo":               false,
	"upload":             false,
	"warning":            false,
}

// IsLocal reports whether the top-level command word runs without a release
// check.
func IsLocal(word string) bool {
	return localCommands[word]
}
