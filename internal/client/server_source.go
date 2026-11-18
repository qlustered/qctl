package client

import "sync/atomic"

type serverSource struct {
	server string
	source string
}

var recordedServer atomic.Pointer[serverSource]

// SetServerSource records the setting that supplied this invocation's server.
func SetServerSource(server, source string) {
	recordedServer.Store(&serverSource{server: server, source: source})
}

func sourceForServer(server string) string {
	if s := recordedServer.Load(); s != nil && s.server == server && s.source != "" {
		return " (" + s.source + ")"
	}
	return ""
}
