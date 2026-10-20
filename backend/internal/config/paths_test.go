package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDoesNotLoadOrCreateState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AO_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("AO_RUN_FILE", filepath.Join(root, "run", "running.json"))
	t.Setenv("AO_PORT", "not-a-port")
	t.Setenv("AO_REQUEST_TIMEOUT", "not-a-duration")
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != filepath.Join(root, "data") || paths.RunFilePath != filepath.Join(root, "run", "running.json") || paths.DataDirSource != "AO_DATA_DIR" || paths.RunFileSource != "AO_RUN_FILE" {
		t.Fatalf("paths = %+v", paths)
	}
	if _, err := os.Stat(paths.DataDir); !os.IsNotExist(err) {
		t.Fatalf("resolver created data directory: %v", err)
	}
}

func TestResolvePathsUsesExistingDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("AO_DATA_DIR", "")
	t.Setenv("AO_RUN_FILE", "")
	wantData, err := resolveDataDir()
	if err != nil {
		t.Fatal(err)
	}
	wantRun, err := resolveRunFilePath()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != wantData || paths.RunFilePath != wantRun || paths.DataDirSource != "default" || paths.RunFileSource != "default" {
		t.Fatalf("paths = %+v", paths)
	}
}
