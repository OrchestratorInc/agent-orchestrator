package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// renderedReadinessRuntime serves a sequence of rendered viewports, the way a
// full-screen TUI on the alternate screen is only visible through
// GetStyledOutput while the raw output ring holds no contiguous text.
type renderedReadinessRuntime struct {
	*fakeRuntime
	screens     []string
	styledCalls int
	styledErr   error
}

func (r *renderedReadinessRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.styledCalls++
	if r.styledErr != nil {
		return "", r.styledErr
	}
	if len(r.screens) == 0 {
		return "", nil
	}
	out := r.screens[0]
	if len(r.screens) > 1 {
		r.screens = r.screens[1:]
	}
	return out, nil
}

const testTrustDialog = "Do you trust the contents of this directory?"

func newRenderedReadinessManager(rt *renderedReadinessRuntime, msg *fakeMessenger, hints ports.PromptReadinessHints) *Manager {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	return New(Deps{
		Runtime: rt,
		Agents: singleAgent{agent: readinessAgent{
			afterStartAgent: afterStartAgent{recordingAgent: &recordingAgent{}},
			hints:           hints,
		}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
}

func TestSpawn_RenderedScreenReadinessMatchesAlternateScreen(t *testing.T) {
	rt := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{outputs: []string{"\x1b[?1049h\x1b[K\r\n\x1b[K"}},
		screens:     []string{"", "\x1b[2m Grok Build \x1b[0m 1.0.77"},
	}
	msg := &fakeMessenger{}
	m := newRenderedReadinessManager(rt, msg, ports.PromptReadinessHints{
		Patterns:       []string{"Grok Build"},
		PollInterval:   time.Millisecond,
		Timeout:        time.Second,
		RenderedScreen: true,
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"}); err != nil {
		t.Fatal(err)
	}
	if rt.styledCalls < 2 {
		t.Fatalf("GetStyledOutput calls = %d, want at least 2", rt.styledCalls)
	}
	if rt.outputCalls != 0 {
		t.Fatalf("GetOutput calls = %d, want 0 when the rendered screen is available", rt.outputCalls)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "Run ao status" {
		t.Fatalf("delivered prompts = %#v, want one original prompt", msg.msgs)
	}
}

func TestSpawn_BlockingPatternHoldsReadinessUntilDialogCloses(t *testing.T) {
	rt := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{},
		screens: []string{
			testTrustDialog + "\nYes, proceed y\nNo, quit n\nGrok Build 1.0.77",
			"> \nGrok Build 1.0.77",
		},
	}
	msg := &fakeMessenger{}
	m := newRenderedReadinessManager(rt, msg, ports.PromptReadinessHints{
		Patterns:         []string{"Grok Build"},
		PollInterval:     time.Millisecond,
		Timeout:          time.Second,
		RenderedScreen:   true,
		BlockingPatterns: []string{testTrustDialog},
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"}); err != nil {
		t.Fatal(err)
	}
	if rt.styledCalls < 2 {
		t.Fatalf("GetStyledOutput calls = %d, want at least 2 (dialog, then composer)", rt.styledCalls)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "Run ao status" {
		t.Fatalf("delivered prompts = %#v, want one original prompt after the dialog closed", msg.msgs)
	}
}

func TestSpawn_BlockingPatternAtTimeoutFailsInsteadOfTypingIntoDialog(t *testing.T) {
	rt := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{},
		screens:     []string{testTrustDialog + "\nNo, quit n\nGrok Build 1.0.77"},
	}
	msg := &fakeMessenger{}
	m := newRenderedReadinessManager(rt, msg, ports.PromptReadinessHints{
		Patterns:         []string{"Grok Build"},
		PollInterval:     time.Millisecond,
		Timeout:          20 * time.Millisecond,
		RenderedScreen:   true,
		BlockingPatterns: []string{testTrustDialog},
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"})
	if !errors.Is(err, errPromptBlockedByDialog) {
		t.Fatalf("Spawn err = %v, want errPromptBlockedByDialog", err)
	}
	if !errors.Is(err, ErrSpawnDeliverPrompt) {
		t.Fatalf("Spawn err = %v, want ErrSpawnDeliverPrompt stage", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("delivered prompts = %#v, want none typed into the dialog", msg.msgs)
	}
}

func TestSpawn_RenderedScreenReadinessFallsBackToRawOutput(t *testing.T) {
	rt := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{outputs: []string{"Grok Build"}},
		styledErr:   fmt.Errorf("old host: %w", ports.ErrStyledTerminalOutputUnavailable),
	}
	msg := &fakeMessenger{}
	m := newRenderedReadinessManager(rt, msg, ports.PromptReadinessHints{
		Patterns:       []string{"Grok Build"},
		PollInterval:   time.Millisecond,
		Timeout:        time.Second,
		RenderedScreen: true,
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"}); err != nil {
		t.Fatal(err)
	}
	if rt.outputCalls != 1 {
		t.Fatalf("GetOutput calls = %d, want 1 raw fallback read", rt.outputCalls)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("delivered prompts = %#v, want one", msg.msgs)
	}
}

func TestPromptOutputContainsWrappedMatchesWrappedDialogHeading(t *testing.T) {
	cases := map[string]string{
		"single line":       "│ " + testTrustDialog + " │",
		"wrapped in border": "╭──────────────────────────────╮\r\n│ Do you trust the contents of │\r\n│ \x1b[1mthis directory?\x1b[0m              │\r\n╰──────────────────────────────╯",
		"wrapped plain":     "Do you trust the\n  contents of this\n  directory?",
	}
	for name, screen := range cases {
		if !promptOutputContainsWrapped(screen, []string{testTrustDialog}) {
			t.Errorf("%s: blocking heading not detected in %q", name, screen)
		}
	}
	if promptOutputContainsWrapped("Do you trust the\ncontents of that directory?", []string{testTrustDialog}) {
		t.Error("different heading matched")
	}
	if promptOutputContainsWrapped("Grok Build 1.0.77", []string{"", "  "}) {
		t.Error("blank blocking pattern matched")
	}
}

func TestSpawn_WrappedBlockingDialogAtTimeoutStillFails(t *testing.T) {
	rt := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{},
		screens:     []string{"│ Do you trust the contents of │\n│ this directory?              │\n│ No, quit n                   │\nGrok Build 1.0.77"},
	}
	msg := &fakeMessenger{}
	m := newRenderedReadinessManager(rt, msg, ports.PromptReadinessHints{
		Patterns:         []string{"Grok Build"},
		PollInterval:     time.Millisecond,
		Timeout:          20 * time.Millisecond,
		RenderedScreen:   true,
		BlockingPatterns: []string{testTrustDialog},
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"})
	if !errors.Is(err, errPromptBlockedByDialog) {
		t.Fatalf("Spawn err = %v, want errPromptBlockedByDialog for a wrapped dialog heading", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("delivered prompts = %#v, want none typed into the dialog", msg.msgs)
	}
}

// A caller deadline shorter than the delivery reserve skips the readiness
// wait; blocking patterns must still keep the prompt out of a startup dialog.
func TestSpawn_ShortDeadlineStillGuardsBlockingDialog(t *testing.T) {
	dialog := testTrustDialog + "\nNo, quit n\nGrok Build 1.0.77"
	cases := []struct {
		name     string
		hints    ports.PromptReadinessHints
		screen   string
		wantErr  error
		delivers bool
	}{
		{
			name:    "initial delay skip with dialog",
			hints:   ports.PromptReadinessHints{InitialDelay: time.Second, Patterns: []string{"Grok Build"}, Timeout: 8 * time.Second, RenderedScreen: true, BlockingPatterns: []string{testTrustDialog}},
			screen:  dialog,
			wantErr: errPromptBlockedByDialog,
		},
		{
			name:    "initial delay skip before anything renders",
			hints:   ports.PromptReadinessHints{InitialDelay: time.Second, Patterns: []string{"Grok Build"}, Timeout: 8 * time.Second, RenderedScreen: true, BlockingPatterns: []string{testTrustDialog}},
			screen:  "",
			wantErr: errPromptDialogUnchecked,
		},
		{
			name:    "no readiness budget with dialog",
			hints:   ports.PromptReadinessHints{Patterns: []string{"Grok Build"}, Timeout: 8 * time.Second, RenderedScreen: true, BlockingPatterns: []string{testTrustDialog}},
			screen:  dialog,
			wantErr: errPromptBlockedByDialog,
		},
		{
			name:     "initial delay skip with ready composer",
			hints:    ports.PromptReadinessHints{InitialDelay: time.Second, Patterns: []string{"Grok Build"}, Timeout: 8 * time.Second, RenderedScreen: true, BlockingPatterns: []string{testTrustDialog}},
			screen:   "> \nGrok Build 1.0.77",
			delivers: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &renderedReadinessRuntime{fakeRuntime: &fakeRuntime{}, screens: []string{tc.screen}}
			msg := &fakeMessenger{}
			m := newRenderedReadinessManager(rt, msg, tc.hints)

			spawnCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, _, err := m.Spawn(spawnCtx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "Run ao status"})
			if tc.delivers {
				if err != nil {
					t.Fatalf("Spawn: %v", err)
				}
				if len(msg.msgs) != 1 {
					t.Fatalf("delivered prompts = %#v, want one", msg.msgs)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Spawn err = %v, want %v", err, tc.wantErr)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("delivered prompts = %#v, want none typed into the dialog", msg.msgs)
			}
			if rt.styledCalls == 0 {
				t.Fatal("skipped readiness never read the screen for the blocking dialog")
			}
		})
	}
}
