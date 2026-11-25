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

// The project's role agent is the one that launches when the spawn names none,
// so it is the one the starting choices are checked against.
func TestSpawnChecksStartingChoicesAgainstTheProjectRoleAgent(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}}}
	cmd := &fakeCommander{}
	svc := &Service{store: st, manager: cmd}
	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, AutoCompactWindow: "1m"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AUTO_COMPACT_WINDOW_UNSUPPORTED" || cmd.spawnCalls != 0 {
		t.Fatalf("spawnCalls=%d, err=%v, want an AUTO_COMPACT_WINDOW_UNSUPPORTED refusal", cmd.spawnCalls, err)
	}
}

// A terminal session ignores Chat settings, so asking for one is not refused over them.
func TestSpawnIgnoresChatStartingChoicesForATerminal(t *testing.T) {
	cmd := &fakeCommander{}
	svc := &Service{store: newFakeStore(), manager: cmd}
	cfg := ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI, AutoCompactWindow: "1m"}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil || cmd.spawnCalls != 1 {
		t.Fatalf("spawnCalls=%d, err=%v, want the terminal spawn to go ahead", cmd.spawnCalls, err)
	}
}

func TestDelegateTaskRefusesAStartingChoiceTheAgentCannotTake(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  domain.AgentHarness
		input DelegateTaskInput
		code  string
	}{
		{"requested agent", "", DelegateTaskInput{RequestedAgent: domain.HarnessCodex, AutoCompactWindow: "1m"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{"project role agent", domain.HarnessCodex, DelegateTaskInput{AutoCompactWindow: "1m"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{"fast mode", domain.HarnessClaudeCode, DelegateTaskInput{FastMode: true}, "FAST_MODE_UNSUPPORTED"},
		{"valid choice", domain.HarnessClaudeCode, DelegateTaskInput{AutoCompactWindow: "1m"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["ao"] = domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: tc.role}}}
			cmd := &fakeCommander{}
			in := tc.input
			in.ProjectID, in.Brief = "ao", "Fix login redirect"
			_, err := (&Service{store: st, manager: cmd}).DelegateTask(context.Background(), in)
			if tc.code == "" {
				if err != nil || cmd.spawnCalls != 1 {
					t.Fatalf("spawnCalls=%d, err=%v, want the spawn to go ahead", cmd.spawnCalls, err)
				}
				return
			}
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || cmd.spawnCalls != 0 {
				t.Fatalf("spawnCalls=%d, err=%v, want a %s refusal", cmd.spawnCalls, err, tc.code)
			}
		})
	}
}
