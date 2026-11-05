package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type suggestionForkDriver struct {
	fakeDriver
	fork func(context.Context, string, string, ports.ChatStartConfig) (ports.ChatConversation, error)
}

func (d *suggestionForkDriver) ForkIntoHost(ctx context.Context, source, anchor string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.fork(ctx, source, anchor, cfg)
}

type suggestionParent struct {
	*fakeConversation
	mu        sync.Mutex
	deleted   []string
	deleteErr error
}

func (c *suggestionParent) NativeTurnID(id string) string { return "native-" + id }
func (c *suggestionParent) DeleteFork(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, id)
	return c.deleteErr
}

type suggestionChild struct{ *backgroundTestConversation }

func (c *suggestionChild) StartDeferredTurn(id string) error {
	c.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: id, Text: `{"prompt":"Add a regression test."}`}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, TurnState: domain.TurnStateCompleted})
	return nil
}
func newSuggestionChild() *suggestionChild {
	c := &suggestionChild{&backgroundTestConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}}}
	c.providerConversationID = "suggestion-child"
	return c
}
func newSuggestionForkHarness(t *testing.T, provider domain.AgentHarness, capability ports.ChatCapability) (*harness, *suggestionParent, *suggestionForkDriver, string) {
	t.Helper()
	st := openStore(t)
	parent := &suggestionParent{fakeConversation: newFakeConversation()}
	caps := productionCaps()
	caps[capability] = true
	parent.setCapabilities(caps)
	driver := &suggestionForkDriver{}
	driver.start = func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if cfg.SessionID == testSession {
			return parent, nil
		}
		return newSuggestionChild(), nil
	}
	dir := t.TempDir()
	if supplied := os.Getenv("AO_TEST_SUGGESTION_CRASH_DIR"); supplied != "" {
		dir = supplied
	}
	svc := chatsvc.New(chatsvc.Options{Store: st, Reader: fullSnapshotReader(st), Sessions: st, Drivers: fakeRegistry{driver: driver}, DataDir: dir, AppRunID: "suggestions", NewID: uuid.NewString})
	ctrl, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: provider, WorkspacePath: t.TempDir(), Model: "parent-model", Permissions: ports.PermissionModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeSideChats(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	h := &harness{st: st, svc: svc, ctrl: ctrl, conv: parent.fakeConversation, clock: time.Now(), rendersDir: dir}
	turnID := settledSuggestionTurn(t, h)
	return h, parent, driver, turnID
}
func suggestionForkConfig() ports.ChatStartConfig {
	return ports.ChatStartConfig{WorkspacePath: "/scratch", Permissions: ports.PermissionModeAcceptEdits, Model: "background-model", Effort: "xhigh", SystemPrompt: "Suggest only; inherited history is reference context. Do not use tools."}
}

func TestPromptSuggestionForkUsesNativeContextAndStaysInvisible(t *testing.T) {
	for _, provider := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessOpenCodeV2, domain.HarnessKimi, domain.HarnessOMP, domain.HarnessPi, domain.HarnessUnreal} {
		t.Run(string(provider), func(t *testing.T) {
			capability := ports.ChatCapabilityForkCurrent
			if provider == domain.HarnessCodex || provider == domain.HarnessUnreal {
				capability = ports.ChatCapabilityFork
			}
			h, parent, driver, turnID := newSuggestionForkHarness(t, provider, capability)
			before, err := h.svc.Snapshot(t.Context(), testSession)
			if err != nil {
				t.Fatal(err)
			}
			child := newSuggestionChild()
			driver.fork = func(ctx context.Context, source, anchor string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				wantAnchor := ""
				if capability == ports.ChatCapabilityFork {
					wantAnchor = "native-provider-turn-1"
				}
				if source != parent.ProviderConversationID() || anchor != wantAnchor {
					t.Fatalf("source/anchor=%q/%q", source, anchor)
				}
				if cfg.SessionID == testSession || !cfg.ReadOnly || !cfg.DisableNetwork || cfg.Ephemeral || !cfg.ProviderIDsScoped || cfg.Model != "parent-model" || cfg.Effort != "" || cfg.Permissions != ports.PermissionModeAcceptEdits {
					t.Fatalf("unsafe or incorrect child config: %+v", cfg)
				}
				if capability == ports.ChatCapabilityForkCurrent {
					copyCtx, release, err := cfg.AcquireForkSource(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if copyCtx.Err() != nil {
						t.Fatal(copyCtx.Err())
					}
					release()
				}
				if err := cfg.RecordFork(context.WithoutCancel(ctx), child.ProviderConversationID()); err != nil {
					t.Fatal(err)
				}
				files, err := filepath.Glob(filepath.Join(suggestionJournalDir(h), "*.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("child ID not durable before load: %v %v", files, err)
				}
				return child, nil
			}
			h.svc.SetPromptSuggestionRunner(func(ctx context.Context, id domain.SessionID, turn, system, prompt string) (string, error) {
				cfg := suggestionForkConfig()
				cfg.SystemPrompt = system
				return h.svc.RunPromptSuggestionTask(ctx, id, turn, provider, cfg, prompt)
			})
			answer, err := h.svc.PromptSuggestion(t.Context(), testSession, turnID)
			if err != nil || answer != "Add a regression test." {
				t.Fatalf("answer=%q err=%v", answer, err)
			}
			after, err := h.svc.Snapshot(t.Context(), testSession)
			if err != nil || len(after.Messages) != len(before.Messages) || len(after.Turns) != len(before.Turns) || len(parent.sentMessages()) != 1 {
				t.Fatal("suggestion changed parent history", err)
			}
			records, err := h.svc.ExportSideChatLaunch(t.Context(), "suggestions")
			if err != nil || len(records) != 0 {
				t.Fatalf("visible or retained side state: %+v %v", records, err)
			}
			if !child.terminated.Load() {
				t.Fatal("child host survived generation")
			}
			parent.mu.Lock()
			defer parent.mu.Unlock()
			if len(parent.deleted) != 1 || parent.deleted[0] != "suggestion-child" {
				t.Fatalf("wrong cleanup: %v", parent.deleted)
			}
		})
	}
}

func TestPromptSuggestionForkCancelsHungCopyWithoutBlockingParent(t *testing.T) {
	for _, action := range []string{"request cancellation", "Stop", "Kill"} {
		t.Run(action, func(t *testing.T) {
			h, _, driver, turnID := newSuggestionForkHarness(t, domain.HarnessClaudeCode, ports.ChatCapabilityForkCurrent)
			started := make(chan struct{})
			driver.fork = func(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				copyCtx, release, err := cfg.AcquireForkSource(ctx)
				if err != nil {
					return nil, err
				}
				defer release()
				close(started)
				<-copyCtx.Done()
				return nil, copyCtx.Err()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := h.svc.RunPromptSuggestionTask(ctx, testSession, turnID, domain.HarnessClaudeCode, suggestionForkConfig(), "Suggest the next message")
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("copy not started")
			}
			stopped := make(chan error, 1)
			go func() {
				switch action {
				case "request cancellation":
					cancel()
					stopped <- nil
				case "Stop":
					err := h.ctrl.Interrupt(t.Context())
					if errors.Is(err, chatsvc.ErrNoActiveTurn) {
						err = nil
					}
					stopped <- err
				case "Kill":
					stopped <- h.svc.Stop(t.Context(), testSession)
				}
			}()
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("parent command blocked on fork")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("fork did not cancel")
			}
			if action != "Kill" {
				if _, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "Parent still works", Origin: domain.MessageOriginHuman}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPromptSuggestionForkRecordsReturnedChildBeforeLoadFailure(t *testing.T) {
	h, parent, driver, turnID := newSuggestionForkHarness(t, domain.HarnessCodex, ports.ChatCapabilityFork)
	parent.deleteErr = errors.New("delete temporarily unavailable")
	var journal string
	driver.fork = func(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if err := cfg.RecordFork(context.WithoutCancel(ctx), "returned-before-load"); err != nil {
			return nil, err
		}
		files, err := filepath.Glob(filepath.Join(suggestionJournalDir(h), "*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("journal missing before load: %v %v", files, err)
		}
		journal = files[0]
		body, err := os.ReadFile(journal)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if json.Unmarshal(body, &record) != nil || record["forkId"] != "returned-before-load" || record["adopted"] != false {
			t.Fatalf("record=%s", body)
		}
		return nil, errors.New("load failed")
	}
	if _, err := h.svc.RunPromptSuggestionTask(t.Context(), testSession, turnID, domain.HarnessCodex, suggestionForkConfig(), "Suggest"); err == nil {
		t.Fatal("load failure lost")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("failed cleanup lost durable child ID", err)
	}
	records, err := h.svc.ExportSideChatLaunch(t.Context(), "suggestions")
	if err != nil || len(records) != 1 || !records[0].CleanupOnly || records[0].Harness != domain.HarnessCodex {
		t.Fatalf("cleanup ownership=%+v %v", records, err)
	}
	if strings.Contains(string(records[0].LaunchConfig), "Suggest") {
		t.Fatal("prompt persisted in cleanup record")
	}
}

func suggestionJournalDir(h *harness) string { return filepath.Join(h.rendersDir, "side-forks") }

func TestPromptSuggestionForkFallsBackWhenNativeCapabilityIsMissing(t *testing.T) {
	h, _, driver, turnID := newSuggestionForkHarness(t, domain.HarnessCursor, ports.ChatCapabilityResume)
	driver.fork = func(context.Context, string, string, ports.ChatStartConfig) (ports.ChatConversation, error) {
		t.Fatal("fork called without a negotiated capability")
		return nil, nil
	}
	var started ports.ChatStartConfig
	driver.start = func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		started = cfg
		return newSuggestionChild(), nil
	}
	answer, err := h.svc.RunPromptSuggestionTask(t.Context(), testSession, turnID, domain.HarnessCursor, suggestionForkConfig(), "Recent transcript plus suggestion instructions")
	if err != nil || answer != `{"prompt":"Add a regression test."}` || !strings.HasPrefix(string(started.SessionID), "background-") || !started.ReadOnly || !started.DisableNetwork {
		t.Fatalf("fallback=%q config=%+v err=%v", answer, started, err)
	}
}

func TestPromptSuggestionForkOmitsMovedCurrentContext(t *testing.T) {
	h, _, driver, turnID := newSuggestionForkHarness(t, domain.HarnessClaudeCode, ports.ChatCapabilityForkCurrent)
	driver.fork = func(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "New task", Origin: domain.MessageOriginHuman}); err != nil {
			t.Fatal(err)
		}
		_, _, err := cfg.AcquireForkSource(ctx)
		return nil, err
	}
	h.svc.SetPromptSuggestionRunner(func(ctx context.Context, id domain.SessionID, turn, _, prompt string) (string, error) {
		return h.svc.RunPromptSuggestionTask(ctx, id, turn, domain.HarnessClaudeCode, suggestionForkConfig(), prompt)
	})
	answer, err := h.svc.PromptSuggestion(t.Context(), testSession, turnID)
	if err != nil || answer != "" {
		t.Fatalf("stale current-context result=%q %v", answer, err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("background request reached parent")
	}
}

type waitingSuggestionChild struct {
	*backgroundTestConversation
	started chan struct{}
}

func (c *waitingSuggestionChild) StartDeferredTurn(string) error { close(c.started); return nil }

func TestPromptSuggestionForkStopsGenerationWhenParentIsKilled(t *testing.T) {
	h, _, driver, turnID := newSuggestionForkHarness(t, domain.HarnessCodex, ports.ChatCapabilityFork)
	child := &waitingSuggestionChild{backgroundTestConversation: &backgroundTestConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}}, started: make(chan struct{})}
	child.providerConversationID = "generation-child"
	driver.fork = func(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if err := cfg.RecordFork(context.WithoutCancel(ctx), child.ProviderConversationID()); err != nil {
			return nil, err
		}
		return child, nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := h.svc.RunPromptSuggestionTask(t.Context(), testSession, turnID, domain.HarnessCodex, suggestionForkConfig(), "Suggest")
		result <- err
	}()
	select {
	case <-child.started:
	case <-time.After(2 * time.Second):
		t.Fatal("generation not started")
	}
	if err := h.svc.Stop(t.Context(), testSession); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("generation error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generation survived parent Kill")
	}
	if !child.terminated.Load() {
		t.Fatal("child host survived parent Kill")
	}
}

func TestPromptSuggestionForkReturnedChildSurvivesDaemonCrash(t *testing.T) {
	if dir := os.Getenv("AO_TEST_SUGGESTION_CRASH_DIR"); dir != "" {
		h, _, driver, turnID := newSuggestionForkHarness(t, domain.HarnessCodex, ports.ChatCapabilityFork)
		driver.fork = func(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
			if err := cfg.RecordFork(context.WithoutCancel(ctx), "child-before-daemon-crash"); err != nil {
				t.Fatal(err)
			}
			os.Exit(17)
			return nil, nil
		}
		_, _ = h.svc.RunPromptSuggestionTask(t.Context(), testSession, turnID, domain.HarnessCodex, suggestionForkConfig(), "Suggest")
		t.Fatal("daemon did not crash before child load")
	}
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestPromptSuggestionForkReturnedChildSurvivesDaemonCrash$")
	cmd.Env = append(os.Environ(), "AO_TEST_SUGGESTION_CRASH_DIR="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("daemon crash=%v\n%s", err, output)
	}
	files, err := filepath.Glob(filepath.Join(dir, "side-forks", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("crash lost child ownership: %v %v", files, err)
	}
	t.Setenv("AO_TEST_SUGGESTION_CRASH_DIR", dir)
	_, parent, _, _ := newSuggestionForkHarness(t, domain.HarnessCodex, ports.ChatCapabilityFork)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		parent.mu.Lock()
		deleted := len(parent.deleted) == 1 && parent.deleted[0] == "child-before-daemon-crash"
		parent.mu.Unlock()
		if deleted {
			files, err = filepath.Glob(filepath.Join(dir, "side-forks", "*.json"))
			if err != nil || len(files) != 0 {
				t.Fatalf("retired journal=%v %v", files, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replacement daemon did not clean up returned child")
}
