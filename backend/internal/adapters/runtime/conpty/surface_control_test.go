package conpty

import (
	"fmt"
	"strings"
	"testing"
	"time"

	vt "github.com/unixshells/vt-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func controlTestSurface(t *testing.T) *renderedSurface {
	t.Helper()
	// These fixtures contain no queries. Avoid a concurrent reader because the
	// dependency's Close is not synchronized with Read.
	surface := &renderedSurface{emulator: vt.NewSafeEmulator(60, 4)}
	t.Cleanup(func() { _ = surface.emulator.Close() })
	return surface
}

func TestRenderedSurfaceTitleDoesNotBecomeDraft(t *testing.T) {
	for _, command := range []string{"0", "2"} {
		for _, terminator := range []string{"\a", "\x1b\\"} {
			title := "\x1b]" + command + ";✳ managed-account-turn-finished" + terminator
			for split := 0; split <= len(title); split++ {
				t.Run(fmt.Sprintf("%s/%q/split-%d", command, terminator, split), func(t *testing.T) {
					surface := controlTestSurface(t)
					surface.Write([]byte("\x1b[H────────────────────────────────\r\n❯ \r\n────────────────────────────────\x1b[2;3H"))
					before := surface.Tail(4)
					surface.Write([]byte(title[:split]))
					surface.Write([]byte(title[split:]))
					if got := surface.Tail(4); got != before {
						t.Fatalf("non-visible title changed the composer: before=%q after=%q", before, got)
					}
				})
			}
		}
	}
}

func TestRenderedSurfaceControlStringsPreserveVisibleInput(t *testing.T) {
	for _, control := range []string{
		"\x1b]0;✳ title\a", "\x1b]2;title\x1b\\", "\x1b]8;;https://example.test/✳\x1b\\",
		"\x1b]0;cancelled\x18", "\x1b]0;cancelled\x1a", "\x1b]0;cancelled\x1b[0m",
	} {
		t.Run(fmt.Sprintf("%q", control), func(t *testing.T) {
			surface := controlTestSurface(t)
			surface.Write([]byte("❯ "))
			for _, b := range []byte(control + "real draft") {
				surface.Write([]byte{b})
			}
			if got := surface.Tail(4); got != "❯ real draft" {
				t.Fatalf("control parsing damaged input: %q", got)
			}
			if terminalui.LastPromptComposerState(surface.Tail(4), "❯") != terminalui.ComposerDraft {
				t.Fatal("real draft became empty")
			}
		})
	}
}

func TestRuntimeTitleCapturePreservesDrainEvidence(t *testing.T) {
	for _, draft := range []string{"", "real unsent draft"} {
		t.Run(draft, func(t *testing.T) {
			isolateRegistry(t)
			hosts := map[string]*inProcHost{}
			runtime := New(Options{Spawner: fakeSpawnerFor(t, hosts, livePID())})
			handle, err := runtime.Create(t.Context(), ports.RuntimeConfig{SessionID: "title-drain", WorkspacePath: t.TempDir(), Argv: []string{"unused"}})
			if err != nil {
				t.Fatal(err)
			}
			host := hosts[handle.ID]
			defer host.cleanup(t)
			frame := "\x1b[2J\x1b[H────────────────────────────────\r\n❯ " + draft + "\r\n────────────────────────────────\x1b[2;3H\x1b]0;✳ synthetic-title\a\x1b[4;1Hframe-ready"
			if _, err := host.pty.WriteOutput([]byte(frame)); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				output, err := runtime.GetStyledOutput(t.Context(), handle, 50)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(output, "frame-ready") {
					want := terminalui.ComposerEmpty
					if draft != "" {
						want = terminalui.ComposerDraft
					}
					if got := terminalui.LastBorderedPromptComposerState(output, "❯"); got != want {
						t.Fatalf("runtime drain evidence=%v want=%v output=%q", got, want, output)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("runtime did not publish the final capture")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
