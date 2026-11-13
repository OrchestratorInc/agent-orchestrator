package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// A successful probe is cached for the service's lifetime, which is right for
// a repaired install but wrong for a credential that lapses afterwards (a
// one-hour token, a revoked API key): Start would then fail with
// ErrChatAuthRequired after the worktree exists, on every spawn, while
// PreflightChat kept serving the stale "authorized" answer. The driver's own
// auth refusal at Start therefore drops the cached probe, so the next spawn
// re-probes and the refusal lands in PreflightChat, before any durable state.
func TestAuthRequiredAtStartDropsTheCachedProbe(t *testing.T) {
	startErrors := []struct {
		name       string
		err        error
		wantProbes int
	}{
		{"auth required re-probes", ports.ErrChatAuthRequired, 2},
		{"wrapped auth required re-probes", fmt.Errorf("zcode: %w", ports.ErrChatAuthRequired), 2},
		{"other failures keep the cache", errors.New("launch app-server: exec failed"), 1},
	}
	for _, tc := range startErrors {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			probes := 0
			nextID := 0
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{
					probe: func() error {
						probes++
						return nil
					},
					start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
						return nil, tc.err
					},
				}},
				Log: slog.New(slog.DiscardHandler),
				NewID: func() string {
					nextID++
					return fmt.Sprintf("probe-auth-%d", nextID)
				},
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

			if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault); err != nil {
				t.Fatalf("PreflightChat: %v", err)
			}
			if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(),
			}); !errors.Is(err, tc.err) {
				t.Fatalf("Start err = %v, want %v", err, tc.err)
			}
			if probes != 1 {
				t.Fatalf("Probe calls before the second preflight = %d, want 1", probes)
			}
			if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault); err != nil {
				t.Fatalf("second PreflightChat: %v", err)
			}
			if probes != tc.wantProbes {
				t.Fatalf("Probe calls = %d, want %d", probes, tc.wantProbes)
			}
		})
	}
}
