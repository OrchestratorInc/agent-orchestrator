//go:build !windows

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Exercise actual daemon wiring and subprocess admission with many durable
// idle sessions. No provider credentials or network calls are needed.
func TestIdleChatStartupDoesNotStartProviderProcesses(t *testing.T) {
	dataDir := t.TempDir()
	binDir := t.TempDir()
	calls := filepath.Join(t.TempDir(), "app-server-starts")
	workspaceRoot := t.TempDir()
	// Account/catalog probes can also invoke app-server at startup. Count only
	// children launched in these sessions' workspaces, as the Chat driver does.
	shim := "#!/bin/sh\nif [ \"$1\" = app-server ]; then\n  case \"$PWD\" in\n    \"$AO_COLD_WORKSPACE_ROOT\"/*) printf 'started\\n' >> \"$AO_COLD_PROVIDER_CALLS\" ;;\n  esac\n  exit 1\nfi\nprintf 'codex-cli 0.100.0\\n'\n"
	bin := filepath.Join(binDir, "codex")
	if err := os.WriteFile(bin, []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_CODEX_BIN", bin)
	t.Setenv("AO_COLD_PROVIDER_CALLS", calls)
	t.Setenv("AO_COLD_WORKSPACE_ROOT", workspaceRoot)
	st, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for i := range 20 {
		workspace := filepath.Join(workspaceRoot, fmt.Sprint(i))
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
			Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
			Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Metadata: domain.SessionMetadata{WorkspacePath: workspace, ProviderConversationID: fmt.Sprintf("cold-thread-%d", i), ControllerGeneration: "previous-generation"},
		})
		if err != nil {
			_ = st.Close()
			t.Fatal(err)
		}
		sessions = append(sessions, string(rec.ID))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, dataDir)
	for _, id := range sessions {
		deadline := time.Now().Add(10 * time.Second)
		for {
			var read struct {
				Session struct {
					StatusReadiness string `json:"statusReadiness"`
					IsTerminated    bool   `json:"isTerminated"`
				} `json:"session"`
			}
			d.mustCall("GET", "/sessions/"+id, http.StatusOK, nil, &read)
			if read.Session.StatusReadiness == "ready" && !read.Session.IsTerminated {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("idle session %s did not become ready", id)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if state := d.conversation(id).Controller; state != "cold" {
			t.Fatalf("idle session %s controller=%s, want cold", id, state)
		}
	}
	if data, err := os.ReadFile(calls); !os.IsNotExist(err) {
		t.Fatalf("idle startup launched provider children: %q err=%v", data, err)
	}
}
