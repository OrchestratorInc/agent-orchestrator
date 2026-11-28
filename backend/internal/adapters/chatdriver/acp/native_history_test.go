package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeHistoryUsesReplayNormalizerAndStableIdentities(t *testing.T) {
	d := &Driver{cfg: Config{NativeHistoryReplay: func(_ context.Context, cfg LaunchConfig, id string) ([]acpsdk.SessionNotification, bool, error) {
		if cfg.WorkspacePath != "/worktree" || id != "native-id" {
			t.Fatalf("reader received wrong resume context: %#v, %q", cfg, id)
		}
		return []acpsdk.SessionNotification{replayUserChunk(id, "u1", "remember plum"), replayAgentChunk(id, "a1", "plum remembered")}, true, nil
	}}}
	var previous []byte
	for range 2 {
		c := replayConversation()
		if err := d.replayNativeHistory(context.Background(), c, LaunchConfig{WorkspacePath: "/worktree"}, "native-id"); err != nil {
			t.Fatal(err)
		}
		history, err := c.ReadHistory(context.Background())
		if err != nil || len(history) == 0 {
			t.Fatalf("history = %#v, %v", history, err)
		}
		var user, assistant bool
		for _, event := range history {
			if event.ProviderEventID == "" {
				t.Fatal("native replay lost durable identity")
			}
			user = user || (event.Kind == ports.ChatEventUserMessageCompleted && event.Text == "remember plum")
			assistant = assistant || (event.Kind == ports.ChatEventMessageCompleted && event.Text == "plum remembered")
		}
		if !user || !assistant {
			t.Fatalf("missing native messages: %#v", history)
		}
		serialized, err := json.Marshal(history)
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && !bytes.Equal(previous, serialized) {
			t.Fatal("reopening the same native history changed normalized events")
		}
		previous = serialized
	}
}

func TestNativeHistoryNeverMarksMissingOrMismatchedReplayLoaded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available bool
		updates   []acpsdk.SessionNotification
		err       error
		wantError bool
	}{
		{name: "ordinary resume-only session"},
		{name: "empty prepared history", available: true, wantError: true},
		{name: "wrong native ID", available: true, updates: []acpsdk.SessionNotification{replayUserChunk("different-id", "u", "hello")}, wantError: true},
		{name: "corrupt private history", available: true, err: errors.New("corrupt"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{cfg: Config{NativeHistoryReplay: func(context.Context, LaunchConfig, string) ([]acpsdk.SessionNotification, bool, error) {
				return tc.updates, tc.available, tc.err
			}}}
			c := replayConversation()
			err := d.replayNativeHistory(context.Background(), c, LaunchConfig{}, "native-id")
			if (err != nil) != tc.wantError {
				t.Fatalf("replay error = %v", err)
			}
			if _, err := c.ReadHistory(context.Background()); !errors.Is(err, ports.ErrChatHistoryUnavailable) {
				t.Fatalf("missing history was accepted: %v", err)
			}
		})
	}
}
