package daemon

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestOpenStoreRefusesDatabaseMigratedByNewerAO(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir()}
	sqlitetest.MustCreateNewerSchemaAt(t, cfg.DataDir)

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
	cfg := config.Config{DataDir: t.TempDir(), AllowNewerSchema: true}
	sqlitetest.MustCreateNewerSchemaAt(t, cfg.DataDir)

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
