package mobilebridge

import (
	"path/filepath"
	"testing"
)

// TestLoadConfigLongPath 验证替换 Windows 读取实现后仍支持超过 MAX_PATH 的配置位置。
func TestLoadConfigLongPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 280 {
		dir = filepath.Join(dir, "long-config-directory")
	}
	path := filepath.Join(dir, "config.json")
	want := State{Enabled: true, Password: "long-path", LastPort: 3011}
	if err := Save(path, want); err != nil {
		t.Fatalf("save long config path: %v", err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("load long config path = %+v, err = %v; want %+v", got, err, want)
	}
	want.LastPort++
	if err := Save(path, want); err != nil {
		t.Fatalf("replace long config path: %v", err)
	}
	got, err = Load(path)
	if err != nil || got != want {
		t.Fatalf("replaced long config path = %+v, err = %v; want %+v", got, err, want)
	}
}
