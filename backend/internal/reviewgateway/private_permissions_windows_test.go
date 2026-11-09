//go:build windows

package reviewgateway

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertPrivatePath 读取真实对象的 owner 与 DACL，不能以 Windows 的模拟 mode 判断权限。
func assertPrivatePath(t *testing.T, path string, directory bool) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() != directory {
		t.Fatalf("cache object directory=%t: %v", directory, err)
	}
	assertPrivateHandle(t, file)
}

// assertPrivateHandle 验证有效授权只属于当前用户和系统管理员，并禁止父目录继承。
func assertPrivateHandle(t *testing.T, file *os.File) {
	t.Helper()
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ACL %s: %s", file.Name(), sd.String())
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("%s owner is not the current user: %v", file.Name(), err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("%s inherits parent permissions: %v", file.Name(), err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s has no private DACL: %v", file.Name(), err)
	}
	var ownerAccess windows.ACCESS_MASK
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("%s contains an unsupported effective ACE", file.Name())
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) // #nosec G103 -- 已验证 ACCESS_ALLOWED_ACE 类型，SID 位于 Windows 返回的有效 ACE 缓冲区内。
		if !sid.IsValid() || !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			t.Fatalf("%s grants access to another principal", file.Name())
		}
		if sid.Equals(user.User.Sid) {
			ownerAccess |= ace.Mask
		}
	}
	readWrite := windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_WRITE_DATA)
	if ownerAccess&readWrite != readWrite && ownerAccess&windows.ACCESS_MASK(windows.GENERIC_ALL) == 0 {
		t.Fatalf("%s does not grant owner read/write", file.Name())
	}
}

// privatePathTestRoot 只给本用例新建目录增加公共读取继承；保留句柄以恢复原 DACL。
func privatePathTestRoot(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(ptr, windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	original, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		_ = windows.CloseHandle(handle)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer func() { _ = windows.CloseHandle(handle) }()
		dacl, _, restoreErr := original.DACL()
		control, _, controlErr := original.Control()
		if restoreErr != nil || controlErr != nil {
			t.Errorf("restore ACL: %v %v", restoreErr, controlErr)
			return
		}
		protection := windows.SECURITY_INFORMATION(windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
		if control&windows.SE_DACL_PROTECTED != 0 {
			protection = windows.PROTECTED_DACL_SECURITY_INFORMATION
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|protection, nil, nil, dacl, nil); err != nil {
			t.Errorf("restore ACL: %v", err)
		}
	})
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	return path
}
