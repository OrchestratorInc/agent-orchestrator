package sessionmanager

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestMCPServerCommandUsesAoMcpNextToAo(t *testing.T) {
	dir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	ao := filepath.Join(dir, "ao"+ext)
	helper := filepath.Join(dir, "ao-mcp"+ext)
	check := func(executable, wantCommand string, wantArgs []string) {
		t.Helper()
		command, args := mcpServerCommand(executable)
		if command != wantCommand || !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("mcpServerCommand(%s) = %s %v, want %s %v", executable, command, args, wantCommand, wantArgs)
		}
	}
	check(ao, ao, []string{"mcp"})
	if err := os.WriteFile(helper, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	check(ao, helper, nil)
	check(filepath.Join(dir, "ao-dev"+ext), filepath.Join(dir, "ao-dev"+ext), []string{"mcp"})
	if runtime.GOOS != "windows" {
		if err := os.Chmod(helper, 0o644); err != nil {
			t.Fatal(err)
		}
		check(ao, ao, []string{"mcp"})
	}
}
