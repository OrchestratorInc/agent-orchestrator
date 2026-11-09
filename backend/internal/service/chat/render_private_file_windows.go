package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createRenderArtifact 在 os.Root 已固定的目录句柄内独占创建单一叶子文件。
// 私有 DACL 随 NtCreateFile 一起提交，避免继承权限窗口；拒绝已有对象和
// reparse point，不先做路径检查再按绝对路径创建文件。
func createRenderArtifact(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return nil, &os.PathError{Op: "create render", Path: name, Err: os.ErrInvalid}
	}
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	sd, err := renderArtifactSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	leaf, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, &os.PathError{Op: "create render", Path: name, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         leaf,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: sd,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.GENERIC_WRITE|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		var native windows.NTStatus
		if errors.As(err, &native) {
			err = native.Errno()
		}
		return nil, &os.PathError{Op: "create render", Path: name, Err: err}
	}
	return os.NewFile(uintptr(handle), filepath.Join(root.Name(), name)), nil
}

// renderArtifactSecurityDescriptor 只授权当前用户、SYSTEM 和 Administrators，
// 显式指定当前用户所有权，并关闭从父目录继承权限。
func renderArtifactSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() +
		"D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
}
