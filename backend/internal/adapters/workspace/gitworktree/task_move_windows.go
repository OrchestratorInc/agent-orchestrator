package gitworktree

import (
	"golang.org/x/sys/windows"
)

func moveTaskDirectory(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// MoveFile atomically refuses every occupied destination.
	return windows.MoveFile(from, to)
}
