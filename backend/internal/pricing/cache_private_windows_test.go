package pricing

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertPrivateCachePath 读取真实对象的 owner 与 DACL，不能以 Windows 的模拟 mode 判断权限。
func assertPrivateCachePath(t *testing.T, path string, directory bool) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() != directory {
		t.Fatalf("cache object directory=%t: %v", directory, err)
	}
	assertPrivateCacheHandle(t, file)
}

// assertPrivateCacheHandle 验证有效授权只属于当前用户和系统管理员，并禁止父目录继承。
func assertPrivateCacheHandle(t *testing.T, file *os.File) {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
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
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
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

// allowPublicCacheAccess 模拟旧 Windows 缓存继承的 Everyone 读写权限，仅修改临时测试对象。
func allowPublicCacheAccess(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
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

// TestCacheInstallRepairsWindowsPermissions 覆盖旧目录及未变 blob 的权限修复；删除
// ensurePrivateDir 的 DACL 设置或匹配 blob 的权限修复调用，本用例都会失败。
func TestCacheInstallRepairsWindowsPermissions(t *testing.T) {
	cache := NewCache(t.TempDir())
	fixture := testCandidate(t, testBaseModels("0.1"))
	catalog, err := ParseCatalog(fixture.manifest, fixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Install(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Dir(filepath.Dir(cache.Root())), filepath.Dir(cache.Root()), cache.Root()}
	files := append([]string{filepath.Join(cache.Root(), "manifest.json")}, providerFilePaths(t, cache.Root(), fixture)...)
	for _, file := range files {
		paths = append(paths, filepath.Dir(filepath.Dir(file)), filepath.Dir(file), file)
	}
	for _, path := range paths {
		allowPublicCacheAccess(t, path)
	}
	if err := cache.Install(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		assertPrivateCachePath(t, path, info.IsDir())
	}
	if _, err := cache.Load(t.Context()); err != nil {
		t.Fatalf("repaired cache is unreadable: %v", err)
	}
}

// TestCreatePrivateCacheTempHasPrivateDACLBeforeWrite 删除 NtCreateFile 的
// SecurityDescriptor 参数会使空文件继承 Everyone 授权，本用例立即失败。
func TestCreatePrivateCacheTempHasPrivateDACLBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	allowPublicCacheAccess(t, dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := createPrivateCacheTemp(root)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assertPrivateCacheHandle(t, file)
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatalf("created cache object must be empty: %v", err)
	}
	if _, err := file.WriteString("pricing"); err != nil {
		t.Fatal(err)
	}
	contents, err := root.ReadFile(filepath.Base(file.Name()))
	if err != nil || string(contents) != "pricing" {
		t.Fatalf("private temp contents = %q, err = %v", contents, err)
	}
}
