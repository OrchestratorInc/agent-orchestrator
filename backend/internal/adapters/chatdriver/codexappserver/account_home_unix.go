//go:build !windows

package codexappserver

import "os"

func managedHomePrivate(_ string, info os.FileInfo) bool {
	return info.Mode().Perm() == 0o700
}
