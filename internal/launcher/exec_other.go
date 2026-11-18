//go:build !unix

package launcher

import "os"

// execFunc is nil: replacing the process image is unsupported here.
var execFunc func(path string, argv, env []string) error

// adoptLock drops the lock descriptor variable from the environment.
func adoptLock(string) {
	_ = os.Unsetenv(lockFDEnv)
}
