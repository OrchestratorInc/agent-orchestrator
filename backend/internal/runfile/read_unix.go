//go:build !windows

package runfile

import "os"

func openRead(path string) (*os.File, error) { return os.Open(path) }
