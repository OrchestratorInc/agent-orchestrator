package binaryutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelectedBinaryDoesNotFallBack 覆盖显式选择的优先级与丢失后的失败闭锁。
func TestSelectedBinaryDoesNotFallBack(t *testing.T) {
	spec := RegisterSpec("selection-test", BinarySpec{Label: "selection-test", Names: []string{"chosen"}, WinNames: []string{"chosen.cmd"}})
	dir := t.TempDir()
	path := filepath.Join(dir, CandidateNames(spec)[0])
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	SetManagedBinDir(dir)
	SetSelectedBinaries(map[string]string{"selection-test": path})
	t.Cleanup(func() { SetSelectedBinaries(nil); SetManagedBinDir("") })
	got, err := ResolveBinary(t.Context(), spec)
	if err != nil || got != path {
		t.Fatalf("selected = %q, %v", got, err)
	}
	missing := filepath.Join(dir, "missing", filepath.Base(path))
	SetSelectedBinaries(map[string]string{"selection-test": missing})
	if got, err := ResolveBinary(t.Context(), spec); err == nil || got != "" {
		t.Fatalf("missing selection fell back to %q: %v", got, err)
	}
	SetSelectedBinaries(nil)
	if got, err := ResolveBinary(t.Context(), spec); err != nil || got != path {
		t.Fatalf("automatic detection = %q, %v", got, err)
	}
}

// TestScanKeepsFailedCandidatesAndHonorsDepth 将真实进程探测与目录边界放在同一条行为路径验证。
func TestScanKeepsFailedCandidatesAndHonorsDepth(t *testing.T) {
	spec := RegisterSpec("scan-test", BinarySpec{Label: "scan-test", Names: []string{"scan-cli"}, WinNames: []string{"scan-cli.cmd"}})
	dir := t.TempDir()
	name := CandidateNames(spec)[0]
	write := func(folder, body string) string {
		t.Helper()
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	success, failure := "#!/bin/sh\necho 1.2.3\n", "#!/bin/sh\necho broken >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		success, failure = "@echo off\r\necho 1.2.3\r\n", "@echo off\r\necho broken\r\nexit /b 1\r\n"
	}
	good := write(filepath.Join(dir, "bin"), success)
	bad := write(filepath.Join(dir, "other"), failure)
	write(filepath.Join(dir, strings.Repeat("deep"+string(filepath.Separator), scanMaxDepth+1)), success)
	result, err := ScanExecutables(t.Context(), "scan-test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 || !result.Truncated {
		t.Fatalf("scan = %+v", result)
	}
	if result.Candidates[0].Path != good || result.Candidates[0].Version != "1.2.3" || result.Candidates[0].Error != "" {
		t.Fatalf("working entry = %+v", result.Candidates[0])
	}
	if result.Candidates[1].Path != bad || result.Candidates[1].Error == "" {
		t.Fatalf("failed entry = %+v", result.Candidates[1])
	}
	if SelectedPath("scan-test") != "" {
		t.Fatal("scan changed the selection")
	}
}

// TestSelectedBinaryUsesHarnessIdentity 防止共享命令名的两个 Harness 互相覆盖或跳过身份校验。
func TestSelectedBinaryUsesHarnessIdentity(t *testing.T) {
	first := RegisterSpec("identity-a", BinarySpec{Names: []string{"shared"}, WinNames: []string{"shared.exe"}})
	second := first
	second.ValidateIdentity = func(context.Context, string) bool { return false }
	RegisterSpec("identity-b", second)
	path := filepath.Join(t.TempDir(), CandidateNames(first)[0])
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	SetSelectedBinaries(map[string]string{"identity-a": path, "identity-b": path})
	t.Cleanup(func() { SetSelectedBinaries(nil) })
	if _, selected, err := SelectedBinary(t.Context(), "identity-a"); !selected || err != nil {
		t.Fatal(selected, err)
	}
	if _, selected, err := SelectedBinary(t.Context(), "identity-b"); !selected || err == nil {
		t.Fatal(selected, err)
	}
}
