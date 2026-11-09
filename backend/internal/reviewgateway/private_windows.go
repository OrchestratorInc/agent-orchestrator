//go:build windows

package reviewgateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ensurePrivateDir 创建时即限制 DACL；既有 AO 目录必须属于当前用户且不是 reparse point。
func ensurePrivateDir(path string) error {
	parent, err := openReviewerParent(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
			return err
		}
		parent, err = openReviewerParent(path)
	}
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	file, err := openPrivateReviewerObject(parent, filepath.Base(path), true, windows.FILE_OPEN_IF)
	if err != nil {
		return err
	}
	return file.Close()
}

// openPrivateManifest 固定父目录及文件身份后才读写，复用时也检查所有者和硬链接。
func openPrivateManifest(path string, create bool) (*os.File, error) {
	parent, err := openReviewerParent(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_CREATE
	}
	return openPrivateReviewerObject(parent, filepath.Base(path), false, disposition)
}

// openReviewerParent 从卷根逐级按父句柄打开，拒绝中间目录重解析，避免路径检查后再跟随别名。
func openReviewerParent(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("review gateway: private path must be absolute")
	}
	parentPath := filepath.Dir(filepath.Clean(path))
	volume := filepath.VolumeName(parentPath) + string(filepath.Separator)
	parent, err := os.Open(volume)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(volume, parentPath)
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	if rel == "." {
		return parent, nil
	}
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		next, openErr := openReviewerObject(parent, name, true, windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_OPEN, nil)
		_ = parent.Close()
		if openErr != nil {
			return nil, openErr
		}
		parent = next
	}
	return parent, nil
}

// openPrivateReviewerObject 沿用 NtCreateFile 的创建时安全描述符；旧对象只收紧当前用户拥有的单一对象。
func openPrivateReviewerObject(parent *os.File, name string, directory bool, disposition uint32) (_ *os.File, err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() +
		"D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
	if err != nil {
		return nil, err
	}
	file, err := openReviewerObject(parent, name, directory, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC, disposition, sd)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	handle := windows.Handle(file.Fd())
	current, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := current.Owner()
	if err != nil {
		return nil, err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return nil, errors.New("review gateway: private object owner is unsafe")
	}
	// SetSecurityInfo 会传播到尚未验证的子项，甚至改动其外部硬链接；只设置当前句柄。
	if err := windows.SetKernelObjectSecurity(handle,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, sd); err != nil {
		return nil, err
	}
	return file, nil
}

// openReviewerObject 以固定父句柄和不跟随重解析的方式打开，拒绝硬链接后才允许调用方改权限或内容。
func openReviewerObject(parent *os.File, name string, directory bool, access, disposition uint32, sd *windows.SECURITY_DESCRIPTOR) (*os.File, error) {
	leaf, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: windows.Handle(parent.Fd()),
		ObjectName: leaf, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: sd,
	}
	options := uint32(windows.FILE_NON_DIRECTORY_FILE)
	if directory {
		options = windows.FILE_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attributes, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		disposition, options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		var native windows.NTStatus
		if errors.As(err, &native) {
			err = native.Errno()
		}
		return nil, &os.PathError{Op: "open private reviewer object", Path: filepath.Join(parent.Name(), name), Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!directory && info.NumberOfLinks != 1) {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("review gateway: private object is a reparse point or hard link")
	}
	return os.NewFile(uintptr(handle), filepath.Join(parent.Name(), name)), nil
}
