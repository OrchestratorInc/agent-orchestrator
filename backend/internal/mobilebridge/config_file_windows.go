package mobilebridge

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// ReplaceFileW 发布期间可能短暂让 CreateFile 观察到目标不存在。
// 仅同步配置读取与文件发布，临时文件写入、fsync 和连接器操作不持有该锁。
var configPublicationMu sync.RWMutex

// readConfigFile 在发布之间读取完整快照；退出时先关闭读句柄，再释放读锁，
// 避免把 Windows 文件发布窗口误报为配置丢失或 bridge 禁用。
func readConfigFile(path string) ([]byte, error) {
	configPublicationMu.RLock()
	defer configPublicationMu.RUnlock()
	file, err := openConfigFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// openConfigFile 允许 Save 在配置读句柄仍打开时原子替换文件。
// os.Open 缺少 FILE_SHARE_DELETE，会让并发 Status 轮询阻断 Windows rename。
func openConfigFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(extendedConfigPath(path))
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

// replaceAtomicFile 对现存目标使用 ReplaceFileW，以允许 share-delete 读者
// 持有完整旧快照。flags 为 0，保留 Windows 元数据和 ACL 合并检查；
// 只有目标尚不存在时回退 rename，其他错误直接返回，不主动删除目标或重试。
func replaceAtomicFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(extendedConfigPath(source))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: err}
	}
	to, err := windows.UTF16PtrFromString(extendedConfigPath(destination))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: err}
	}
	configPublicationMu.Lock()
	defer configPublicationMu.Unlock()
	replaced, _, err := replaceFileW.Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), 0, 0, 0, 0) // #nosec G103 -- UTF16PtrFromString 已拒绝 NUL；两个 UTF-16 缓冲在同步 ReplaceFileW.Call 返回前存活。
	if replaced != 0 {
		return nil
	}
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return os.Rename(source, destination)
	}
	return &os.LinkError{Op: "rename", Old: source, New: destination, Err: err}
}

// extendedConfigPath 为长路径补上标准库使用的扩展前缀，保留短路径、设备路径
// 和已有前缀的行为；添加前缀前先规范化绝对路径，因为前缀会关闭 Windows 规范化。
func extendedConfigPath(path string) string {
	if strings.HasPrefix(path, `\??\`) ||
		len(path) >= 4 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) &&
			(path[2] == '?' || path[2] == '.') && os.IsPathSeparator(path[3]) {
		return path
	}
	full, err := filepath.Abs(path)
	if err != nil || len(full) < 248 {
		return path
	}
	if strings.HasPrefix(full, `\\`) {
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}
