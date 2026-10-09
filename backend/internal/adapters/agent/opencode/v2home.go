package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const v2DataHomeDirName = "opencode-v2-home"

// V2DataHome returns the XDG_DATA_HOME OpenCode 2 runs with: a sibling of the
// user's own data home, so OpenCode 1 and 2 never share a database.
func V2DataHome() (string, error) {
	parent := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if parent != "" && !filepath.IsAbs(parent) {
		return "", fmt.Errorf("opencode: XDG_DATA_HOME must be absolute, got %q", parent)
	}
	if filepath.Base(parent) == v2DataHomeDirName {
		return filepath.Clean(parent), nil
	}
	if parent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("opencode: resolve absolute user data home: %w", err)
		}
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("opencode: user data home must be absolute, got %q", home)
		}
		parent = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(parent, v2DataHomeDirName), nil
}

// V2NPMPrefix is the private npm prefix OpenCode 2 installs into so its
// `opencode` executable never replaces the OpenCode 1 one on PATH.
func V2NPMPrefix() (string, error) {
	home, err := V2DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "npm"), nil
}

// V2NPMBinDir is where npm places executables for V2NPMPrefix.
func V2NPMBinDir() (string, error) {
	prefix, err := V2NPMPrefix()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return prefix, nil
	}
	return filepath.Join(prefix, "bin"), nil
}

var v2DataMigrationLock = make(chan struct{}, 1)

// PrepareV2DataHome returns the isolated OpenCode 2 data home after copying a
// pre-isolation OpenCode store into it once. The legacy store is left intact
// for OpenCode 1; an existing isolated store is never overwritten.
func PrepareV2DataHome(ctx context.Context) (string, error) {
	select {
	case v2DataMigrationLock <- struct{}{}:
		defer func() { <-v2DataMigrationLock }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	destinationHome, err := V2DataHome()
	if err != nil {
		return "", err
	}
	legacyHome := filepath.Dir(destinationHome)
	if filepath.Base(legacyHome) == v2DataHomeDirName {
		return destinationHome, nil
	}
	source := filepath.Join(legacyHome, "opencode")
	destination := filepath.Join(destinationHome, "opencode")
	if _, err := os.Stat(destination); err == nil {
		return destinationHome, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("opencode: inspect isolated data store: %w", err)
	}
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return destinationHome, nil
	} else if err != nil {
		return "", fmt.Errorf("opencode: inspect legacy data store: %w", err)
	}
	if err := os.MkdirAll(destinationHome, 0o700); err != nil {
		return "", fmt.Errorf("opencode: create isolated data home: %w", err)
	}
	staging, err := os.MkdirTemp(destinationHome, ".opencode-migration-")
	if err != nil {
		return "", fmt.Errorf("opencode: stage legacy data migration: %w", err)
	}
	if err := copyTree(ctx, source, staging); err != nil {
		cleanupErr := os.RemoveAll(staging)
		return "", errors.Join(fmt.Errorf("opencode: copy legacy data store: %w", err), cleanupErr)
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
				return "", fmt.Errorf("opencode: remove redundant migration staging: %w", cleanupErr)
			}
			return destinationHome, nil
		}
		cleanupErr := os.RemoveAll(staging)
		return "", errors.Join(fmt.Errorf("opencode: activate migrated data store: %w", err), cleanupErr)
	}
	return destinationHome, nil
}

func copyTree(ctx context.Context, source, destination string) error {
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return errors.Join(err, sourceRoot.Close())
	}
	walkErr := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if relative == "." {
				return nil
			}
			return destinationRoot.Mkdir(relative, info.Mode().Perm())
		}
		if relative == "opencode.db" || relative == "opencode.db-wal" || relative == "opencode.db-shm" || relative == "opencode.db-journal" {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported legacy data entry %q", path)
		}
		input, err := sourceRoot.Open(relative)
		if err != nil {
			return err
		}
		output, err := destinationRoot.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		copyErr := copyFileContext(ctx, output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
	closeErr := errors.Join(sourceRoot.Close(), destinationRoot.Close())
	if walkErr != nil || closeErr != nil {
		return errors.Join(walkErr, closeErr)
	}
	databasePath := filepath.Join(source, "opencode.db")
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return snapshotSQLiteDatabase(ctx, databasePath, filepath.Join(destination, "opencode.db"))
}

func copyFileContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func snapshotSQLiteDatabase(ctx context.Context, source, destination string) error {
	sourceURL := url.URL{Path: source}
	db, err := sql.Open("sqlite", "file:"+sourceURL.EscapedPath()+"?mode=ro&_pragma=busy_timeout(0)")
	if err != nil {
		return err
	}
	for {
		_, snapshotErr := db.ExecContext(ctx, `VACUUM INTO ?`, destination)
		if snapshotErr == nil {
			return db.Close()
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, db.Close())
		}
		var sqliteErr *moderncsqlite.Error
		if !errors.As(snapshotErr, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY {
			return errors.Join(snapshotErr, db.Close())
		}
		if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, db.Close())
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), db.Close())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
