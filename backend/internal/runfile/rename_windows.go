//go:build windows

package runfile

import (
	"syscall"
	"unsafe"
)

// movefileReplaceExisting tells MoveFileEx to overwrite dst if it already
// exists. Mirrors MOVEFILE_REPLACE_EXISTING from the Win32 API; declared
// locally so we don't pull in golang.org/x/sys for a single constant.
const movefileReplaceExisting = 0x1

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

// atomicReplace renames src to dst, replacing dst if it exists. Go's
// os.Rename on Windows happens to do the same MoveFileEx call internally,
// but calling it directly makes the cross-platform contract explicit instead
// of leaning on a runtime implementation detail. The replace is atomic
// against concurrent readers — readers see either the old or the new file,
// never an empty or partially-written one.
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
		uintptr(unsafe.Pointer(srcPtr)), // #nosec G103 -- UTF16PtrFromString 创建 NUL 终止的源路径；直接传入同步 Call，Go 保持缓冲存活。
		uintptr(unsafe.Pointer(dstPtr)), // #nosec G103 -- 目标路径同样经 UTF-16/NUL 校验；指针不逃出此次 MoveFileExW 调用。
		uintptr(movefileReplaceExisting),
	)
	if ret == 0 {
		return err
	}
	return nil
}
