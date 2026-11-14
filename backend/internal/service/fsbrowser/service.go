package fsbrowser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const maxEntries = 500

type Entry struct {
	Name    string
	Path    string
	GitRepo bool
}

type Listing struct {
	Path      string
	Parent    string
	Entries   []Entry
	Truncated bool
}

type Service struct{}

func New() *Service { return &Service{} }

func (*Service) List(ctx context.Context, path string) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	if path == "" {
		var err error
		path, err = os.UserHomeDir()
		if err != nil {
			return Listing{}, err
		}
	}
	if !filepath.IsAbs(path) {
		return Listing{}, apierr.Invalid("FS_PATH_NOT_ABSOLUTE", "path must be absolute on the daemon host", nil)
	}
	path = filepath.Clean(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Listing{}, apierr.NotFound("FS_NOT_FOUND", "no such directory on the daemon host")
		case errors.Is(err, os.ErrPermission):
			return Listing{}, apierr.Forbidden("FS_FORBIDDEN", "the daemon may not read that directory")
		default:
			return Listing{}, apierr.Invalid("FS_NOT_A_DIRECTORY", "not a directory", nil)
		}
	}

	out := Listing{Path: path, Parent: filepath.Dir(path), Entries: []Entry{}}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if !entry.IsDir() {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, statErr := os.Stat(child)
			if statErr != nil || !target.IsDir() {
				continue
			}
		}
		if len(out.Entries) == maxEntries {
			out.Truncated = true
			break
		}
		_, gitErr := os.Stat(filepath.Join(child, ".git"))
		out.Entries = append(out.Entries, Entry{Name: entry.Name(), Path: child, GitRepo: gitErr == nil})
	}
	return out, nil
}
