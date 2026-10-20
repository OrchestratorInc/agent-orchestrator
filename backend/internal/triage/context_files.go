package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func localContextAllowed(origin Origin) bool {
	if origin.HostID != "" || len(origin.Session) > collectMaxBytes {
		return false
	}
	if len(origin.Session) == 0 {
		return true
	}
	var session struct {
		ID     string `json:"id"`
		HostID string `json:"hostId"`
		Cloud  any    `json:"cloud"`
	}
	if json.Unmarshal(origin.Session, &session) != nil {
		return false
	}
	return (session.ID == "" || session.ID == origin.SessionID) && session.HostID == "" && (session.Cloud == nil || session.Cloud == false)
}

// openRetainedContextFile accepts only known AO-relative paths. It pins the
// AO root and rejects aliases in every component before returning an open file.
func openRetainedContextFile(ctx context.Context, dataDir, relative string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil || !filepath.IsAbs(dataDir) || relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return nil, nil, errors.New("retained diagnostic file is unavailable")
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, nil, errors.New("retained diagnostic root is unavailable")
	}
	defer func() { _ = root.Close() }()
	path := "."
	var before os.FileInfo
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, nil, errors.New("retained diagnostic file is unavailable")
		}
		path = filepath.Join(path, part)
		before, err = root.Lstat(path)
		if err != nil || before.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("retained diagnostic file is unavailable or is an alias")
		}
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errors.New("retained diagnostic file is unavailable")
	}
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, nil, errors.New("retained diagnostic file could not be opened")
	}
	opened, err := file.Stat()
	after, afterErr := root.Lstat(filepath.FromSlash(relative))
	if err != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = file.Close()
		return nil, nil, errors.New("retained diagnostic file changed while opening")
	}
	return file, opened, nil
}

func readRetainedTail(ctx context.Context, dataDir, relative string, budget int) ([]byte, os.FileInfo, bool, bool, error) {
	file, info, err := openRetainedContextFile(ctx, dataDir, relative)
	if err != nil {
		return nil, nil, false, false, err
	}
	defer func() { _ = file.Close() }()
	start := max(int64(0), info.Size()-int64(budget))
	data, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, false, false, errors.New("retained tail is unreadable")
	}
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = nil
		}
	}
	partial := len(data) > 0 && data[len(data)-1] != '\n'
	if partial {
		if newline := bytes.LastIndexByte(data, '\n'); newline >= 0 {
			data = data[:newline+1]
		} else {
			data = nil
		}
	}
	return data, info, start > 0, partial, nil
}
