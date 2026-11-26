package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestStartSavesANewConversationsFastModeAndAutoCompaction(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	newService := func(started *ports.ChatStartConfig, resumed *ports.ChatResumeConfig) *chatsvc.Service {
		conv := newFakeConversation()
		conv.providerConversationID = "claude-thread"
		return chatsvc.New(chatsvc.Options{
			Store: st, Reader: fullSnapshotReader(st), Sessions: st,
			Drivers: fakeRegistry{driver: fakeDriver{conv: conv, startCfg: started, resumeCfg: resumed}},
			Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
		})
	}

	var started ports.ChatStartConfig
	svc := newService(&started, nil)
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), FastMode: true, AutoCompactWindow: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fast mode is a setting of the running agent; the window is saved.
	if !controller.Settings().FastMode {
		t.Fatalf("live settings = %+v, want Fast mode on", controller.Settings())
	}
	if got := started.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("compaction window = %q, want the picked 1m", got)
	}
	stored, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.AutoCompactWindow != "1m" {
		t.Fatalf("saved settings = %+v, want a 1m window", stored.Settings)
	}
	if err := svc.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}

	// A resume carries no starting choice and keeps the saved one.
	var resumed ports.ChatResumeConfig
	svc = newService(nil, &resumed)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: "claude-thread",
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, id) })
	if got := resumed.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("resumed compaction window = %q, want the saved 1m", got)
	}
	stored, err = st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.AutoCompactWindow != "1m" {
		t.Fatalf("settings after resume = %+v, want the window kept", stored.Settings)
	}
}

func TestSetTurnSettingsRefusesAChoiceTheAgentCannotTake(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		harness  domain.AgentHarness
		settings domain.ConversationSettings
		code     string
	}{
		{domain.HarnessCodex, domain.ConversationSettings{AutoCompactWindow: "1m"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{domain.HarnessCodex, domain.ConversationSettings{AutoCompactWindow: "off"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{domain.HarnessClaudeCode, domain.ConversationSettings{AutoCompactWindow: "off"}, "AUTO_COMPACT_WINDOW_UNSUPPORTED"},
		{domain.HarnessClaudeCode, domain.ConversationSettings{FastMode: true}, "FAST_MODE_UNSUPPORTED"},
		{domain.HarnessClaudeCode, domain.ConversationSettings{AutoCompactWindow: "500k"}, ""},
		{domain.HarnessCodex, domain.ConversationSettings{FastMode: true}, ""},
	} {
		st := openStore(t)
		svc := chatsvc.New(chatsvc.Options{
			Store: st, Sessions: st,
			Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
			Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
		})
		if _, err := svc.Start(ctx, chatsvc.StartConfig{
			SessionID: testSession, ProjectID: testProject, Harness: tc.harness, WorkspacePath: t.TempDir(),
		}); err != nil {
			t.Fatal(err)
		}
		got, err := svc.SetTurnSettings(ctx, testSession, tc.settings)
		var apiErr *apierr.Error
		switch {
		case tc.code == "" && err != nil:
			t.Errorf("%s %+v: %v, want it saved", tc.harness, tc.settings, err)
		case tc.code == "" && (got.AutoCompactWindow != tc.settings.AutoCompactWindow || got.FastMode != tc.settings.FastMode):
			t.Errorf("%s %+v: saved %+v", tc.harness, tc.settings, got)
		case tc.code != "" && (!errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != tc.code):
			t.Errorf("%s %+v: %v, want a %s refusal", tc.harness, tc.settings, err, tc.code)
		}
		_ = svc.Stop(ctx, testSession)
	}
}

// Changing auto-compaction restarts the agent: the chat stops, and the session
// manager starts it again with an environment built afresh from the project.
// Auto after Off must then start OpenCode with the project's own config, and a
// restart that keeps Off must turn compaction off again.
func TestOpenCodeRestartAfterOffFollowsTheSavedChoice(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessOpenCode,
		Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	const projectConfig = `{"model":"a/b"}`
	newService := func(started *ports.ChatStartConfig, resumed *ports.ChatResumeConfig) *chatsvc.Service {
		conv := newFakeConversation()
		conv.providerConversationID = "opencode-thread"
		return chatsvc.New(chatsvc.Options{
			Store: st, Reader: fullSnapshotReader(st), Sessions: st,
			Drivers: fakeRegistry{driver: fakeDriver{conv: conv, startCfg: started, resumeCfg: resumed}},
			Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
		})
	}
	// restart stops svc and starts the chat again in a new service, as a resume
	// does, returning it.
	restart := func(svc *chatsvc.Service, want string) *chatsvc.Service {
		t.Helper()
		if err := svc.Stop(ctx, id); err != nil {
			t.Fatal(err)
		}
		var resumed ports.ChatResumeConfig
		next := newService(nil, &resumed)
		if _, err := next.Start(ctx, chatsvc.StartConfig{
			SessionID: id, ProjectID: testProject, Harness: domain.HarnessOpenCode, WorkspacePath: t.TempDir(),
			ProviderConversationID: "opencode-thread", Env: map[string]string{"OPENCODE_CONFIG_CONTENT": projectConfig},
		}); err != nil {
			t.Fatal(err)
		}
		if got := resumed.Env["OPENCODE_CONFIG_CONTENT"]; got != want {
			t.Fatalf("restarted OpenCode config = %s, want %s", got, want)
		}
		return next
	}

	var started ports.ChatStartConfig
	svc := newService(&started, nil)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Harness: domain.HarnessOpenCode, WorkspacePath: t.TempDir(),
		AutoCompactWindow: "off", Env: map[string]string{"OPENCODE_CONFIG_CONTENT": projectConfig},
	}); err != nil {
		t.Fatal(err)
	}
	const off = `{"compaction":{"auto":false},"model":"a/b"}`
	if got := started.Env["OPENCODE_CONFIG_CONTENT"]; got != off {
		t.Fatalf("started OpenCode config = %s, want compaction off over the project's", got)
	}
	// A restart with Off still chosen turns it off again.
	svc = restart(svc, off)
	// Auto is saved, then the restart starts OpenCode with the project's config alone.
	if _, err := svc.SetTurnSettings(ctx, id, domain.ConversationSettings{}); err != nil {
		t.Fatal(err)
	}
	svc = restart(svc, projectConfig)
	if err := svc.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
}
