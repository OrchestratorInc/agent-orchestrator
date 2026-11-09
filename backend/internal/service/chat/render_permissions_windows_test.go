package chat_test

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertRenderArtifactPrivate 检查实际 Windows DACL，拒绝继承授权和任何
// 非当前用户、System 或 Administrators 的有效授权，不用 POSIX mode 代替访问控制。
func assertRenderArtifactPrivate(t *testing.T, file string) {
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
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("artifact owner is not the current user: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("artifact DACL inherits other principals: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("artifact DACL is missing: %v", err)
	}
	userCanReadWrite := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			t.Fatalf("read artifact ACE %d: %v", index, err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("artifact has unsupported effective ACE type %d", ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || (!sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(administrators)) {
			t.Fatal("artifact grants access to an untrusted principal")
		}
		if sid.Equals(user.User.Sid) {
			readWrite := windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_WRITE_DATA)
			userCanReadWrite = ace.Mask&readWrite == readWrite || ace.Mask&windows.ACCESS_MASK(windows.GENERIC_ALL) != 0
		}
	}
	if !userCanReadWrite {
		t.Fatal("artifact does not grant the current user read and write access")
	}
}
