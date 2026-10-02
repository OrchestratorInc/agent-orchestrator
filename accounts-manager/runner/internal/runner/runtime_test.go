package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteRuntimeRecordIsPrivateAndSecretFree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	chmodPrivateDir(t, root)
	record := RuntimeRecord{
		PID:             1234,
		Port:            43127,
		InstanceID:      "instance-1",
		RunnerVersion:   "runner-test",
		UpstreamVersion: "v7.3.8",
		StartedAt:       time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
	}
	if err := WriteRuntimeRecord(root, record); err != nil {
		t.Fatalf("WriteRuntimeRecord() error = %v", err)
	}

	path := filepath.Join(root, "runtime.json")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime mode = %v, want regular 0600", info.Mode())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret", "token", "authDir", "config"} {
		if strings.Contains(strings.ToLower(string(b)), strings.ToLower(forbidden)) {
			t.Fatalf("runtime record exposed %q: %s", forbidden, b)
		}
	}
	var got RuntimeRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != record {
		t.Fatalf("runtime record = %#v, want %#v", got, record)
	}
}

func TestWriteRuntimeRecordRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root := t.TempDir()
	chmodPrivateDir(t, root)
	target := filepath.Join(root, "target")
	writePrivateFile(t, target, "keep")
	if err := os.Symlink(target, filepath.Join(root, "runtime.json")); err != nil {
		t.Fatal(err)
	}
	err := WriteRuntimeRecord(root, RuntimeRecord{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "regular") {
		t.Fatalf("WriteRuntimeRecord() error = %v, want symlink rejection", err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "keep" {
		t.Fatalf("symlink target was modified: %q", b)
	}
}
