package skillassets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"embed"
)

//go:embed using-ao bug-triage
var files embed.FS

const SkillName = "using-ao"

func Dir(dataDir string) string {
	return filepath.Join(dataDir, "skills", SkillName)
}

func Install(dataDir string) error {
	if err := Materialize(Dir(dataDir)); err != nil {
		return err
	}
	return MaterializeBugTriage(BugTriageDir(dataDir))
}

func Materialize(destDir string) error {
	return materialize(SkillName, destDir)
}

func materialize(name, destDir string) error {
	if strings.TrimSpace(destDir) == "" {
		return fmt.Errorf("skillassets.Materialize: destDir is required")
	}
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("clear skill dir %q: %w", destDir, err)
	}
	return fs.WalkDir(files, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, name)
		rel = strings.TrimPrefix(rel, "/")
		target := destDir
		if rel != "" {
			target = filepath.Join(destDir, filepath.FromSlash(rel))
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %q: %w", p, err)
		}
		if err := os.WriteFile(target, b, 0o600); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		return nil
	})
}
