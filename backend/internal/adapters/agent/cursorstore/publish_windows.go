package cursorstore

import "golang.org/x/sys/windows"

func publishDirectory(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// No MOVEFILE_REPLACE_EXISTING: an unrelated target must never be replaced.
	return windows.MoveFileEx(from, to, 0)
}
