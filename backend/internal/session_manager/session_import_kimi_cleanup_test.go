package sessionmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestDeleteImportedKimiRetainsUnverifiedAuthButDeletesReleasedSession(t *testing.T) {
	for _, kind := range []string{"missing proof", "unreadable proof", "wrong identity", "credentials directory", "foreign credentials link", "foreign oauth link"} {
		t.Run(kind, func(t *testing.T) {
			m, store, _, _ := newManager()
			m.dataDir = t.TempDir()
			source, foreign := t.TempDir(), t.TempDir()
			home := filepath.Join(m.dataDir, "native-imports", "kimi", ".native-cleanup")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, root := range []string{source, foreign} {
				for _, name := range []string{"credentials", "oauth"} {
					if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic-preserved-auth"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			proof := map[string]string{"source": source, "id": "copied", "home": filepath.Base(home)}
			if kind == "wrong identity" {
				proof["id"] = "another"
			}
			if kind == "unreadable proof" {
				if err := os.Mkdir(filepath.Join(home, ".ao-import.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if kind != "missing proof" {
				data, err := json.Marshal(proof)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".ao-import.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "credentials directory" {
				if err := os.Mkdir(filepath.Join(home, "credentials"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "credentials", "foreign"), []byte("retained"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(kind, "foreign ") {
				name := "credentials"
				if kind == "foreign oauth link" {
					name = "oauth"
				}
				if err := os.Symlink(filepath.Join(foreign, name), filepath.Join(home, name)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			agent := kimi.New()
			cfg := ports.WorkspaceHookConfig{DataDir: m.dataDir, ImportConfigDir: home, NativeSessionID: "copied"}
			if err := agent.CleanupWorkspace(t.Context(), cfg); err == nil {
				t.Fatal("fixture did not reject unverified cleanup")
			}
			m.agents = singleAgent{agent: agent}
			var warnings bytes.Buffer
			m.logger = slog.New(slog.NewTextHandler(&warnings, nil))
			store.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessKimi, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/released", ImportSource: &domain.SessionImportSource{NativeID: "source", ConfigDir: source, ResumeNativeID: "copied", ResumeConfigDir: home}}}
			store.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{"mer-1": {SessionID: "mer-1", WorkspaceDisposition: domain.DispositionRemoved}}
			if err := m.DeleteSession(t.Context(), "mer-1", false); err != nil {
				t.Fatal(err)
			}
			if _, exists := store.sessions["mer-1"]; exists {
				t.Fatal("cleanup failure prevented session deletion")
			}
			if !strings.Contains(warnings.String(), "preserving unverified files") {
				t.Fatal("cleanup refusal was not logged")
			}
			for _, root := range []string{source, foreign} {
				for _, name := range []string{"credentials", "oauth"} {
					if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != "synthetic-preserved-auth" {
						t.Fatalf("source/foreign auth changed: %v", err)
					}
				}
			}
			if kind == "credentials directory" {
				if data, err := os.ReadFile(filepath.Join(home, "credentials", "foreign")); err != nil || string(data) != "retained" {
					t.Fatalf("unverified private directory changed: %v", err)
				}
			}
		})
	}
}

func TestImportedKimiDeletionStillRequiresTerminationAndWorkspaceRelease(t *testing.T) {
	for _, terminated := range []bool{false, true} {
		m, store, _, _ := newManager()
		agent := &cleaningAgent{}
		m.agents = singleAgent{agent: agent}
		store.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessKimi, IsTerminated: terminated, Metadata: domain.SessionMetadata{WorkspacePath: "/unreleased", ImportSource: &domain.SessionImportSource{NativeID: "source"}}}
		want := ErrSessionNotTerminated
		if terminated {
			want = ErrWorkspaceNotReleased
		}
		if err := m.DeleteSession(t.Context(), "mer-1", false); !errors.Is(err, want) {
			t.Fatalf("delete error=%v want=%v", err, want)
		}
		if agent.cleanupCalls != 0 {
			t.Fatal("cleanup ran before deletion lifecycle gates")
		}
	}
}
