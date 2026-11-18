//go:build unix

package launcher

import (
	"os"
	"strconv"
	"syscall"
)

// execFunc replaces the process image, keeping its pid and standard streams.
var execFunc = syscall.Exec

// adoptLock marks the inherited in-use lock descriptor named by fd
// close-on-exec, so programs this process starts do not hold it, and drops
// the variable from the environment.
func adoptLock(fd string) {
	_ = os.Unsetenv(lockFDEnv)
	if n, err := strconv.Atoi(fd); err == nil && n > 2 {
		syscall.CloseOnExec(n)
	}
}
