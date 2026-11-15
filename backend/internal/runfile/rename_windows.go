//go:build windows

package runfile

import (
	"syscall"
	"unsafe"
)

const movefileReplaceExisting = 0x1

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func atomicReplace(src, dst string) error {
	srcPtr, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	dstPtr, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	ret, _, err := moveFileExW.Call(
		uintptr(unsafe.Pointer(srcPtr)),
		uintptr(unsafe.Pointer(dstPtr)),
		uintptr(movefileReplaceExisting),
	)
	if ret == 0 {
		return err
	}
	return nil
}
