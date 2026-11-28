package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Search is optional: with an index that cannot be opened the daemon still
// gets a working store, and search answers "unavailable" instead of failing.
func TestSessionSearchIsOffWhenItsIndexCannotOpen(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "search.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("daemon store over an unopenable search.db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := startSessionSearch(ctx, store, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	coverage, err := service.Coverage(ctx)
	if err != nil || coverage.State != "unavailable" {
		t.Fatalf("coverage = %+v, %v; want state unavailable", coverage, err)
	}
	_, err = service.Search(ctx, domain.ParseSessionSearchQuery("flaky"), domain.SessionSearchParams{})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_SEARCH_UNAVAILABLE" {
		t.Fatalf("search error = %v; want SESSION_SEARCH_UNAVAILABLE", err)
	}
}
