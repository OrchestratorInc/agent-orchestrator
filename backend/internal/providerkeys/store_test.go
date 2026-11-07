package providerkeys

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSavePersistsKeysWithoutReplacingOtherProviders(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"OPENAI_API_KEY": "openai-secret", "ANTHROPIC_API_KEY": "anthropic-secret"} {
		if err := Save(context.Background(), dir, name, value); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := Load(dir)
	if err != nil || keys["OPENAI_API_KEY"] != "openai-secret" || keys["ANTHROPIC_API_KEY"] != "anthropic-secret" {
		t.Fatalf("Load = %v, %v", keys, err)
	}
	if err := Save(context.Background(), dir, "OPENAI_API_KEY", "updated"); err != nil {
		t.Fatal(err)
	}
	keys, err = Load(dir)
	if err != nil || keys["OPENAI_API_KEY"] != "updated" || keys["ANTHROPIC_API_KEY"] != "anthropic-secret" {
		t.Fatalf("replacement Load = %v, %v", keys, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path(dir))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("provider store permissions = %v, %v", info, err)
		}
	}
}

func TestSaveRejectsUnsafeValuesAndDoesNotExposeThemInError(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, value string }{
		{"AO_SESSION_ID", "secret"}, {"BAD=NAME", "secret"}, {"OPENAI_API_KEY", "line\nbreak"}, {"OPENAI_API_KEY", strings.Repeat("x", 16*1024+1)},
	} {
		if err := Save(context.Background(), dir, tc.name, tc.value); err == nil || strings.Contains(err.Error(), tc.value) {
			t.Fatalf("Save(%s) error = %v", tc.name, err)
		}
	}
	if _, err := os.Stat(path(dir)); !os.IsNotExist(err) {
		t.Fatalf("unexpected credential file: %v", err)
	}
}

func TestLoadRejectsInvalidStoreWithoutOverwritingIt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, filename)
	if err := os.WriteFile(file, []byte(`{"OPENAI_API_KEY":"one\ntwo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted invalid credential store")
	}
	if err := Save(context.Background(), dir, "ANTHROPIC_API_KEY", "secret"); err == nil {
		t.Fatal("overwrote invalid credential store")
	}
}

func TestRemoveDeletesKeyAndDeviceMarker(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if err := Save(ctx, dir, name, "secret"); err != nil {
			t.Fatal(err)
		}
		if err := SetDevice(ctx, dir, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(ctx, dir, "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	keys, err := Load(dir)
	if err != nil || len(keys) != 1 || keys["ANTHROPIC_API_KEY"] != "secret" {
		t.Fatalf("Load after Remove = %v, %v", keys, err)
	}
	device, err := DeviceNames(dir)
	if err != nil || len(device) != 1 || !device["ANTHROPIC_API_KEY"] {
		t.Fatalf("DeviceNames after Remove = %v, %v", device, err)
	}
	if err := Remove(ctx, dir, "OPENAI_API_KEY"); err != nil {
		t.Fatalf("removing an absent key = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, deviceFilename))
	if err != nil || strings.Contains(string(data), "secret") {
		t.Fatalf("device list holds a value or is missing: %s, %v", data, err)
	}
}
