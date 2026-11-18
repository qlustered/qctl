//go:build !unix

package releasebuild

import "os/exec"

// ownProcessGroup leaves cmd unchanged.
func ownProcessGroup(*exec.Cmd) {}
