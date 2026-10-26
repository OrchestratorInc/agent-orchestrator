package workertransport

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestMayChangeAgentTerminal(t *testing.T) {
	for input, want := range map[string]bool{
		// Esc, alone, doubled or as the kitty protocol's Esc press and release.
		"\x1b": false, "\x1b\x1b": false, "\x1b[27u": false, "\x1b[27;1:3u": false,
		// Navigation, in normal and application cursor mode, with modifiers.
		"\x1b[A": false, "\x1b[1;5D": false, "\x1bOB": false, "\x1b[H": false, "\x1b[5~": false, "\x1b[6~": false,
		// Terminal replies: cursor position, device attributes, kitty flags,
		// a mode report, a color report, a terminal version.
		"\x1b[13;1R": false, "\x1b[?62;22c": false, "\x1b[>0;276;0c": false, "\x1b[?1u": false,
		"\x1b[?2004;1$y": false, "\x1b]11;rgb:1e1e/1e1e/1e1e\x07": false, "\x1bP>|xterm.js(5.5.0)\x1b\\": false,
		// Focus events and a kitty modifier key or key release.
		"\x1b[I": false, "\x1b[O": false, "\x1b[57441u": false, "\x1b[97;1:3u": false,
		// Typing, editing, Enter and shortcuts count.
		"a": true, "hello": true, "\x7f": true, "\r": true, "\x03": true, "\t": true, "\x1b[3~": true, "\x1b[Z": true,
		"\x1b[97u": true, "\x1b[13u": true, "\x1bOM": true, "\x1bb": true, "\x1b[200~text\x1b[201~": true,
		// Esc then typing in one frame still counts.
		"\x1bx": true, "\x1b[Ax": true,
		// An unknown sequence counts.
		"\x1b[15~": true,
	} {
		if got := mayChangeAgentTerminal([]byte(input)); got != want {
			t.Errorf("mayChangeAgentTerminal(%q) = %v, want %v", input, got, want)
		}
	}
}

// A second Esc (or a scroll, or a terminal reply) after the turn's stop proof
// does not hold a Chat switch; typing does.
func TestTerminalInputThatChangesNothingKeepsTheStopProof(t *testing.T) {
	for input, idle := range map[string]bool{
		"\x1b": true, "\x1b[A": true, "\x1b[13;1R": true, "\x1b[I": true,
		"y": false, "\r": false,
	} {
		dataDir := t.TempDir()
		supervisor := &Supervisor{DataDir: dataDir, AgentTerminalID: "agent", terminals: map[string]*terminalProcess{"agent": {}}}
		supervisor.iface.current = InterfaceTUI
		supervisor.tuiStartedAt = time.Now().Add(-time.Minute)
		supervisor.mu.Lock()
		supervisor.noteAgentTerminalInput([]byte("\x1b")) // the Esc that interrupted
		supervisor.mu.Unlock()
		if err := worker.RecordTUIStop(dataDir, time.Now()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		supervisor.mu.Lock()
		supervisor.noteAgentTerminalInput([]byte(input))
		supervisor.mu.Unlock()
		result, err := supervisor.inspectInterface()
		if err != nil {
			t.Fatal(err)
		}
		if got := result.(interfaceInspectResult).Idle; got != idle {
			t.Fatalf("input %q after the stop proof: idle = %v, want %v", input, got, idle)
		}
		if idle {
			continue
		}
		if err := supervisor.stopInterface(context.Background(), "drain"); err == nil {
			t.Fatalf("input %q after the stop proof: drain stopped the terminal", input)
		}
	}
}
