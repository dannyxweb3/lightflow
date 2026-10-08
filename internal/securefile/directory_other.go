//go:build !windows

package securefile

import "os"

func RestrictDirectory(path string) error { return os.Chmod(path, 0700) }
