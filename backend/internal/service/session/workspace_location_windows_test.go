//go:build windows

package session

import (
	"testing"

	"golang.org/x/sys/windows"
)

// unreadableWorkspace 只拒绝本用例新建目录的 FILE_LIST_DIRECTORY；保留句柄以恢复原 DACL。
func unreadableWorkspace(t *testing.T) string {
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
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;0x1;;;WD)(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
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
	t.Logf("scratch directory DACL: %s", sd.String())
	return path
}
