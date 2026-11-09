//go:build windows

package reviewgateway

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestReviewerManifestIsPrivateBeforeWrite 验证首次写入前已有受保护 DACL，而不是事后收紧。
func TestReviewerManifestIsPrivateBeforeWrite(t *testing.T) {
	path := filepath.Join(privatePathTestRoot(t), "manifest.json")
	file, err := openPrivateManifest(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	assertPrivateHandle(t, file)
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		t.Fatalf("new manifest is not empty: %v", err)
	}
	if _, err := file.WriteString("private authority"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".replaced"); err == nil {
		t.Fatal("open manifest identity was replaced")
	}
}

// TestReviewerRepairsOwnedManifestWithoutChangingAuthority 覆盖旧对象复用：收紧 ACL 但不改内容或文件身份。
func TestReviewerRepairsOwnedManifestWithoutChangingAuthority(t *testing.T) {
	root := t.TempDir()
	manifest := testManifest(t)
	env, err := PrepareEnvironment(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	before := reviewerFileIdentity(t, env.ManifestPath)
	raw, err := os.ReadFile(env.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// 仅给本用例已经创建的 AO 对象增加 Everyone 读取；原 DACL 用保存的句柄恢复。
	for _, path := range []string{env.Root, env.ManifestPath} {
		allowReviewerFixtureRead(t, path)
	}
	again, err := PrepareEnvironment(root, manifest)
	if err != nil || again.ManifestPath != env.ManifestPath {
		t.Fatalf("reuse = %#v, %v", again, err)
	}
	assertPrivatePath(t, env.Root, true)
	assertPrivatePath(t, env.ManifestPath, false)
	after := reviewerFileIdentity(t, env.ManifestPath)
	if before != after {
		t.Fatalf("reuse replaced authority: before=%v after=%v", before, after)
	}
	got, err := os.ReadFile(env.ManifestPath)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("reuse changed manifest bytes: %v", err)
	}
}

// reviewerFileIdentity 在关闭句柄前固定卷号和文件 ID，避免 Windows 路径 Stat 的延迟身份读取。
func reviewerFileIdentity(t *testing.T, path string) [3]uint32 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	return [3]uint32{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}
}

// TestReviewerRejectsManifestHardLinkWithoutChangingTarget 删除单链接校验会修改外部对象 ACL，本用例必须失败。
func TestReviewerRejectsManifestHardLinkWithoutChangingTarget(t *testing.T) {
	root := t.TempDir()
	manifest := testManifest(t)
	env, err := PrepareEnvironment(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(env.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(privatePathTestRoot(t), "external.json")
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	security, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(env.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, env.ManifestPath); err != nil {
		t.Fatal(err)
	}
	linkedSecurity, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || linkedSecurity.String() != security.String() {
		t.Fatalf("hard-link fixture changed target ACL: %v", err)
	}
	if _, err := PrepareEnvironment(root, manifest); err == nil {
		t.Fatal("hard-linked authority accepted")
	}
	after, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || after.String() != security.String() {
		t.Fatalf("external object ACL changed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("external object contents changed: %v", err)
	}
}

// TestReviewerRejectsAncestorJunction 使用无提权的真实目录 junction，验证中间路径也不能重解析。
func TestReviewerRejectsAncestorJunction(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	junction := filepath.Join(root, "reviewer-runtime")
	if out, err := exec.CommandContext(t.Context(), "cmd", "/D", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("create scratch junction: %v (%s)", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Errorf("remove scratch junction: %v", err)
		}
	})
	// 直接打开深层路径，确保不是只靠最外层目录准备阻断 junction。
	if _, err := openPrivateManifest(filepath.Join(junction, "manifest.json"), true); err == nil {
		t.Fatal("ancestor junction accepted")
	}
	if _, err := PrepareHostTrustedEnvironment(root, "reviewer"); err == nil {
		t.Fatal("reviewer root junction accepted")
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("junction target changed: entries=%v err=%v", entries, err)
	}
}

// allowReviewerFixtureRead 只更改测试创建对象；固定恢复句柄避免清理跟随替换后的路径。
func allowReviewerFixtureRead(t *testing.T, path string) {
	t.Helper()
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
		dacl, _, err := original.DACL()
		if err == nil {
			err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
		}
		if err != nil {
			t.Errorf("restore fixture ACL: %v", err)
		}
	})
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)")
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
}
