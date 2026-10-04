package codexappserver

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReconnectRestoresActualThreadDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		state                                *persistenthost.CodexState
		model, effort, wantModel, wantEffort string
	}{
		{"implicit", &persistenthost.CodexState{ThreadID: "actual", Model: "configured", Effort: "medium"}, "", "", "configured", "medium"},
		{"explicit", &persistenthost.CodexState{ThreadID: "actual", Model: "chosen", Effort: "high"}, "chosen", "high", "chosen", "high"},
		{"mismatched", &persistenthost.CodexState{ThreadID: "other", Model: "wrong", Effort: "low"}, "chosen", "high", "chosen", "high"},
		{"legacy unknown", nil, "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			d.persistent = true
			d.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
				return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, CodexState: tc.state}, nil
			}
			srv.reply("model/list", `{"data":[{"id":"recommended","isDefault":true,"defaultReasoningEffort":"low"},{"id":"configured","defaultReasoningEffort":"low"},{"id":"chosen","defaultReasoningEffort":"low"}]}`)
			conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{SessionID: "reconnect", ProviderConversationID: "actual", DataDir: t.TempDir(), WorkspacePath: "/tmp/ws", Model: tc.model, Effort: tc.effort})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			models, err := conv.(ports.ChatModelLister).ListModels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defaultCount := 0
			for _, model := range models {
				if !model.Default {
					continue
				}
				defaultCount++
				if model.ID != tc.wantModel || model.DefaultEffort != tc.wantEffort {
					t.Fatalf("default = %#v", model)
				}
			}
			if (tc.wantModel == "" && defaultCount != 0) || (tc.wantModel != "" && defaultCount != 1) {
				t.Fatalf("default count = %d", defaultCount)
			}
			if srv.sentMethod("thread/resume") || srv.sentMethod("config/read") {
				t.Fatal("reconnect queried or restarted settings")
			}
		})
	}
}

func TestLiveThreadSettingsUpdateReportedDefault(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/start", `{"thread":{"id":"actual"},"model":"configured","reasoningEffort":"medium"}`)
	srv.reply("model/list", `{"data":[{"id":"recommended","isDefault":true,"defaultReasoningEffort":"low"},{"id":"configured","defaultReasoningEffort":"low"},{"id":"chosen","defaultReasoningEffort":"low"}]}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	srv.push(`{"method":"thread/settings/updated","params":{"threadId":"other","threadSettings":{"model":"recommended","effort":"low"}}}`)
	srv.push(`{"method":"thread/settings/updated","params":{"threadId":"actual","threadSettings":{"model":"chosen","effort":null}}}`)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		models, err := conv.(ports.ChatModelLister).ListModels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range models {
			if model.Default && model.ID == "chosen" {
				if model.DefaultEffort != "" {
					t.Fatalf("null effort became %q", model.DefaultEffort)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("matching thread settings were not reflected")
}
