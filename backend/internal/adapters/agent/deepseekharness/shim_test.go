package deepseekharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeShim writes a dsh-shaped npm cmd-shim into dir and returns its path.
func writeShim(t *testing.T, dir, content string) string {
	t.Helper()
	shim := filepath.Join(dir, "dsh.cmd")
	if err := os.WriteFile(shim, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return shim
}

const dshShimBody = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\nIF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\nendLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@deepseek-ai\\dsh\\lib\\bin.js\" %*\r\n"

func TestResolveWindowsShimArgvStartsNodeEntryPointDirectly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the shim body below uses Windows path separators; the rewrite only fires for a .cmd binary, which the binary resolver only produces on Windows")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(node, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shim := writeShim(t, dir, dshShimBody)

	got := resolveWindowsShimArgv([]string{shim, "--profile", "headless", "long task"}, exec.LookPath)
	want := []string{node, entry, "--profile", "headless", "long task"}
	if len(got) != len(want) {
		t.Fatalf("rewritten argv = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rewritten argv = %#v, want %#v", got, want)
		}
	}
}

func TestResolveWindowsShimArgvFallsBackToPathNode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the shim body below uses Windows path separators; the rewrite only fires for a .cmd binary, which the binary resolver only produces on Windows")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shim := writeShim(t, dir, dshShimBody)

	pathNode := filepath.Join(t.TempDir(), "node.exe")
	if err := os.WriteFile(pathNode, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lookPath := func(name string) (string, error) {
		if name == "node.exe" || name == "node" {
			return pathNode, nil
		}
		return "", os.ErrNotExist
	}

	got := resolveWindowsShimArgv([]string{shim, "--profile", "headless", "task"}, lookPath)
	if len(got) != 5 || got[0] != pathNode || got[1] != entry {
		t.Fatalf("rewritten argv = %#v, want node %q then entry %q first", got, pathNode, entry)
	}
}

func TestResolveWindowsShimArgvKeepsArgvWhenEntryIsMissing(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(node, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shim := writeShim(t, dir, dshShimBody)

	original := []string{shim, "--profile", "headless", "task"}
	if got := resolveWindowsShimArgv(original, exec.LookPath); len(got) != 4 || got[0] != shim {
		t.Fatalf("argv = %#v, want it unchanged so the original path fails with its own error", got)
	}
}

func TestResolveWindowsShimArgvKeepsArgvWhenInterpreterIsNotNode(t *testing.T) {
	dir := t.TempDir()
	shim := writeShim(t, dir, strings.ReplaceAll(dshShimBody, `SET "_prog=node"`, `SET "_prog=bun"`))

	original := []string{shim, "--profile", "headless", "task"}
	if got := resolveWindowsShimArgv(original, exec.LookPath); len(got) != 4 || got[0] != shim {
		t.Fatalf("argv = %#v, want it unchanged for a non-Node shim", got)
	}
}

func TestResolveWindowsShimArgvKeepsArgvWhenShimIsUnparseable(t *testing.T) {
	dir := t.TempDir()
	shim := writeShim(t, dir, "@ECHO off\r\nGOTO start\r\n")

	original := []string{shim, "--profile", "headless", "task"}
	if got := resolveWindowsShimArgv(original, exec.LookPath); len(got) != 4 || got[0] != shim {
		t.Fatalf("argv = %#v, want it unchanged for an unparseable shim", got)
	}
}

func TestResolveWindowsShimArgvKeepsArgvForNativeBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the shim body below uses Windows path separators; the rewrite only fires for a .cmd binary, which the binary resolver only produces on Windows")
	}
	dir := t.TempDir()
	shim := writeShim(t, dir, "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n\"%dp0%\\payload\\tool.exe\" %*\r\n")
	payload := filepath.Join(dir, "payload", "tool.exe")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveWindowsShimArgv([]string{shim, "--profile", "headless", "task"}, exec.LookPath)
	if len(got) != 4 || got[0] != payload {
		t.Fatalf("argv = %#v, want the native payload %q started directly", got, payload)
	}
}

func TestResolveWindowsShimArgvKeepsNativeBinaryArgv(t *testing.T) {
	dir := t.TempDir()
	native := filepath.Join(dir, "dsh.exe")
	if err := os.WriteFile(native, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	original := []string{native, "--profile", "headless", "task"}
	if got := resolveWindowsShimArgv(original, exec.LookPath); len(got) != 4 || got[0] != native {
		t.Fatalf("argv = %#v, want it unchanged for a native .exe", got)
	}
}

func TestResolveWindowsShimArgvKeepsEmptyArgv(t *testing.T) {
	if got := resolveWindowsShimArgv(nil, exec.LookPath); got != nil {
		t.Fatalf("argv = %#v, want nil unchanged", got)
	}
}
