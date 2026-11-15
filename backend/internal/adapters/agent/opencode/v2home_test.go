package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareV2DataHomeSnapshotsLiveSQLiteDatabase(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(legacy, "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('two')`); err != nil {
		t.Fatal(err)
	}
	padding := make([]byte, 8<<20)
	if err := os.WriteFile(filepath.Join(legacy, "opencode.db-padding"), padding, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := PrepareV2DataHome(context.Background())
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		staging, _ := filepath.Glob(filepath.Join(parent, v2DataHomeDirName, ".opencode-migration-*", "opencode.db-padding"))
		if len(staging) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not reach the post-database padding file")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	migrated, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(parent, v2DataHomeDirName, "opencode", "opencode.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migrated sessions = %d, want a consistent two-session snapshot", count)
	}
}

func TestPrepareV2DataHomeDoesNotCopyOrphanedRollbackJournal(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "opencode.db-journal"), []byte("orphaned journal"), 0o600); err != nil {
		t.Fatal(err)
	}

	home, err := PrepareV2DataHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "opencode.db-journal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrated store retained rollback journal: %v", err)
	}
}

func TestPrepareV2DataHomeSkipsToolCache(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	linkDir := filepath.Join(legacy, "bin", "node_modules", ".bin")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../astro/bin/ls.js", filepath.Join(linkDir, "astro-ls")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(legacy, "storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "storage", "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	home, err := PrepareV2DataHome(context.Background())
	if err != nil {
		t.Fatalf("PrepareV2DataHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "storage", "session.json")); err != nil {
		t.Fatalf("user data was not migrated: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "opencode", "bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tool cache was migrated (err %v), want it left behind", err)
	}
}

func TestPrepareV2DataHomeStopsWaitingForMigrationLockOnCancel(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(legacy, "opencode.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`ROLLBACK`) }()

	firstDone := make(chan error, 1)
	go func() {
		_, err := PrepareV2DataHome(context.Background())
		firstDone <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		staging, _ := filepath.Glob(filepath.Join(parent, v2DataHomeDirName, ".opencode-migration-*"))
		if len(staging) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first migration did not acquire the migration lock")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := PrepareV2DataHome(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second migration error = %v, want deadline while waiting for lock", err)
	}
	if _, err := db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first migration did not finish after releasing SQLite lock")
	}
}

type removeAtCheckContext struct {
	context.Context
	at     int
	calls  int
	remove func()
}

func (c *removeAtCheckContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.remove()
	}
	return c.Context.Err()
}

func TestCopyTreeToleratesEntriesVanishingAtAnyPoint(t *testing.T) {
	const project = "9b29c8a3ea477dcfbde51766b401cd337ce370e8"
	tests := []struct {
		name         string
		remove       func(source string)
		wantVanished string
	}{
		{
			name: "file",
			remove: func(source string) {
				_ = os.Remove(filepath.Join(source, "shell", project, "sh_1.out"))
			},
			wantVanished: filepath.Join("shell", project, "sh_1.out"),
		},
		{
			name: "directory",
			remove: func(source string) {
				_ = os.RemoveAll(filepath.Join(source, "shell", project))
			},
			wantVanished: filepath.Join("shell", project),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for at := 1; ; at++ {
				source, destination := t.TempDir(), t.TempDir()
				for _, entry := range []string{
					filepath.Join("shell", project, "sh_0.out"),
					filepath.Join("shell", project, "sh_1.out"),
					filepath.Join("storage", "session.json"),
				} {
					if err := os.MkdirAll(filepath.Join(source, filepath.Dir(entry)), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, entry), []byte("data"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ctx := &removeAtCheckContext{
					Context: context.Background(),
					at:      at,
					remove:  func() { tt.remove(source) },
				}
				if err := copyTree(ctx, source, destination); err != nil {
					t.Fatalf("deleting at context check %d: copyTree: %v", at, err)
				}
				if _, err := os.Stat(filepath.Join(destination, "storage", "session.json")); err != nil {
					t.Fatalf("deleting at context check %d: lost unrelated data: %v", at, err)
				}
				if at > ctx.calls {
					if _, err := os.Stat(filepath.Join(destination, tt.wantVanished)); err != nil {
						t.Fatalf("copy without a deletion lost %s: %v", tt.wantVanished, err)
					}
					return
				}
			}
		})
	}
}

func TestPrepareV2DataHomeFinishesWhenLegacyEntryVanishes(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	shell := filepath.Join(legacy, "shell", "project")
	if err := os.MkdirAll(shell, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sh_0.out", "sh_1.out"} {
		if err := os.WriteFile(filepath.Join(shell, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &removeAtCheckContext{
		Context: context.Background(),
		at:      7,
		remove:  func() { _ = os.Remove(filepath.Join(shell, "sh_1.out")) },
	}
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	home, err := PrepareV2DataHome(ctx)
	if err != nil {
		t.Fatalf("PrepareV2DataHome: %v", err)
	}
	if !strings.Contains(logs.String(), "sh_1.out") {
		t.Fatalf("the deletion at check %d did not hit the vanished-entry skip; logs: %s", ctx.at, logs.String())
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "shell", "project", "sh_0.out")); err != nil {
		t.Fatalf("surviving entry was not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "shell", "project", "sh_1.out")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vanished entry state: %v", err)
	}
}

func TestPrepareV2DataHomeFailsWhenWholeLegacyStoreVanishes(t *testing.T) {
	for at := 1; ; at++ {
		parent := t.TempDir()
		t.Setenv("XDG_DATA_HOME", parent)
		legacy := filepath.Join(parent, "opencode")
		for _, entry := range []string{
			filepath.Join("shell", "project", "sh_0.out"),
			filepath.Join("storage", "session.json"),
		} {
			if err := os.MkdirAll(filepath.Join(legacy, filepath.Dir(entry)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(legacy, entry), []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ctx := &removeAtCheckContext{
			Context: context.Background(),
			at:      at + 1,
			remove:  func() { _ = os.RemoveAll(legacy) },
		}
		_, err := PrepareV2DataHome(ctx)
		if ctx.calls < ctx.at {
			if err != nil {
				t.Fatalf("copy without a deletion: %v", err)
			}
			return
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleting at context check %d: error = %v, want the vanished store to fail", ctx.at, err)
		}
		if _, statErr := os.Stat(filepath.Join(parent, v2DataHomeDirName, "opencode")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("deleting at context check %d: activated a copy of a vanished store: %v", ctx.at, statErr)
		}
		staging, _ := filepath.Glob(filepath.Join(parent, v2DataHomeDirName, ".opencode-migration-*"))
		if len(staging) != 0 {
			t.Fatalf("deleting at context check %d: left staging behind: %v", ctx.at, staging)
		}
	}
}

func TestCopyTreeSummarizesVanishedEntriesInOneWarning(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	const files = 8
	name := func(index int) string { return fmt.Sprintf("sh_%d.out", index) }
	for index := range files {
		if err := os.WriteFile(filepath.Join(source, name(index)), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	ctx := &removeAtCheckContext{
		Context: context.Background(),
		at:      4,
		remove: func() {
			for index := 1; index < files; index++ {
				_ = os.Remove(filepath.Join(source, name(index)))
			}
		},
	}
	if err := copyTree(ctx, source, destination); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	output := logs.String()
	if lines := strings.Count(output, "vanished during migration"); lines != 1 {
		t.Fatalf("logged %d vanished-entry warnings, want one summary: %s", lines, output)
	}
	if !strings.Contains(output, "count=7") {
		t.Fatalf("summary lacks the full count: %s", output)
	}
	if !strings.Contains(output, name(1)) || strings.Contains(output, name(6)) {
		t.Fatalf("summary should name the first %d entries only: %s", maxVanishedLegacyExamples, output)
	}
}

func TestCopyTreeStillFailsOnOtherErrors(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.Symlink("missing-target", filepath.Join(source, "dangling")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyTree(context.Background(), source, destination); err == nil {
		t.Fatal("copyTree accepted a dangling symlink, want the unsupported-entry error")
	}
}

func TestCopyTreeStopsOnCancel(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyTree(ctx, source, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyTree error = %v, want context canceled", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled copy created destination file: %v", err)
	}
}

func TestSnapshotSQLiteDatabasePreservesDeadline(t *testing.T) {
	source := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(source))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`ROLLBACK`) }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = snapshotSQLiteDatabase(ctx, source, filepath.Join(t.TempDir(), "snapshot.db"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshot error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("snapshot ignored deadline for %s", elapsed)
	}
}
