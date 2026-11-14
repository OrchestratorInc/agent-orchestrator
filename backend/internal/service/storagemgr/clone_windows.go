//go:build windows

package storagemgr

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileSupportsBlockRefcounting is the volume flag ReFS (and so a Dev Drive)
// sets when it can clone blocks.
const fileSupportsBlockRefcounting = 0x08000000

// minBlockCloneBuild is the first Windows build whose CopyFile clones blocks
// on its own within a ReFS volume (Windows 11 24H2 / Server 2025).
const minBlockCloneBuild = 26100

var procCopyFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("CopyFileW")

// volumeClones reports whether path's volume clones blocks natively on copy.
// NTFS never does; there a copy would cost full size, so it is refused.
func volumeClones(path string) bool {
	if windows.RtlGetVersion().BuildNumber < minBlockCloneBuild {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	volume := filepath.VolumeName(abs)
	if volume == "" {
		return false
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return false
	}
	var flags uint32
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return false
	}
	return flags&fileSupportsBlockRefcounting != 0
}

// cloneTree recreates src at dst on a block-cloning volume, copying each file
// with CopyFile, which clones within one ReFS volume on supported builds.
// Links and junctions are not recreated (that can need privileges); a tree
// holding one is reported unsupported so the caller falls back to Setup.
//
//nolint:gosec // G122: src is a tree AO itself moved under its data dir; links are recreated, never followed.
func cloneTree(src, dst string) error {
	if !volumeClones(src) || !strings.EqualFold(filepath.VolumeName(src), filepath.VolumeName(dst)) {
		return errCloneUnsupported
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case entry.IsDir():
			return os.Mkdir(target, 0o750)
		case entry.Type().IsRegular():
			return copyFileClone(path, target)
		default:
			return errCloneUnsupported
		}
	})
}

func copyFileClone(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	const failIfExists = 1
	ok, _, callErr := procCopyFileW.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), failIfExists) //nolint:gosec // Win32 call with UTF-16 path pointers
	if ok == 0 {
		return callErr
	}
	return nil
}
