package chat

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertPrivateRenderHandle 检查创建返回时的真实对象，避免按路径重新打开
// 已被替换的文件来验证权限。
func assertPrivateRenderHandle(t *testing.T, file *os.File) {
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
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("created file has an unexpected owner: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("created file inherits its parent's grants: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("created file has no private DACL: %v", err)
	}
	ownerReadWrite := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatal("created file has an unsupported effective ACE")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			t.Fatal("created file grants access to another principal")
		}
		if sid.Equals(user.User.Sid) {
			bits := windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_WRITE_DATA)
			ownerReadWrite = ownerReadWrite || ace.Mask&bits == bits || ace.Mask&windows.ACCESS_MASK(windows.GENERIC_ALL) != 0
		}
	}
	if !ownerReadWrite {
		t.Fatal("created file does not grant owner read/write")
	}
}

// allowInheritedRenderReads 给临时父目录设置 Everyone 的可继承读取权限。
func allowInheritedRenderReads(t *testing.T, dir string) {
	t.Helper()
	// 当前进程 TOKEN_QUERY 伪句柄不拥有可关闭资源。
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() +
		")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// renderTestJunction 建立真实目录链接，只删除链接本身，不递归触碰目标。
func renderTestJunction(t *testing.T, link, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`$ErrorActionPreference = "Stop"; New-Item -ItemType Junction -Path $env:AO_TEST_RENDER_LINK -Target $env:AO_TEST_RENDER_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "AO_TEST_RENDER_LINK="+link, "AO_TEST_RENDER_TARGET="+target)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create render junction: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove render junction: %v", err)
		}
	})
}

// TestCreateRenderArtifactHasPrivateDACLBeforeWrite 检查首次返回且尚无内容
// 的对象权限，而不是写入完成后才检查最终权限。
func TestCreateRenderArtifactHasPrivateDACLBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	allowInheritedRenderReads(t, dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	file, err := createRenderArtifact(context.Background(), root, "private.html")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	assertPrivateRenderHandle(t, file)
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatalf("new private file = %v, err = %v; want empty", info, err)
	}
	if _, err := file.WriteString("private content"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "private.html"))
	if err != nil || string(data) != "private content" {
		t.Fatalf("private file = %q, err = %v", data, err)
	}
}

// TestCreateRenderArtifactRejectsUnsafeLeafAndExistingFile 验证单一叶子约束
// 与 FILE_CREATE 的独占创建语义，既有文件不能被覆盖。
func TestCreateRenderArtifactRejectsUnsafeLeafAndExistingFile(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for _, name := range []string{"", ".", "..", "../outside.html", `..\outside.html`, "nested/page.html", `C:\outside.html`, "page.html:stream"} {
		if file, err := createRenderArtifact(context.Background(), root, name); err == nil {
			_ = file.Close()
			t.Errorf("unsafe leaf %q was accepted", name)
		}
	}
	path := filepath.Join(dir, "existing.html")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if file, err := createRenderArtifact(context.Background(), root, "existing.html"); !errors.Is(err, os.ErrExist) {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("existing-file error = %v, want exist", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}

// TestCreateRenderArtifactUsesPinnedRootDirectory 区分目录句柄的共享删除防护
// 与路径替换，并替换 root 的 junction 别名来验证固定对象上的创建行为。
func TestCreateRenderArtifactUsesPinnedRootDirectory(t *testing.T) {
	base := t.TempDir()
	dir, moved, outside := filepath.Join(base, "managed"), filepath.Join(base, "moved"), filepath.Join(base, "outside")
	alias := filepath.Join(base, "root")
	for _, path := range []string{dir, outside} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	renderTestJunction(t, alias, dir)
	root, err := os.OpenRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	pinnedDir := moved
	if err := os.Rename(dir, moved); err != nil {
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("unexpected pinned-directory rename error: %v", err)
		}
		t.Logf("fixed root handle rejected directory replacement: %v", err)
		pinnedDir = dir
	}
	if err := os.Remove(alias); err != nil {
		t.Fatalf("remove original root junction: %v", err)
	}
	renderTestJunction(t, alias, outside)
	file, err := createRenderArtifact(context.Background(), root, "kept.html")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	assertPrivateRenderHandle(t, file)
	if _, err := file.WriteString("kept inside root"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(pinnedDir, "kept.html"))
	if err != nil || string(data) != "kept inside root" {
		t.Fatalf("pinned file = %q, err = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "kept.html")); !os.IsNotExist(err) {
		t.Fatalf("outside root was touched: %v", err)
	}
}

// TestCreateRenderArtifactRefusesExistingJunction 验证叶子 reparse point
// 不能导致创建过程写入外部目录。
func TestCreateRenderArtifactRefusesExistingJunction(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	renderTestJunction(t, filepath.Join(dir, "escape.html"), outside)
	if file, err := createRenderArtifact(context.Background(), root, "escape.html"); err == nil {
		_ = file.Close()
		t.Fatal("existing junction was followed")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("junction target changed: %v, %v", entries, err)
	}
}

// TestCreateRenderArtifactCancelledCreatesNothing 保留取消时不创建对象的边界。
func TestCreateRenderArtifactCancelledCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := createRenderArtifact(ctx, root, "cancelled.html"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled create left files: %v, %v", entries, err)
	}
}
