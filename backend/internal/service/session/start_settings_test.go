package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawnRefusesAStartingChoiceTheAgentCannotTake(t *testing.T) {
	for _, tc := range []struct {
		cfg  ports.SpawnConfig
		code string
	}{
		{ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessCodex, AutoCompactWindow: "1m"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, AutoCompactWindow: "off"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessOpenCode, FastMode: true}, "FAST_MODE_UNSUPPORTED"},
	} {
		cmd := &fakeCommander{}
		svc := &Service{store: newFakeStore(), manager: cmd}
		_, _, _, err := svc.Spawn(context.Background(), tc.cfg)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != tc.code || cmd.spawnCalls != 0 {
			t.Fatalf("%+v: spawnCalls=%d, err=%v, want a %s refusal", tc.cfg, cmd.spawnCalls, err, tc.code)
		}
	}
}
