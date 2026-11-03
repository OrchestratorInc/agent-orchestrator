//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type memcodeImportRuntime struct {
	*fakeRuntime
	data, session, target string
	messages              *fakeMessenger
	wrongIdentity         bool
}

func (r *memcodeImportRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	generation := r.lastCfg.Env["AO_MEMCODE_GENERATION"]
	if generation == "" {
		return "", errors.New("missing native generation in synthetic runtime")
	}
	id, sequence := "sess_deadbeef", 1
	output := "○ idle\n→  Ask memcode…   ·   $ = shell\nmemcode · glm-5.3-flash · allow-all"
	if len(r.messages.msgs) != 0 {
		id, sequence = r.target, 2
		if r.wrongIdentity {
			id = "sess_bad0bad0"
		}
		output += fmt.Sprintf("\n↩ resumed %s (1 messages)", r.target)
	}
	data, err := json.Marshal(map[string]any{"generation": generation, "native_id": id, "sequence": sequence})
	if err != nil {
		return "", err
	}
	path := filepath.Join(r.data, "agent-launches", "memcode", r.session, generation, "witness.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return output, nil
}

func TestMemcodeImportUsesActualInteractiveRestorerAfterNativeCopy(t *testing.T) {
	for _, wrongIdentity := range []bool{false, true} {
		t.Run(map[bool]string{false: "native resume", true: "wrong identity refused"}[wrongIdentity], func(t *testing.T) {
			source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("MEMCODE_ENDPOINT_URL", "")
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			// Only CI executes this synthetic version responder. fakeRuntime
			// replaces provider execution and supplies current terminal evidence.
			if err := os.WriteFile(filepath.Join(binDir, "memcode"), []byte("#!/bin/sh\necho 'memcode synthetic fixture'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			id := "sess_01234567"
			dir := filepath.Join(source, ".memcode", "sessions", id)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			body := `{"session_id":"sess_01234567","messages":[{"role":"user","content":[{"type":"text","text":"make a greeting"}]}]}`
			path := filepath.Join(dir, "messages.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			m, st, rt, msg := interactiveManager(t, false, &interactiveTestAgent{})
			m.agents = singleAgent{agent: memcode.New()}
			m.runtime = &memcodeImportRuntime{fakeRuntime: rt, data: m.dataDir, session: "mer-1", target: id, messages: msg, wrongIdentity: wrongIdentity}
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: source, CWD: source, Transferred: true, Prepared: true}}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if wrongIdentity {
				if err == nil || !st.sessions[rec.ID].NeedsImportResume() || rt.destroyed == 0 {
					t.Fatalf("wrong native identity adopted = %#v, %v, destroyed=%d", result, err, rt.destroyed)
				}
			} else {
				if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || result.Session.Metadata.AgentSessionID != id {
					t.Fatalf("actual interactive import = %#v, %v", result, err)
				}
				prepared := result.Session.Metadata.ImportSource
				if prepared.ConfigDir != source || prepared.ResumeConfigDir != workspace || prepared.ResumeNativeID != id || result.Session.Metadata.AgentSessionIDLaunchID != "launch-new" {
					t.Fatalf("native copy/generation not adopted = %+v", result.Session.Metadata)
				}
			}
			if len(msg.msgs) != 1 || msg.msgs[0] != "/resume "+id || rt.lastCfg.WorkspacePath != workspace {
				t.Fatalf("wrong interactive input or tool cwd: messages=%v cfg=%+v", msg.msgs, rt.lastCfg)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != body {
				t.Fatalf("source changed = %q, %v", data, err)
			}
		})
	}
}
