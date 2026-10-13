package persistenthost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Regression test for the controller-leak class: a controller that stops
// reading while keeping its transport socket open. The host's provider pump
// used to block in an unbounded socket write while holding the host-wide
// mutex, so the host stopped reading client frames, the provider stalled on a
// full stdout pipe, and every later attach failed until the host process was
// killed. Recovery must instead be the designed detached state: release
// ownership, keep the journaled prompt, keep the provider child alive, and let
// a replacement controller replay the journal in full.
func TestStalledControllerReleasesOwnershipAndPreservesACPReplay(t *testing.T) {
	originalTimeout := clientWriteTimeout
	clientWriteTimeout = 750 * time.Millisecond
	t.Cleanup(func() { clientWriteTimeout = originalTimeout })

	dataDir := t.TempDir()
	cfg := Config{
		SessionID: "acp-stall", DataDir: dataDir, Workdir: t.TempDir(), Protocol: ProtocolACP,
		Env: append(os.Environ(),
			"AO_CHAT_HOST_PROVIDER_HELPER=1",
			"AO_CHAT_HOST_ACP_HELPER=1",
			// Enough bytes to exceed loopback socket buffers many times over,
			// so a controller that stops reading forces the host's provider
			// pump into a blocking socket write.
			"AO_CHAT_HOST_ACP_BURST=12000",
		),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), cfg) }()
	d := awaitDescriptor(t, dataDir, cfg.SessionID)
	t.Cleanup(func() {
		_ = Shutdown(context.Background(), dataDir, cfg.SessionID)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("host exit: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("host did not exit after shutdown")
		}
	})

	stalled := awaitAttach(t, d)
	reader := bufio.NewReader(stalled.Stdout)
	sendFrame(t, stalled, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readFrame(t, reader)
	sendFrame(t, stalled, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	_ = readFrame(t, reader)
	sendFrame(t, stalled, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"acp-session-live","prompt":[]}}`)
	// Deliberately never read again: the unread burst wedges the host's write,
	// exactly like a wedged daemon controller.

	// The full prompt must reach the host journal before the replacement
	// attaches, so the replay assertion is exact rather than timing-dependent.
	// A host wedged in the unbounded write never journals the tail and fails
	// this wait.
	const burst = 12000
	journalPath := filepath.Join(dataDir, "chat-hosts", cfg.SessionID, "acp-prompt.journal")
	journalDeadline := time.Now().Add(10 * time.Second)
	for {
		content, err := os.ReadFile(journalPath) //nolint:gosec // path is AO-owned and test-local.
		if err == nil && bytes.Count(content, []byte(`"agent_message_chunk"`)) == burst+1 {
			break
		}
		if time.Now().After(journalDeadline) {
			t.Fatalf("host never journaled the full prompt; controller transport wedged (journal has %d chunks)",
				bytes.Count(content, []byte(`"agent_message_chunk"`)))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Pre-fix this attach cannot succeed: the host's pump is wedged inside the
	// unbounded write under the host-wide mutex, so every attach either reports
	// ErrAttached or times out its handshake.
	replacement := awaitAttach(t, d)
	conn, ok := replacement.Stdin.(*net.TCPConn)
	if !ok {
		t.Fatal("persistent transport stdin is not a TCP connection")
	}
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// The stalled controller's socket received some frames it never consumed;
	// everything must reach the replacement from the host journal without
	// duplication or loss.
	replayReader := bufio.NewReader(replacement.Stdout)
	updates := 0
	var result struct {
		StopReason  string `json:"stopReason"`
		ProviderPID int    `json:"providerPid"`
	}
	for {
		frame := readFrame(t, replayReader)
		if bytes.Contains(frame, []byte(`"agent_message_chunk"`)) {
			updates++
			continue
		}
		if bytes.Contains(frame, []byte(ACPPromptResultMethod)) {
			var notification struct {
				Params struct {
					Result struct {
						StopReason  string `json:"stopReason"`
						ProviderPID int    `json:"providerPid"`
					} `json:"result"`
				} `json:"params"`
			}
			if err := json.Unmarshal(frame, &notification); err != nil {
				t.Fatalf("decode prompt result %s: %v", frame, err)
			}
			result = notification.Params.Result
			break
		}
		t.Fatalf("unexpected replay frame %s", frame)
	}
	if updates != burst+1 {
		t.Fatalf("replayed %d updates, want exactly %d (journal loss or duplication)", updates, burst+1)
	}
	if result.StopReason != "end_turn" || result.ProviderPID <= 0 {
		t.Fatalf("replayed prompt result: %+v", result)
	}

	// The host must be responsive and the provider child must be the same
	// process: reconnection happened without killing the provider host.
	if got := requestProviderPID(t, replacement, 9, "pid"); got != result.ProviderPID {
		t.Fatalf("provider pid changed across forced detach: %d -> %d", result.ProviderPID, got)
	}
	_ = replacement.Stdin.Close()
	// A real controller keeps its transport referenced for its whole life.
	// Without this the finalizer would close the stalled socket mid-test and
	// its RST would release the host even without the fix under test.
	runtime.KeepAlive(stalled)
}
