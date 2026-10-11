package sessionmanager

import (
	"context"
	"errors"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"testing"
)

func TestPoolRejectsTerminalBeforeAllocation(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, rt := newChatManager(launcher)
	cfg := asyncChatSpawnConfig("do work")
	cfg.Harness = domain.HarnessPool
	cfg.RequestedMode = domain.SessionModeTUI
	if _, _, _, err := m.Spawn(context.Background(), cfg); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("terminal request: %v", err)
	}
	if len(st.sessions) != 0 || rt.created != 0 || len(launcher.started) != 0 {
		t.Fatal("unsupported terminal allocated state")
	}
}
func TestPoolDefaultChatNeverFallsBack(t *testing.T) {
	for _, preflight := range []error{nil, ports.ErrChatDriverUnavailable, ports.ErrChatDriverIncompatible, ports.ErrChatAuthRequired} {
		launcher := &recordingLauncher{preflightErr: preflight}
		m, st, rt := newChatManager(launcher)
		deferredBackground(m)
		cfg := asyncChatSpawnConfig("do work")
		cfg.Harness = domain.HarnessPool
		cfg.RequestedMode = ""
		rec, _, _, err := m.Spawn(context.Background(), cfg)
		if preflight != nil {
			if !errors.Is(err, preflight) || len(st.sessions) != 0 {
				t.Fatalf("preflight: %v", err)
			}
		} else if err != nil || rec.Mode != domain.SessionModeChat {
			t.Fatalf("default: %s, %v", rec.Mode, err)
		}
		if rt.created != 0 {
			t.Fatal("Pool fell back to terminal")
		}
	}
}
