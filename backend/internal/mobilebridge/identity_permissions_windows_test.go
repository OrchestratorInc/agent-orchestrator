package mobilebridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertIdentityFilePrivate 验证实际 DACL、当前用户所有权与有效允许项，
// 只允许当前用户、SYSTEM 和 Administrators，不用 POSIX mode 推断 Windows 权限。
func assertIdentityFilePrivate(t *testing.T, path string) {
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
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("identity owner is not current user: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("identity DACL is not protected from inheritance: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("identity DACL is missing: %v", err)
	}
	ownerReadWrite := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			t.Fatalf("read identity ACE %d: %v", index, err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("identity has unsupported effective ACE %d", ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			t.Fatal("identity grants access to an untrusted principal")
		}
		if sid.Equals(user.User.Sid) {
			bits := windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_WRITE_DATA)
			ownerReadWrite = ownerReadWrite || ace.Mask&bits == bits || ace.Mask&windows.ACCESS_MASK(windows.GENERIC_ALL) != 0
		}
	}
	if !ownerReadWrite {
		t.Fatal("identity does not grant the owner read/write")
	}
}

// identityTestSID 读取当前测试进程 SID，所有 ACL 修改只作用于 t.TempDir。
func identityTestSID(t *testing.T) string {
	t.Helper()
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

// setIdentityTestDACL 为临时夹具配置真实 Windows DACL。
func setIdentityTestDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// TestEnsureIdentityRepairsLegacyDACLWithoutRotatingHostID 验证旧公开文件在
// 硬件信息未变化时也升级为私有对象，且保留现存 HostID。
func TestEnsureIdentityRepairsLegacyDACLWithoutRotatingHostID(t *testing.T) {
	dir := t.TempDir()
	sid := identityTestSID(t)
	setIdentityTestDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GR;;;WD)")
	path := filepath.Join(dir, "identity.json")
	ifaces := []net.Interface{iface("en0", "aa:bb:cc:dd:ee:01")}
	old := Identity{HostID: "h-existing", Fingerprint: MachineFingerprint(ifaces), Fingerprints: InterfaceFingerprints(ifaces)}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureIdentityFor(path, ifaces)
	if err != nil || got.HostID != old.HostID {
		t.Fatalf("legacy identity = %+v, err = %v; want HostID %q", got, err, old.HostID)
	}
	assertIdentityFilePrivate(t, path)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "identity.json" {
		t.Fatalf("identity directory = %v, err = %v; want only identity.json", entries, err)
	}
}

// TestPrivateIdentityTempHasDACLBeforeWrite 验证父目录允许 Everyone 读取时，
// 临时文件从创建返回、尚未写入内容起就拥有实际私有 DACL。
func TestPrivateIdentityTempHasDACLBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	sid := identityTestSID(t)
	setIdentityTestDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GR;;;WD)")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	file, err := createPrivateIdentityTemp(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	t.Cleanup(func() { _ = file.Close() })
	assertIdentityFilePrivate(t, file.Name())
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatalf("new private identity size = %v, err = %v; want an empty private file", info, err)
	}
}

// TestEnsureIdentityReadFailureDoesNotOverwrite 验证真实读取权限拒绝保持原
// 错误与文件内容，不创建替代身份来覆盖无法读取的文件。
func TestEnsureIdentityReadFailureDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	const original = `{"hostId":"h-do-not-overwrite"}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	sid := identityTestSID(t)
	restore := "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)"
	t.Cleanup(func() { setIdentityTestDACL(t, path, restore) })
	setIdentityTestDACL(t, path, "D:P(D;;0x1;;;"+sid+")(A;;FA;;;"+sid+")(A;;FA;;;SY)(A;;FA;;;BA)")
	if _, err := EnsureIdentityFor(path, nil); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("read-denied identity error = %v, want permission error", err)
	}
	setIdentityTestDACL(t, path, restore)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("unreadable identity changed: %q, err = %v", data, err)
	}
}
