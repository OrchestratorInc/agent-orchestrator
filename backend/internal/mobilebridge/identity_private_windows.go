package mobilebridge

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// identityNeedsPrivacyRepair 验证当前用户所有权、受保护的 DACL 与有效授权。
// 无法读取安全信息时直接返回错误，不以重写掩盖既有文件的读取失败。
func identityNeedsPrivacyRepair(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, err
	}
	sd, err := windows.GetNamedSecurityInfo(extendedConfigPath(path), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return true, nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return true, nil
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return true, nil
	}
	var ownerAccess windows.ACCESS_MASK
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return true, nil
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) // #nosec G103 -- GetAce 返回系统校验 DACL 内的 ACCESS_ALLOWED_ACE；已检查 ACE 类型，SID 从 SidStart 读取并立即 IsValid。
		if !sid.IsValid() || !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			return true, nil
		}
		if sid.Equals(user.User.Sid) {
			ownerAccess |= ace.Mask
		}
	}
	read := ownerAccess&windows.ACCESS_MASK(windows.FILE_READ_DATA|windows.GENERIC_READ|windows.GENERIC_ALL) != 0
	write := ownerAccess&windows.ACCESS_MASK(windows.FILE_WRITE_DATA|windows.GENERIC_WRITE|windows.GENERIC_ALL) != 0
	return !read || !write, nil
}

// createPrivateIdentityTemp 在固定目录句柄内首次创建时同时授予私有 DACL。
// 只使用随机叶子名，避免父路径替换；空文件从未继承其他账户的读取授权。
func createPrivateIdentityTemp(ctx context.Context, root *os.Root) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	name := ".identity-" + rand.Text()
	leaf, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
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
		return nil, &os.PathError{Op: "create private identity", Path: name, Err: err}
	}
	return os.NewFile(uintptr(handle), filepath.Join(root.Name(), name)), nil
}

// writePrivateIdentityFile 发布创建时即私有的新对象，不原地改写旧公开对象。
// 创建、发布和清理共用一个 Root，不重新解析父路径；Root.Rename 保留新对象
// 的 DACL，不能使用会合并旧目标 ACL 的 ReplaceFileW。任何错误直接返回。
func writePrivateIdentityFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	file, err := createPrivateIdentityTemp(ctx, root)
	if err != nil {
		return err
	}
	name := filepath.Base(file.Name())
	defer func() { _ = root.Remove(name) }()
	defer func() { _ = file.Close() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return root.Rename(name, filepath.Base(path))
}
