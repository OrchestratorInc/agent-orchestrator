package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderExists_HelperSemantics(t *testing.T) {
	t.Parallel()

	t.Run("existing directory returns true", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		exists, err := folderExists(dir)
		if err != nil {
			t.Fatalf("folderExists: %v", err)
		}
		if !exists {
			t.Fatal("folderExists = false for existing directory, want true")
		}
	})

	t.Run("removed directory returns false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		exists, err := folderExists(dir)
		if err != nil {
			t.Fatalf("folderExists: %v", err)
		}
		if exists {
			t.Fatal("folderExists = true for removed directory, want false")
		}
	})

	t.Run("regular file at path returns false", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		exists, err := folderExists(path)
		if err != nil {
			t.Fatalf("folderExists: %v", err)
		}
		if exists {
			t.Fatal("folderExists = true for regular file, want false")
		}
	})
}
