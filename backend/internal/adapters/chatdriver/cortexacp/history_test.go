package cortexacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Native-format inputs for guard regressions only, not native proof. Production
// code only reads these files and always leaves replay to native session/load.
func writeSnapshot(t *testing.T, cfg ports.ChatStartConfig, id string, fields map[string]any) string {
	t.Helper()
	dir := filepath.Join(cfg.Env["HOME"], ".snowflake", "cortex", "conversations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func nativeSnapshot(cfg ports.ChatStartConfig) map[string]any {
	return map[string]any{"session_id": "fixture-native-id", "working_directory": cfg.WorkspacePath, "history": []any{map[string]any{"id": "message-1", "role": "user", "content": []any{map[string]any{"type": "text", "text": "prior task"}}}}}
}

func TestColdRestoreRejectsLostOrMisboundNativeHistoryBeforeDriver(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, ports.ChatStartConfig, string)
	}{
		{"oversized snapshot", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.Truncate(p, maxSnapshotBytes+1); err != nil {
				t.Fatal(err)
			}
		}},
		{"snapshot directory", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate merged records cannot hide missing messages", func(t *testing.T, c ports.ChatStartConfig, p string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			s["history_length"] = 2
			writeSnapshot(t, c, "fixture-native-id", s)
			line := `{"id":"same","role":"user","content":[]}` + "\n"
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte(line+line), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"mixed journal formats cannot hide skipped records", func(t *testing.T, c ports.ChatStartConfig, p string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			s["history_length"] = 2
			writeSnapshot(t, c, "fixture-native-id", s)
			lines := `{"id":"m:0","message_id":"m","role":"user","content":[]}` + "\n" + `{"id":"other","role":"user","content":[]}` + "\n"
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"flat blocks cannot hide missing messages", func(t *testing.T, c ports.ChatStartConfig, p string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			s["history_length"] = 2
			writeSnapshot(t, c, "fixture-native-id", s)
			lines := `{"id":"m:0","message_id":"m","role":"user","content":[]}` + "\n" + `{"id":"m:1","message_id":"m","role":"user","content":[]}` + "\n"
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"truncated split journal", func(t *testing.T, c ports.ChatStartConfig, p string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			s["history_length"] = 2
			writeSnapshot(t, c, "fixture-native-id", s)
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte("{\"id\":\"one\",\"role\":\"user\",\"content\":[]}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing snapshot", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}},
		{"malformed snapshot", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.WriteFile(p, []byte(`{"session_id":`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong id", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			s["session_id"] = "other"
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"conflicting id aliases", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			s["sessionId"] = "other"
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"wrong workspace", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			s["working_directory"] = t.TempDir()
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"missing workspace", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			delete(s, "working_directory")
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"lost prior history", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"missing split journal", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			delete(s, "history")
			s["history_length"] = 1
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"corrupt journal", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte("{broken\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty overriding journal", func(t *testing.T, _ ports.ChatStartConfig, p string) {
			if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"legacy structured content would be stringified", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			s["messages"] = s["history"]
			delete(s, "history")
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
		{"corrupt history record", func(t *testing.T, c ports.ChatStartConfig, _ string) {
			s := nativeSnapshot(c)
			s["history"] = []any{nil}
			writeSnapshot(t, c, "fixture-native-id", s)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := isolatedConfig(t)
			p := writeSnapshot(t, cfg, "fixture-native-id", nativeSnapshot(cfg))
			tt.change(t, cfg, p)
			inner := &recordingDriver{err: errors.New("must not reach native load")}
			d := &candidate{driver: inner, reconnect: inner}
			resume := ports.ChatResumeConfig{ProviderConversationID: "fixture-native-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
			if _, err := d.Resume(context.Background(), resume); !errors.Is(err, ports.ErrChatResumeFailed) {
				t.Fatalf("Resume = %v", err)
			}
			if inner.resume.ProviderConversationID != "" {
				t.Fatal("unsafe history reached native load")
			}
		})
	}
}

func TestNativeHistoryGuardAcceptsInlineAndSplitWithoutChangingBytes(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "inline", true: "split"}[split], func(t *testing.T) {
			cfg := isolatedConfig(t)
			s := nativeSnapshot(cfg)
			if split {
				delete(s, "history")
				s["history_length"] = 1
				s["sessionId"] = s["session_id"]
				s["workingDirectory"] = s["working_directory"]
			}
			p := writeSnapshot(t, cfg, "fixture-native-id", s)
			if split {
				if err := os.WriteFile(p[:len(p)-5]+".history.jsonl", []byte("{\"id\":\"m:0\",\"message_id\":\"m\",\"role\":\"user\",\"content\":[]}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			resume := ports.ChatResumeConfig{ProviderConversationID: "fixture-native-id", WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
			if err := validateNativeHistory(context.Background(), resume); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("guard modified native snapshot")
			}
		})
	}
}

func TestNativeHistoryGuardRejectsPathEscapeAndCancelledContext(t *testing.T) {
	cfg := isolatedConfig(t)
	resume := ports.ChatResumeConfig{ProviderConversationID: "../escape", WorkspacePath: cfg.WorkspacePath, Env: cfg.Env}
	if err := validateNativeHistory(context.Background(), resume); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatal(err)
	}
	resume.ProviderConversationID = "fixture-native-id"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateNativeHistory(ctx, resume); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cfg.Env["HOME"], ".snowflake")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := validateNativeHistory(context.Background(), resume); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatal(err)
	}
}

func TestNativeHistoryGuardAcceptsLegacyTextMessages(t *testing.T) {
	cfg := isolatedConfig(t)
	s := nativeSnapshot(cfg)
	delete(s, "history")
	s["messages"] = []any{map[string]any{"role": "user", "content": "legacy task"}}
	writeSnapshot(t, cfg, "fixture-native-id", s)
	resume := ports.ChatResumeConfig{ProviderConversationID: "fixture-native-id", WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
	if err := validateNativeHistory(context.Background(), resume); err != nil {
		t.Fatal(err)
	}
}
