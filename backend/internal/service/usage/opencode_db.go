package usage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // register sqlite driver
)

var openCodeChannelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._]*$`)

func openCodeFamilyDBName(name, app string) bool {
	if name == app+".db" {
		return true
	}
	channel, ok := strings.CutPrefix(name, app+"-")
	if !ok {
		return false
	}
	channel, ok = strings.CutSuffix(channel, ".db")
	return ok && openCodeChannelPattern.MatchString(channel)
}

func discoverOpenCodeFamilyDB(ctx context.Context, root, app, nativeID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(root) == "" || strings.TrimSpace(nativeID) == "" {
		return "", nil
	}
	paths, err := filepath.Glob(filepath.Join(root, app+"*.db"))
	if err != nil {
		return "", err
	}
	sort.Slice(paths, func(i, j int) bool {
		iDefault := filepath.Base(paths[i]) == app+".db"
		jDefault := filepath.Base(paths[j]) == app+".db"
		if iDefault != jDefault {
			return iDefault
		}
		return paths[i] < paths[j]
	})
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !openCodeFamilyDBName(filepath.Base(path), app) {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if openCodeDBListsSession(ctx, path, nativeID) {
			return path, nil
		}
	}
	return "", nil
}

func openCodeDBListsSession(ctx context.Context, path, nativeID string) bool {
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath() +
		"?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var found int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM session WHERE id = ? LIMIT 1`, nativeID).Scan(&found)
	return err == nil
}
