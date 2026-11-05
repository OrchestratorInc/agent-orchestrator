//go:build !windows

package shellterm

import "os"

func newCueCommandDirectory(root string) (string, error) {
	return os.MkdirTemp(root, "command-*")
}
