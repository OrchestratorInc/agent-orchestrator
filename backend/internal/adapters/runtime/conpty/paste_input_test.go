package conpty

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Issue #6931: AO typed long prompts as 512-rune keystroke frames. When the
// agent read its terminal slowly, macOS merged frames into 1022-byte reads that
// Claude Code treated as separate pastes and dropped or reordered. One
// bracketed paste arrives as one paste, whole.

const bracketedPasteOn = "\x1b[?2004h"

func TestModeTrackerReportsBracketedPaste(t *testing.T) {
	m := newModeTracker()
	if m.BracketedPaste() {
		t.Fatal("bracketed paste on before the program asked for it")
	}
	_, _ = m.Write([]byte(bracketedPasteOn))
	if !m.BracketedPaste() {
		t.Fatal("bracketed paste off after ?2004h")
	}
	_, _ = m.Write([]byte("\x1b[?2004l"))
	if m.BracketedPaste() {
		t.Fatal("bracketed paste on after ?2004l")
	}
}

func TestPasteInputIsBracketedWhenTheProgramAskedForIt(t *testing.T) {
	f := startServe(t, 140)
	defer f.cancel()
	if _, err := f.pty.WriteOutput([]byte(bracketedPasteOn)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.host.modes.BracketedPaste() })
	c := newTestClient(t, f.addr)
	defer c.close()

	// A paste must not be able to end itself early.
	if err := c.send(MsgPasteInput, []byte("line one\nline two\x1b[201~ tail")); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[200~line one\nline two tail\x1b[201~"
	if got := readPTYInput(t, f.pty, len(want)); got != want {
		t.Fatalf("pty input = %q, want %q", got, want)
	}
}

func TestPasteInputIsPlainWhenTheProgramDidNotAsk(t *testing.T) {
	f := startServe(t, 141)
	defer f.cancel()
	c := newTestClient(t, f.addr)
	defer c.close()

	if err := c.send(MsgPasteInput, []byte("plain text")); err != nil {
		t.Fatal(err)
	}
	if got := readPTYInput(t, f.pty, len("plain text")); got != "plain text" {
		t.Fatalf("pty input = %q, want %q", got, "plain text")
	}
}

func TestSendMessageSendsOneBracketedPaste(t *testing.T) {
	isolateRegistry(t)
	hosts := map[string]*inProcHost{}
	rt := New(Options{Spawner: fakeSpawnerFor(t, hosts, livePID())})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{SessionID: "sess-paste", WorkspacePath: "/tmp/w", Argv: []string{"sh"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := hosts["sess-paste"]
	defer h.cleanup(t)
	enableBracketedPaste(t, h)

	msg := strings.Repeat("0123456789", 300) // 3000 runes: several old 512-rune frames
	if err := rt.SendMessage(context.Background(), handle, msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	want := "\x1b[200~" + msg + "\x1b[201~\r"
	if got := readPTYInput(t, h.pty, len(want)); got != want {
		t.Fatalf("pty input = %q…, want one bracketed paste then Enter", truncate(got, 80))
	}
}

func TestSendMessageKeepsKeystrokesForOlderHosts(t *testing.T) {
	// A pty-host started by an older AO does not understand MsgPasteInput.
	isolateRegistry(t)
	hosts := map[string]*inProcHost{}
	rt := New(Options{Spawner: fakeSpawnerFor(t, hosts, livePID())})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{SessionID: "sess-old", WorkspacePath: "/tmp/w", Argv: []string{"sh"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := hosts["sess-old"]
	defer h.cleanup(t)
	enableBracketedPaste(t, h)
	rt.mu.Lock()
	rt.sessions["sess-old"].protocolVersion = conPTYStyledOutputProtocolVersion
	rt.mu.Unlock()

	if err := rt.SendMessage(context.Background(), handle, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := readPTYInput(t, h.pty, len("hello\r")); got != "hello\r" {
		t.Fatalf("pty input = %q, want plain keystrokes %q", got, "hello\r")
	}
}

// enableBracketedPaste makes the fake program ask for bracketed paste and
// waits until the host has recorded it (output reaches clients after the mode
// tracker has seen it).
func enableBracketedPaste(t *testing.T, h *inProcHost) {
	t.Helper()
	c := newTestClient(t, h.addr)
	defer c.close()
	if _, err := h.pty.WriteOutput([]byte(bracketedPasteOn)); err != nil {
		t.Fatal(err)
	}
	for {
		typ, payload := c.readFrame(t)
		if typ == MsgTerminalData && bytes.Contains(payload, []byte("2004h")) {
			return
		}
	}
}

func readPTYInput(t *testing.T, p *fakePTY, n int) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, n)
		k, _ := io.ReadFull(p.inR, buf)
		got <- string(buf[:k])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading %d bytes of pty input", n)
		return ""
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestPasteWritesKeepThePacedChunks(t *testing.T) {
	text := strings.Repeat("x", 1100)
	plain := pasteWrites(text, false)
	if len(plain) != 3 || len(plain[0]) != 512 || strings.Join(plain, "") != text {
		t.Fatalf("plain writes = %d parts, first %d bytes; want 3 parts of up to 512 runes", len(plain), len(plain[0]))
	}
	bracketed := pasteWrites(text, true)
	if len(bracketed) != 3 || strings.Join(bracketed, "") != "\x1b[200~"+text+"\x1b[201~" {
		t.Fatalf("bracketed writes = %d parts; want 3 parts that join to one paste", len(bracketed))
	}
}
