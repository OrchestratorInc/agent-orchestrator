package pricing

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ensurePrivateDir 创建时即设置私有 DACL，并修复旧缓存目录的继承授权。
func ensurePrivateDir(directory string) error {
	parent := filepath.Dir(directory)
	root, err := os.OpenRoot(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := ensurePrivateDir(parent); err != nil {
			return err
		}
		root, err = os.OpenRoot(parent)
	}
	if err != nil {
		return fmt.Errorf("open pricing parent directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := openPrivateCacheObject(root, filepath.Base(directory), true, windows.FILE_OPEN_IF)
	if err != nil {
		return fmt.Errorf("protect pricing directory: %w", err)
	}
	return file.Close()
}

// protectCacheFile 只收紧当前用户拥有的普通 blob，不跟随叶子 reparse point。
func protectCacheFile(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	file, err := openPrivateCacheObject(root, filepath.Base(path), false, windows.FILE_OPEN)
	if err != nil {
		return err
	}
	return file.Close()
}

// createPrivateCacheTemp 返回尚未写入内容、创建时即私有的临时对象。
func createPrivateCacheTemp(root *os.Root) (*os.File, error) {
	return openPrivateCacheObject(root, ".pricing-"+rand.Text(), false, windows.FILE_CREATE)
}

// openPrivateCacheObject 沿用项目的 NtCreateFile/os.Root 模式：固定父句柄、独占
// 创建临时文件，并在首次创建时提交 owner 和受保护 DACL。旧对象只允许当前用户
// 所有的普通文件或目录；权限错误直接返回，调用方不得继续发布清单。
func openPrivateCacheObject(root *os.Root, name string, directory bool, disposition uint32) (_ *os.File, err error) {
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() +
		"D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return nil, err
	}
	leaf, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         leaf,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: sd,
	}
	options := uint32(windows.FILE_NON_DIRECTORY_FILE)
	if directory {
		options = windows.FILE_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, disposition,
		options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		var native windows.NTStatus
		if errors.As(err, &native) {
			err = native.Errno()
		}
		return nil, &os.PathError{Op: "open private pricing object", Path: name, Err: err}
	}
	file := os.NewFile(uintptr(handle), filepath.Join(root.Name(), name))
	if disposition == windows.FILE_CREATE {
		return file, nil
	}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.New("pricing cache object is a reparse point")
	}
	current, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := current.Owner()
	if err != nil {
		return nil, err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return nil, errors.New("pricing cache object is not owned by the current user")
	}
	dacl, _, err := sd.DACL()
	if err == nil {
		err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}
	if err != nil {
		return nil, err
	}
	return file, nil
}
