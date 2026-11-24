//go:build unix

package runfile

import "os"

func openRunFile(path string) (*os.File, error) {
	return os.Open(path)
}
