package cursorstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const preparationFile = ".ao-import.json"

type preparation struct {
	Source      string `json:"source"`
	ID          string `json:"id"`
	CWD         string `json:"cwd"`
	Fingerprint string `json:"fingerprint"`
}

func preparationFor(root, source, cwd, id, expected string) (preparation, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return preparation{}, err
	}
	rel, err := filepath.Rel(root, source)
	if err != nil || !filepath.IsLocal(rel) {
		return preparation{}, errors.New("cursor preparation source is outside provider home")
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return preparation{}, err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return preparation{}, err
	}
	return preparation{Source: source, ID: id, CWD: cwd, Fingerprint: expected}, nil
}

// PreparedCopyIn reuses only a complete copy published for this exact
// selection at layout's resume path. Its native state may have advanced since
// publication and must not be replaced.
func PreparedCopyIn(ctx context.Context, layout Layout, root, source, cwd, id, expected string) (string, bool, error) {
	if expected == "" {
		return "", false, nil
	}
	root, err := providerRoot(root)
	if err != nil {
		return "", false, err
	}
	want, err := preparationFor(root, source, cwd, id, expected)
	if err != nil {
		return "", false, err
	}
	target, err := layout.path(root, cwd, id, expected)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(filepath.Dir(target)); errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, filepath.Join(filepath.Dir(target), preparationFile))
	if err != nil {
		return "", false, err
	}
	info, err := provider.Lstat(rel)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return "", false, errors.New("cursor preparation record is invalid")
	}
	file, err := provider.Open(rel)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = file.Close() }()
	var got preparation
	if err := json.NewDecoder(io.LimitReader(file, 8192)).Decode(&got); err != nil {
		return "", false, err
	}
	if got != want {
		return "", false, errors.New("cursor target belongs to a different import selection")
	}
	if err := Verify(ctx, root, target, layout.SessionID(id, cwd, expected)); err != nil {
		return "", false, err
	}
	return target, true, nil
}
