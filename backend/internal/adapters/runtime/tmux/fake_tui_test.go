package tmux

// The fake agent terminal drives the runtime's real SendMessage against a real
// tmux pane running a parser model of each harness class, so the delivery
// contract is exercised at the PTY level exactly as issue #34 observed it: the
// same byte stream reaches every TUI, and only the TUI's parser decides whether
// the message lands in the prompt box.
//
// Parser models:
//
//   - "codex" (crossterm-class, raw mode): \r is Enter/submit, a raw \n is a
//     Ctrl+J key event — deliberately NOT composer text and NOT paste content
//     (crossterm maps raw-mode 0x01..0x1A to Ctrl+letter and only the
//     ESC[200~..ESC[201~ envelope produces a paste event). Bracketed paste
//     content inserts verbatim, newlines included.
//
//   - "claude" (burst-heuristic class): pasted text arrives as fast chunked
//     reads, so a multi-byte read inserts its \n bytes as composer newlines,
//     while a lone \r read is a real submit. Bracketed paste inserts verbatim.
//
// The codex model is what makes issue #34 reproducible: AO's legacy keystroke
// delivery (send-keys -l + Enter) feeds it raw \n bytes that never become paste
// content, so the prompt never lands as one accepted turn. The bracketed-paste
// delivery (paste-buffer -p) hands both models the same unambiguous event.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	fakeTUIModeEnv     = "AO_FAKE_TUI_MODE"
	fakeTUICaptureEnv  = "AO_FAKE_TUI_CAPTURE"
	fakeTUIReadyMarker = "ao-fake-tui-ready"
)

// fakeTUIEvent is one line of the capture file the fake terminal appends to.
type fakeTUIEvent struct {
	Event    string `json:"event"` // "ready" or "submit"
	Mode     string `json:"mode,omitempty"`
	Composer string `json:"composer,omitempty"`
	CtrlJ    int    `json:"ctrlJ,omitempty"`
	Pastes   int    `json:"pastes,omitempty"`
}

func TestMain(m *testing.M) {
	switch os.Getenv(fakeTUIModeEnv) {
	case "codex", "codex-legacy", "claude":
		runFakeTUI(os.Getenv(fakeTUIModeEnv), os.Getenv(fakeTUICaptureEnv))
		// runFakeTUI only returns when stdin closes; the pane is going away.
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeTUI emulates the input side of an agent TUI until its pane closes.
// mode "codex" also models the Ctrl+J fate of raw \n bytes outside the paste
// contract; mode "claude" models the burst heuristic that made claude work
// with the legacy keystroke delivery.
func runFakeTUI(mode, capture string) {
	if capture == "" {
		return
	}
	raw, err := exec.LookPath("stty")
	if err == nil {
		//nolint:gosec // fixed stty invocation on the fake terminal's own tty
		stty := exec.Command(raw, "raw", "-echo")
		stty.Stdin, stty.Stdout, stty.Stderr = os.Stdin, os.Stdout, os.Stderr
		_ = stty.Run()
	}
	if mode != "codex-legacy" {
		// Announce bracketed paste support the way codex and claude both do.
		os.Stdout.WriteString("\x1b[?2004h")
	}
	appendEvent(capture, fakeTUIEvent{Event: "ready", Mode: mode})

	state := &fakeTUIState{capture: capture, mode: mode}
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			state.consume(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

type fakeTUIState struct {
	mu       sync.Mutex
	capture  string
	mode     string
	composer strings.Builder
	ctrlJ    int
	pastes   int
}

// consume parses one read() chunk with the mode's model. The paste envelope is
// mode-independent; everything outside it is where codex and claude diverge.
func (s *fakeTUIState) consume(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < len(chunk); {
		if content, size, ok := consumePasteEnvelope(chunk[i:]); ok {
			s.composer.Write(content)
			s.pastes++
			i += size
			continue
		}
		b := chunk[i]
		switch b {
		case '\r':
			if s.mode == "claude" && len(chunk) > 1 {
				// Claude's burst heuristic: a \r inside a fast multi-byte read
				// is pasted text, not a submit keystroke.
				s.composer.WriteByte(b)
			} else {
				s.submitLocked()
			}
		case '\n':
			if s.mode == "codex" || s.mode == "codex-legacy" {
				// Crossterm in raw mode maps a raw \n to Ctrl+J: it is a key
				// event outside the paste contract, never composer text.
				s.ctrlJ++
			} else {
				// Claude burst heuristic: pasted newline.
				s.composer.WriteByte(b)
			}
		default:
			s.composer.WriteByte(b)
		}
		i++
	}
}

// consumePasteEnvelope recognizes ESC[200~ ... ESC[201~ at the front of chunk
// and returns the wrapped content with the markers stripped plus the total
// envelope size. ok is false when the chunk does not start a (complete) paste
// envelope, in which case the caller treats every byte through its keystroke
// model.
func consumePasteEnvelope(chunk []byte) (content []byte, size int, ok bool) {
	const start = "\x1b[200~"
	const end = "\x1b[201~"
	if !strings.HasPrefix(string(chunk), start) {
		return nil, 0, false
	}
	rest := string(chunk[len(start):])
	idx := strings.Index(rest, end)
	if idx < 0 {
		return nil, 0, false
	}
	return []byte(rest[:idx]), len(start) + idx + len(end), true
}

func (s *fakeTUIState) submitLocked() {
	appendEvent(s.capture, fakeTUIEvent{
		Event:    "submit",
		Mode:     s.mode,
		Composer: s.composer.String(),
		CtrlJ:    s.ctrlJ,
		Pastes:   s.pastes,
	})
	s.composer.Reset()
	s.ctrlJ = 0
	s.pastes = 0
}

func appendEvent(path string, event fakeTUIEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

func readFakeTUIEvents(t *testing.T, path string) []fakeTUIEvent {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-owned capture path
	if err != nil {
		return nil
	}
	var events []fakeTUIEvent
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		var event fakeTUIEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

func waitForFakeTUIEvent(t *testing.T, path, event string, timeout time.Duration) []fakeTUIEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var found []fakeTUIEvent
		for _, e := range readFakeTUIEvents(t, path) {
			if e.Event == event {
				found = append(found, e)
			}
		}
		if len(found) > 0 {
			return found
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %q event appeared in %s within %s", event, path, timeout)
	return nil
}

// TestSendMessageDeliversMultilinePromptToFakeAgentTerminal is the issue #34
// repro: a multi-line ao send into a fake codex terminal (the harness class
// that silently ignored ao send) must land verbatim in the prompt box as one
// paste and submit exactly once. The claude model pins that the same delivery
// keeps working for the harness that already worked with legacy keystrokes.
func TestSendMessageDeliversMultilinePromptToFakeAgentTerminal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	for _, mode := range []string{"codex", "claude"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			id := "ao34-" + mode
			r := New(Options{Timeout: 5 * time.Second})
			_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})
			t.Cleanup(func() {
				if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: id}); err != nil {
					t.Logf("cleanup Destroy: %v", err)
				}
			})

			capture := filepath.Join(t.TempDir(), "capture.jsonl")
			h, err := r.Create(ctx, ports.RuntimeConfig{
				SessionID:     domain.SessionID(id),
				WorkspacePath: t.TempDir(),
				Argv:          []string{os.Args[0]},
				Env: map[string]string{
					fakeTUIModeEnv:    mode,
					fakeTUICaptureEnv: capture,
				},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			waitForFakeTUIEvent(t, capture, "ready", 10*time.Second)

			message := "check the failing test\nand push the fix"
			if err := r.SendMessage(ctx, h, message); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}

			submits := waitForFakeTUIEvent(t, capture, "submit", 10*time.Second)
			if len(submits) != 1 {
				t.Fatalf("submit events = %d, want exactly 1 (events: %+v)", len(submits), submits)
			}
			got := submits[0]
			if got.Composer != message {
				t.Fatalf("prompt box = %q, want the message delivered verbatim %q (ctrlJ=%d pastes=%d)", got.Composer, message, got.CtrlJ, got.Pastes)
			}
			if got.Pastes != 1 || got.CtrlJ != 0 {
				t.Fatalf("pastes = %d ctrlJ = %d, want the message to arrive as one bracketed paste and no raw-\\n keystrokes", got.Pastes, got.CtrlJ)
			}
		})
	}
}

// TestSendMessageLegacyKeystrokesReachNonBracketedTerminal pins the fallback:
// a pane that never enabled ?2004 keeps receiving today's keystroke stream, so
// unknown or legacy TUIs see exactly the bytes they saw before the paste
// contract. The codex parser model documents what those bytes mean over there:
// the newline arrives as Ctrl+J and only the trailing Enter submits.
func TestSendMessageLegacyKeystrokesReachNonBracketedTerminal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	id := "ao34-legacy"
	r := New(Options{Timeout: 5 * time.Second})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})
	t.Cleanup(func() {
		if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: id}); err != nil {
			t.Logf("cleanup Destroy: %v", err)
		}
	})

	capture := filepath.Join(t.TempDir(), "capture.jsonl")
	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: t.TempDir(),
		Argv:          []string{os.Args[0]},
		Env: map[string]string{
			fakeTUIModeEnv:    "codex-legacy",
			fakeTUICaptureEnv: capture,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForFakeTUIEvent(t, capture, "ready", 10*time.Second)

	message := "alpha\nbeta"
	if err := r.SendMessage(ctx, h, message); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	submits := waitForFakeTUIEvent(t, capture, "submit", 10*time.Second)
	if len(submits) != 1 {
		t.Fatalf("submit events = %d, want exactly 1", len(submits))
	}
	// The legacy contract, byte for byte: literal text, the \n as a Ctrl+J key
	// event (not composer text, not paste), then Enter submits once.
	if got, want := submits[0].Composer, "alphabeta"; !reflect.DeepEqual(got, want) {
		t.Fatalf("composer = %q, want legacy keystroke result %q", got, want)
	}
	if submits[0].CtrlJ != 1 || submits[0].Pastes != 0 {
		t.Fatalf("ctrlJ = %d pastes = %d, want 1 Ctrl+J and no paste (legacy \\n semantics)", submits[0].CtrlJ, submits[0].Pastes)
	}
}
