package pax

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakePaxEnv makes a re-executed test binary answer as the pax CLI.
const fakePaxEnv = "AO_FAKE_PAX_VERSION"

// TestMain lets the test binary stand in for Pax: fakePax puts a copy on PATH
// under Pax's executable name, so binary discovery and the identity check run
// as they do in production, on every platform.
func TestMain(m *testing.M) {
	if version, ok := os.LookupEnv(fakePaxEnv); ok {
		if len(os.Args) > 1 && os.Args[1] == "--version" && version != "" {
			fmt.Println(version)
			os.Exit(0)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// fakePax installs a fake `pydantic-ai-pax` that prints version, with HOME
// and XDG_CONFIG_HOME pointed at empty folders. It returns the binary path.
func fakePax(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	name := binarySpec.Names[0]
	if runtime.GOOS == "windows" {
		name = binarySpec.WinNames[0]
	}
	path := filepath.Join(dir, name)
	copyTestBinary(t, path)
	t.Setenv(fakePaxEnv, version)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return path
}

func copyTestBinary(t *testing.T, dst string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe) //nolint:gosec // the running test binary
	if err != nil {
		t.Fatal(err)
	}
	// A copy, not a link: Windows cannot delete a link to the running test
	// binary when the temp dir is cleaned up.
	if err := os.WriteFile(dst, data, 0o700); err != nil { //nolint:gosec // test-only executable
		t.Fatal(err)
	}
}
