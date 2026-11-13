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

// blockMarker makes the next read of a marker file fail, as a write that
// cannot complete would, and returns a func that puts the file back.
func blockMarker(t *testing.T, dir, file string) func() {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".saved", path); err != nil {
			t.Fatal(err)
		}
	}
}

// Turning sharing off never leaves a name published without its user-owned
// mark: a later withdrawal would take the user's own value for AO's copy and
// remove it from their environment.
func TestUnpublishingNeverDropsOwnershipWhileStillPublished(t *testing.T) {
	ctx := context.Background()
	t.Run("device marker write fails", func(t *testing.T) {
		dir := t.TempDir()
		if err := SetDevice(ctx, dir, "GROQ_API_KEY", true); err != nil {
			t.Fatal(err)
		}
		if err := SetUserValue(ctx, dir, "GROQ_API_KEY", true); err != nil {
			t.Fatal(err)
		}
		restore := blockMarker(t, dir, deviceFilename)
		if err := SetDevice(ctx, dir, "GROQ_API_KEY", false); err == nil {
			t.Fatal("unpublished although the device marker could not be written")
		}
		restore()
		if device, _ := DeviceNames(dir); !device["GROQ_API_KEY"] {
			t.Fatal("device marker changed although its write failed")
		}
		if users, _ := UserValueNames(dir); !users["GROQ_API_KEY"] {
			t.Fatal("a still-published name lost its user-owned mark")
		}
	})
	t.Run("user-owned marker write fails", func(t *testing.T) {
		dir := t.TempDir()
		if err := SetDevice(ctx, dir, "GROQ_API_KEY", true); err != nil {
			t.Fatal(err)
		}
		if err := SetUserValue(ctx, dir, "GROQ_API_KEY", true); err != nil {
			t.Fatal(err)
		}
		restore := blockMarker(t, dir, userValueFilename)
		if err := SetDevice(ctx, dir, "GROQ_API_KEY", false); err == nil {
			t.Fatal("reported success although the user-owned marker could not be written")
		}
		restore()
		// The name is unpublished first, so nothing withdraws it; the
		// leftover mark is cleared when AO next publishes its own value.
		if device, _ := DeviceNames(dir); device["GROQ_API_KEY"] {
			t.Fatal("still published although the device marker was cleared first")
		}
	})
}
