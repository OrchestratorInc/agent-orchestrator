package mobilebridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// identityDirectorySwapContext 在真实临时文件创建后的取消检查处替换目录
// junction，控制竞态时序但不替代文件操作，也不为生产代码增加测试钩子。
type identityDirectorySwapContext struct {
	context.Context
	dir      string
	replace  func(string) error
	replaced bool
	err      error
}

// Err 只在看到真实临时文件后注入一次目录替换，夹具错误直接阻止写入。
func (ctx *identityDirectorySwapContext) Err() error {
	if !ctx.replaced && ctx.err == nil {
		entries, err := os.ReadDir(ctx.dir)
		if err != nil {
			ctx.err = err
		} else {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".identity-") {
					ctx.replaced = true
					ctx.err = ctx.replace(entry.Name())
					break
				}
			}
		}
	}
	if ctx.err != nil {
		return ctx.err
	}
	return ctx.Context.Err()
}

// identityTestJunction 只创建和删除临时目录链接，不递归操作其目标。
func identityTestJunction(t *testing.T, link, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`$ErrorActionPreference = "Stop"; New-Item -ItemType Junction -Path $env:AO_TEST_IDENTITY_LINK -Target $env:AO_TEST_IDENTITY_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "AO_TEST_IDENTITY_LINK="+link, "AO_TEST_IDENTITY_TARGET="+target)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create identity junction: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove identity junction: %v", err)
		}
	})
}

// TestPrivateIdentityPublicationKeepsPinnedDirectory 验证父路径在创建后被
// 替换时，发布和清理仍只操作原目录；外部同名对象不能被发布或删除。
func TestPrivateIdentityPublicationKeepsPinnedDirectory(t *testing.T) {
	base := t.TempDir()
	pinned, outside, alias := filepath.Join(base, "pinned"), filepath.Join(base, "outside"), filepath.Join(base, "mobile")
	for _, dir := range []string{pinned, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	identityTestJunction(t, alias, pinned)
	const outsideIdentity = "outside identity must stay unchanged"
	const decoyContent = "outside temporary object must stay unchanged"
	if err := os.WriteFile(filepath.Join(outside, "identity.json"), []byte(outsideIdentity), 0o600); err != nil {
		t.Fatal(err)
	}
	var decoyName string
	ctx := &identityDirectorySwapContext{Context: context.Background(), dir: alias}
	ctx.replace = func(name string) error {
		decoyName = name
		if err := os.Remove(alias); err != nil {
			return err
		}
		identityTestJunction(t, alias, outside)
		return os.WriteFile(filepath.Join(outside, name), []byte(decoyContent), 0o600)
	}
	const content = `{"hostId":"h-pinned-installation"}`
	if err := writePrivateIdentityFile(ctx, filepath.Join(alias, "identity.json"), []byte(content)); err != nil {
		t.Fatalf("publish identity across parent replacement: %v", err)
	}
	if !ctx.replaced {
		t.Fatal("parent replacement did not run after the real temporary file was created")
	}
	pinnedPath := filepath.Join(pinned, "identity.json")
	data, err := os.ReadFile(pinnedPath)
	if err != nil || string(data) != content {
		t.Errorf("pinned identity = %q, err = %v; want original private content", data, err)
	} else {
		assertIdentityFilePrivate(t, pinnedPath)
	}
	entries, err := os.ReadDir(pinned)
	if err != nil || len(entries) != 1 || entries[0].Name() != "identity.json" {
		t.Errorf("pinned directory = %v, err = %v; want only identity.json", entries, err)
	}
	for name, expected := range map[string]string{"identity.json": outsideIdentity, decoyName: decoyContent} {
		data, err := os.ReadFile(filepath.Join(outside, name))
		if err != nil || string(data) != expected {
			t.Errorf("outside object %q = %q, err = %v; want %q", name, data, err, expected)
		}
	}
}
