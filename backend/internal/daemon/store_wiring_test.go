package daemon

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// newerSchemaDataDir returns a data dir whose ao.db records a migration far
// beyond any this build embeds, as if a newer AO had migrated it.
func newerSchemaDataDir(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatalf("create migrated database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatalf("open database to mark it newer: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999, 1)`); err != nil {
		t.Fatalf("record newer migration: %v", err)
	}
	return dataDir
}

func TestOpenStoreRefusesDatabaseMigratedByNewerAO(t *testing.T) {
	cfg := config.Config{DataDir: newerSchemaDataDir(t)}

	store, err := openStore(cfg, slog.New(slog.DiscardHandler))
	if err == nil {
		_ = store.Close()
		t.Fatal("openStore succeeded on a database a newer AO migrated; want a refusal")
	}
	if !errors.Is(err, sqlite.ErrSchemaNewer) {
		t.Fatalf("openStore error = %v, want sqlite.ErrSchemaNewer", err)
	}
	// Run returns this error unchanged and the process prints it to stderr,
	// which the desktop shows under "Show details".
	if !strings.HasPrefix(err.Error(), "open store: ") || !strings.Contains(err.Error(), "migration 99999 applied") {
		t.Fatalf("openStore error = %q, want an %q-prefixed message naming the newer migration", err, "open store: ")
	}
}

func TestOpenStoreAllowNewerSchemaOpensAndWarns(t *testing.T) {
	var logs bytes.Buffer
	cfg := config.Config{DataDir: newerSchemaDataDir(t), AllowNewerSchema: true}

	store, err := openStore(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("openStore with AO_ALLOW_NEWER_SCHEMA: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "AO_ALLOW_NEWER_SCHEMA") {
		t.Fatalf("override was not announced with a warning; log:\n%s", logs.String())
	}
}

func TestOpenStoreOpensCurrentDatabaseWithoutWarning(t *testing.T) {
	var logs bytes.Buffer
	cfg := config.Config{DataDir: t.TempDir()}

	store, err := openStore(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("openStore on a fresh data dir: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if logs.Len() != 0 {
		t.Fatalf("openStore logged without the override set:\n%s", logs.String())
	}
}
