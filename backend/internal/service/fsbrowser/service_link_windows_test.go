package fsbrowser

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// symlink 使用真实 NTFS junction，无需符号链接权限，也不修改进程权限。
// junction 的目标可为目录、文件或缺失路径；文件目标不等同 Windows 文件
// symlink，其 Stat 行为在测试日志中明确记录。清理只删除链接本身。
func symlink(t *testing.T, target, path string) {
	t.Helper()
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target, err := filepath.Abs(target)
	require.NoError(t, err)
	substitute, err := windows.UTF16FromString(`\??\` + target)
	require.NoError(t, err)
	printName, err := windows.UTF16FromString(target)
	require.NoError(t, err)
	names := append(substitute, printName...)
	// NTFS mount-point 数据包含 8 字节头、8 字节名称偏移表和 UTF-16 名称。
	// 名称长度不包含 NUL，而下一名称的偏移必须越过前一名称的 NUL。
	buffer := make([]byte, 16+2*len(names))
	binary.LittleEndian.PutUint32(buffer[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:12], uint16(2*(len(substitute)-1)))
	binary.LittleEndian.PutUint16(buffer[12:14], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16(2*(len(printName)-1)))
	for index, unit := range names {
		binary.LittleEndian.PutUint16(buffer[16+2*index:18+2*index], unit)
	}
	require.NoError(t, os.Mkdir(path, 0o700))
	t.Cleanup(func() { require.NoError(t, os.Remove(path)) })
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	require.NoError(t, err)
	defer func() { require.NoError(t, windows.CloseHandle(handle)) }()
	var returned uint32
	require.NoError(t, windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT,
		&buffer[0], uint32(len(buffer)), nil, 0, &returned, nil))
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		info, statErr := os.Stat(path)
		isDirectory := statErr == nil && info.IsDir()
		t.Logf("文件目标 junction：Stat.IsDir=%v，错误=%v；本夹具不代表 Windows 文件 symlink", isDirectory, statErr)
		require.ErrorIs(t, statErr, windows.ERROR_DIRECTORY)
		require.False(t, isDirectory)
	}
}
