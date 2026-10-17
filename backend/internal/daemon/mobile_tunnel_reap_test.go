package daemon

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func TestMobileTunnelReapBlocksReplacementUntilCleanupSucceeds(t *testing.T) {
	calls := 0
	guard := mobileTunnelReapGuard(func() error {
		calls++
		if calls <= 2 {
			return errors.New("owner is unknown")
		}
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for range 2 {
		if guard() {
			t.Fatal("unresolved owner allowed a replacement connector")
		}
	}
	if !guard() {
		t.Fatal("successful cleanup still blocked connector resolution")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if !guard() {
				t.Error("completed cleanup was lost")
			}
		})
	}
	wg.Wait()
	if calls != 3 {
		t.Fatalf("completed cleanup reaped a replacement: calls = %d, want 3", calls)
	}
}
