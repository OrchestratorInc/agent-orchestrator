//go:build unix

package runfile

import "os"

func atomicReplace(src, dst string) error {
	return os.Rename(src, dst)
}
