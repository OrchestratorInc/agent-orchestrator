//go:build windows

package usage

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openReplaceableTranscript 仅为并发替换用例开放删除共享；普通 os.Open 会先阻止 rename。
// 仍用真实只读句柄取身份和正文，返回后由现有 Ingestor 负责关闭。
func openReplaceableTranscript(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// replaceTranscript 沿用仓库 ReplaceFileW 方式，在旧句柄仍打开时发布真实新文件；
// 不删除目标、不回退重试，保留系统默认元数据及 ACL 检查。
func replaceTranscript(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	replaceFile := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")
	replaced, _, err := replaceFile.Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), 0, 0, 0, 0) // #nosec G103 -- 两个 UTF-16 缓冲由标准转换拒绝 NUL，存活至同步系统调用返回。
	if replaced == 0 {
		return &os.LinkError{Op: "replace", Old: source, New: destination, Err: err}
	}
	return nil
}
