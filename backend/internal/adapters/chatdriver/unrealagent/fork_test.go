//go:build darwin || linux

package unrealagent

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unreallabsai/unreal-agent/harness/inbox"
	"github.com/unreallabsai/unreal-agent/harness/llm"
	"github.com/unreallabsai/unreal-agent/harness/session"
	"github.com/unreallabsai/unreal-agent/harness/sessionstore"
	"github.com/unreallabsai/unreal-agent/harness/sessionstore/localfile"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func nativeForkFixture(t *testing.T) (string, *localfile.Store) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := localfile.New(nativeSessionDirectory(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	var previous session.TurnID
	for _, step := range []struct {
		input, turn string
		kind        session.TurnType
	}{
		{"ao-request-1", "native-1", session.TurnRegular},
		{"", "native-tools", session.TurnRegular},
		{"", "native-compact", session.TurnCompaction},
		{"ao-request-2", "native-later", session.TurnRegular},
	} {
		if step.input != "" {
			if err := store.AppendInput(t.Context(), "parent", inbox.Input{ID: inbox.ID(step.input), Kind: inbox.InputExternal}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.AppendTurn(t.Context(), "parent", session.Turn{ID: session.TurnID(step.turn), PreviousTurnID: previous, Type: step.kind}); err != nil {
			t.Fatal(err)
		}
		previous = session.TurnID(step.turn)
		if err := store.AppendModelResponse(t.Context(), "parent", sessionstore.ModelResponse{TurnID: session.TurnID(step.turn), Response: llm.Response{ID: step.turn}}); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir, store
}

func TestUnrealNativeForkPreservesExactRequestBoundaryAndParent(t *testing.T) {
	dataDir, store := nativeForkFixture(t)
	parentPath := filepath.Join(nativeSessionDirectory(dataDir), "parent.session.jsonl")
	before, err := os.ReadFile(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []string{"ao-request-1", ""} {
		id, err := forkNativeSession(t.Context(), dataDir, "parent", anchor)
		if err != nil {
			t.Fatal(err)
		}
		page, err := store.Items(t.Context(), session.ID(id), 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		hasLater, hasTools, hasCompact := false, false, false
		for _, item := range page.Items {
			if item.Kind == sessionstore.ItemTurn {
				native := item.Data.(session.Turn)
				hasLater = hasLater || native.ID == "native-later"
				hasTools = hasTools || native.ID == "native-tools"
				hasCompact = hasCompact || native.ID == "native-compact"
			}
		}
		if !hasTools || !hasCompact || hasLater != (anchor == "") {
			t.Fatalf("fork lost native context or exceeded anchor: tools=%v compact=%v later=%v", hasTools, hasCompact, hasLater)
		}
		if err := deleteNativeFork(t.Context(), dataDir, "parent", id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Inspect(t.Context(), session.ID(id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("child remains: %v", err)
		}
	}
	after, err := os.ReadFile(parentPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("parent changed: %v", err)
	}
	if _, err := forkNativeSession(t.Context(), dataDir, "parent", "missing"); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatalf("missing anchor: %v", err)
	}
	if err := deleteNativeFork(t.Context(), dataDir, "parent", "parent"); err == nil {
		t.Fatal("parent deletion accepted")
	}
	if err := deleteNativeFork(t.Context(), dataDir, "parent", "../parent"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestUnrealIsolatedForkRemovesOnlyChildAfterHostFailure(t *testing.T) {
	dataDir, store := nativeForkFixture(t)
	driver := New(nil, nil)
	driver.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return nil, errors.New("host failed")
	}
	_, err := driver.ForkIntoHost(t.Context(), "parent", "ao-request-1", ports.ChatStartConfig{
		SessionID: "side", DataDir: dataDir, WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err == nil {
		t.Fatal("host failure ignored")
	}
	sessions, err := store.ListSessions(t.Context())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "parent" {
		t.Fatalf("sessions after failure: %v %v", sessions, err)
	}
}

func BenchmarkNativeForkBoundary(b *testing.B) {
	dataDir := b.TempDir()
	store, err := localfile.New(nativeSessionDirectory(dataDir))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := store.Create(b.Context(), "parent"); err != nil {
		b.Fatal(err)
	}
	if err := store.AppendInput(b.Context(), "parent", inbox.Input{ID: "request", Kind: inbox.InputExternal}); err != nil {
		b.Fatal(err)
	}
	if err := store.AppendTurn(b.Context(), "parent", session.Turn{ID: "native", Type: session.TurnRegular}); err != nil {
		b.Fatal(err)
	}
	if err := store.AppendModelResponse(b.Context(), "parent", sessionstore.ModelResponse{TurnID: "native", Response: llm.Response{ID: "large", Output: []llm.Item{{Type: llm.ItemMessage, Data: llm.Message{Role: llm.RoleAssistant, Text: strings.Repeat("x", 2*1024*1024)}}}}}); err != nil {
		b.Fatal(err)
	}
	b.Run("identifiers", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := nativeForkTurn(b.Context(), dataDir, "parent", "request"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full_SDK_history", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := store.Items(b.Context(), "parent", 0, math.MaxInt); err != nil {
				b.Fatal(err)
			}
		}
	})
}
