package goose

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGooseContinuationUsesExactGlobalSessionID(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "sessions", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, query := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, archived_at TEXT)`,
		`INSERT INTO sessions VALUES ('20261102_1', NULL), ('archived', '2026-11-02')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	for id, want := range map[string]ports.NativeSessionAvailability{
		"20261102_1": ports.NativeSessionAvailabilityAvailable,
		"missing":    ports.NativeSessionAvailabilityUnavailable,
		"archived":   ports.NativeSessionAvailabilityUnavailable,
	} {
		got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
		if err != nil || got != want {
			t.Fatalf("probe %s = %s, %v; want %s", id, got, err, want)
		}
	}
}

func TestGooseConfigDirOverrides(t *testing.T) {
	ctx := context.Background()
	p := New()
	root, xdg := t.TempDir(), t.TempDir()
	env := map[string]string{"GOOSE_PATH_ROOT": root, "XDG_DATA_HOME": xdg}
	if got, err := p.NativeSessionConfigDir(ctx, env); err != nil || got != filepath.Join(root, "data") {
		t.Fatalf("path root = %s, %v", got, err)
	}
	env["GOOSE_PATH_ROOT"] = ""
	if got, err := p.NativeSessionConfigDir(ctx, env); err != nil || got != filepath.Join(xdg, "goose") {
		t.Fatalf("xdg root = %s, %v", got, err)
	}
}
