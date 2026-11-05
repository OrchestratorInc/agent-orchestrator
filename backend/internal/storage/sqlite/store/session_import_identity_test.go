package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMetadataOnlyImportRequiresNativeTerminalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode domain.SessionMode
		id   string
		want bool
	}{
		{name: "native terminal", mode: domain.SessionModeTUI, id: "native", want: true},
		{name: "chat requires readable history", mode: domain.SessionModeChat, id: "native"},
		{name: "terminal missing identity", mode: domain.SessionModeTUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			rec := domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessGemini, Mode: tc.mode, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			rec.Metadata = domain.SessionMetadata{AgentSessionID: tc.id, ProviderConversationID: tc.id, ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: "/provider"}}
			_, fresh, err := s.CreateImportedSession(context.Background(), rec, nil)
			if tc.want != (err == nil && fresh) {
				t.Fatalf("metadata-only import: fresh=%v err=%v", fresh, err)
			}
		})
	}
}
